#!/usr/bin/env python3
"""Build a reproducible, unapproved host candidate from an immutable checkout."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

TOOLCHAIN = "go1.26.5"
PLATFORMS = ("linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")


def run(args, root, env=None):
    return subprocess.check_output(args, cwd=root, env=env, text=True).strip()


def write_json(path, value):
    path.write_text(json.dumps(value, sort_keys=True, indent=2) + "\n")


def module_stream(raw):
    decoder = json.JSONDecoder()
    values = []
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        if "Replace" in item:
            raise ValueError("release dependencies cannot use replacements")
        values.append(item)
    return sorted({m["Path"]: m for m in values}.values(), key=lambda m: m["Path"])


def sbom(modules, version, platform, commit):
    packages = []
    for index, module in enumerate(modules):
        package = {"SPDXID": f"SPDXRef-Module-{index}", "name": module["Path"],
                   "versionInfo": module.get("Version", version),
                   "downloadLocation": "NOASSERTION", "filesAnalyzed": False,
                   "licenseConcluded": "NOASSERTION", "licenseDeclared": "NOASSERTION",
                   "copyrightText": "NOASSERTION"}
        if module.get("Sum"):
            package["sourceInfo"] = "Go module content sum: " + module["Sum"]
        packages.append(package)
    packages.append({"SPDXID": "SPDXRef-Go", "name": "Go toolchain", "versionInfo": TOOLCHAIN,
                     "downloadLocation": "NOASSERTION", "filesAnalyzed": False,
                     "licenseConcluded": "NOASSERTION", "licenseDeclared": "NOASSERTION",
                     "copyrightText": "NOASSERTION"})
    return {"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT",
            "name": f"operator-{version}-{platform}",
            "documentNamespace": f"https://intrusive.ai/spdx/operator/{commit}/{version}/{platform}",
            "creationInfo": {"created": "1970-01-01T00:00:00Z", "creators": ["Tool: operator-release-builder"]},
            "packages": packages,
            "relationships": [{"spdxElementId": "SPDXRef-DOCUMENT", "relationshipType": "DESCRIBES",
                               "relatedSpdxElement": p["SPDXID"]} for p in packages]}


def build(root, output, version, platforms, python):
    if not re.fullmatch(r"(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})", version):
        raise ValueError("host version must be numeric major.minor.patch")
    if output.exists():
        raise ValueError("output must be a new directory")
    if run(["git", "status", "--porcelain", "--untracked-files=all"], root):
        raise ValueError("release source must be a clean committed checkout")
    commit = run(["git", "rev-parse", "HEAD"], root)
    env = dict(os.environ, GOTOOLCHAIN="local", GOWORK="off", GOFLAGS="", CGO_ENABLED="0",
               GOPROXY="off", GOSUMDB="off", GOAMD64="v1", GOARM64="v8.0")
    env.pop("GOOS", None)
    env.pop("GOARCH", None)
    if run(["go", "env", "GOVERSION"], root, env) != TOOLCHAIN:
        raise ValueError("release builds require " + TOOLCHAIN)
    run(["go", "mod", "verify"], root, env)
    expected = json.loads((root / "release/contract-lock.json").read_text())["package"]
    output.mkdir(mode=0o700, parents=True)
    with tempfile.TemporaryDirectory(prefix="operator-release-build-") as temp:
        temp = Path(temp)
        helper = temp / "operatorctl"
        run(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", str(helper), "./cmd/operatorctl"], root, env)
        contract = temp / "contract"
        report = json.loads(run([str(helper), "contract", "build", "--source", str(root),
                                "--output", str(contract), "--package-version", expected["package_version"]], root, env))
        if report["package"]["package_digest"] != expected["package_digest"]:
            raise ValueError("contract source differs from approved candidate pin")
        run(["go", "test", "./contracts/..."], contract / "source", env)
        test_env = dict(env, PYTHONDONTWRITEBYTECODE="1", PYTHONPATH=str(contract / "source/contracts/python"))
        run([str(python), "-m", "unittest", "discover", "-s", "contracts/python/tests"], contract / "source", test_env)
        for platform in platforms:
            goos, arch = platform.split("/")
            dest = output / platform.replace("/", "-")
            (dest / "bin").mkdir(parents=True, mode=0o700)
            shutil.copytree(contract, dest / "contract")
            shutil.copytree(root / "release/templates", dest / "templates")
            for source in (root / "examples").glob("*"):
                shutil.copyfile(source, dest / "templates" / source.name)
            shutil.copytree(root / "docs", dest / "docs")
            flags = " ".join(["-s", "-w", "-buildid=", "-X", "main.operatorVersion=" + version,
                              "-X", "main.releaseSourceCommit=" + commit,
                              "-X", "github.com/intrusiveai/operator_sandbox/internal/contractstore.SupportedVersion=" + expected["package_version"],
                              "-X", "github.com/intrusiveai/operator_sandbox/internal/contractstore.SupportedDigest=" + expected["package_digest"]])
            target_env = dict(env, GOOS=goos, GOARCH=arch)
            modules = module_stream(run(["go", "list", "-mod=readonly", "-deps", "-f", "{{with .Module}}{{json .}}{{end}}", "./cmd/operatorctl"], root, target_env))
            run(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", flags,
                 "-o", str(dest / "bin/operatorctl"), "./cmd/operatorctl"], root, target_env)
            write_json(dest / "sbom.spdx.json", sbom(modules, version, platform, commit))
            binary_digest = hashlib.sha256((dest / "bin/operatorctl").read_bytes()).hexdigest()
            write_json(dest / "provenance.json", {"_type": "https://in-toto.io/Statement/v1",
                "subject": [{"name": "bin/operatorctl", "digest": {"sha256": binary_digest}}],
                "predicateType": "https://slsa.dev/provenance/v1",
                "predicate": {"buildDefinition": {"buildType": "https://intrusive.ai/build/operator-host/v1",
                    "externalParameters": {"version": version, "platform": platform, "contract": expected},
                    "internalParameters": {"toolchain": TOOLCHAIN, "cgo_enabled": False},
                    "resolvedDependencies": [{"uri": "git+https://github.com/intrusiveai/operator_sandbox",
                        "digest": {"gitCommit": commit}}]},
                    "runDetails": {"builder": {"id": "https://github.com/intrusiveai/operator_sandbox/scripts/build_host_release.py"}}}})
            for name in dest.rglob("*"):
                name.chmod(0o700 if name.is_dir() or name == dest / "bin/operatorctl" else 0o600)
            run([str(helper), "release", "manifest", "--directory", str(dest), "--version", version,
                 "--platform", platform, "--source-commit", commit,
                 "--contract-version", expected["package_version"],
                 "--contract-digest", expected["package_digest"]], root, env)
    print(json.dumps({"status": "built-not-qualified", "source_commit": commit,
                      "platforms": platforms, "output": str(output)}, sort_keys=True))


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[1])
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--platform", choices=PLATFORMS, action="append")
    p.add_argument("--python", type=Path, help="prepared interpreter with locked contract dependencies")
    a = p.parse_args()
    build(a.source.resolve(), a.output.resolve(), a.version, a.platform or PLATFORMS,
          (a.python or a.source / ".venv/bin/python").absolute())


if __name__ == "__main__":
    main()
