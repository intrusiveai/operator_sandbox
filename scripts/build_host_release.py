#!/usr/bin/env python3
"""Build a reproducible, unapproved host candidate from an immutable checkout."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import shlex
import signal
import sys
import time
import subprocess
import tempfile
from native_build import native_environment, native_platform, inspect_binary

TOOLCHAIN = "go1.26.5"
PLATFORMS = ("linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")


# Bounds apply to individual commands; CI also retains its overall job deadline.
COMMAND_TIMEOUT = 120
BUILD_TIMEOUT = 600
TEST_TIMEOUT = 300


def progress(message):
    print("[host-release] " + message, file=sys.stderr, flush=True)


def run(args, root, env=None, *, timeout=COMMAND_TIMEOUT, capture=True):
    command = shlex.join(str(arg) for arg in args)
    progress(f"START (limit {timeout}s): {command}")
    started = time.monotonic()
    status = "FAILED"
    process = None
    try:
        process = subprocess.Popen(args, cwd=root, env=env, text=True,
                                   stdout=subprocess.PIPE if capture else None,
                                   start_new_session=True)
        try:
            output, _ = process.communicate(timeout=timeout)
        except BaseException:
            # Kill the compiler/test descendants too, not just their parent.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=5)
            raise
        if process.returncode:
            if output:
                print(output[-8192:], file=sys.stderr, flush=True)
            raise subprocess.CalledProcessError(process.returncode, args)
        status = "OK"
        return (output or "").strip()
    except subprocess.TimeoutExpired as exc:
        status = "TIMEOUT"
        raise RuntimeError(f"command exceeded {timeout}s: {command}") from exc
    finally:
        if process is not None and process.stdout is not None:
            process.stdout.close()
        progress(f"{status} after {time.monotonic() - started:.1f}s: {command}")


def write_json(path, value):
    path.write_text(json.dumps(value, sort_keys=True, indent=2) + "\n")


def module_stream(raw):
    decoder = json.JSONDecoder()
    values = []
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        if "Module" in item:
            item = item["Module"]
        if not item or "Path" not in item:
            continue
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


def build(root, output, version, platforms, python, *, validate_contract=True):
    if not re.fullmatch(r"(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})", version):
        raise ValueError("host version must be numeric major.minor.patch")
    if output.exists():
        raise ValueError("output must be a new directory")
    if list(platforms) != [native_platform()]:
        raise ValueError("build exactly the platform of this native runner")
    if run(["git", "status", "--porcelain", "--untracked-files=all"], root):
        raise ValueError("release source must be a clean committed checkout")
    commit = run(["git", "rev-parse", "HEAD"], root)
    env, native = native_environment(platforms[0])
    env.update(GOPROXY="off", GOSUMDB="off")
    if run(["go", "env", "GOVERSION"], root, env) != TOOLCHAIN:
        raise ValueError("release builds require " + TOOLCHAIN)
    run(["go", "mod", "verify"], root, env, capture=False)
    expected = json.loads((root / "release/contract-lock.json").read_text())["package"]
    # Validate the approved interface independently of product source identity.
    run([str(python), "scripts/check_harness_contract.py", "--document-only"], root, env)
    from interceptor_contract import identity, validate_pin, PIN, DOCUMENT
    integration_pin = validate_pin(json.loads((root / PIN).read_text()))
    if identity((root / DOCUMENT).read_bytes()) != integration_pin:
        raise ValueError("Interceptor OpenAPI bytes differ from the approved source pin")
    output.mkdir(mode=0o700, parents=True)
    with tempfile.TemporaryDirectory(prefix="operator-release-build-") as temp:
        temp = Path(temp)
        helper = temp / "operatorctl"
        run(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", str(helper), "./cmd/operatorctl"], root, env, timeout=BUILD_TIMEOUT, capture=False)
        interceptor_identity = json.loads(run([str(helper), "version"], root, env))
        interceptor_contract = interceptor_identity["interceptor_contract_version"]
        if any(interceptor_identity.get(k) != v for k, v in integration_pin.items()):
            raise ValueError("compiled Interceptor identity differs from source approval")
        contract = temp / "contract"
        report = json.loads(run([str(helper), "contract", "build", "--source", str(root),
                                "--output", str(contract), "--package-version", expected["package_version"]], root, env))
        if report["package"]["package_digest"] != expected["package_digest"]:
            raise ValueError("contract source differs from approved candidate pin")
        if validate_contract:
            run(["go", "test", "-v", "-timeout", "4m", "./contracts/..."], contract / "source", env,
                timeout=TEST_TIMEOUT, capture=False)
            test_env = dict(env, PYTHONDONTWRITEBYTECODE="1", PYTHONPATH=str(contract / "source/contracts/python"))
            run([str(python), "-m", "unittest", "discover", "-v", "-s", "contracts/python/tests"],
                contract / "source", test_env, timeout=TEST_TIMEOUT, capture=False)
        else:
            progress("Reproducibility rebuild: contract tests passed in the first build; comparing all rebuilt bytes afterward")
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
            modules = module_stream(run(["go", "list", "-mod=readonly", "-deps", "-json=Module", "./cmd/operatorctl"], root, target_env))
            run(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", flags,
                 "-o", str(dest / "bin/operatorctl"), "./cmd/operatorctl"], root, target_env, timeout=BUILD_TIMEOUT, capture=False)
            write_json(dest / "sbom.spdx.json", sbom(modules, version, platform, commit))
            binary_digest = hashlib.sha256((dest / "bin/operatorctl").read_bytes()).hexdigest()
            write_json(dest / "provenance.json", {"_type": "https://in-toto.io/Statement/v1",
                "subject": [{"name": "bin/operatorctl", "digest": {"sha256": binary_digest}}],
                "predicateType": "https://slsa.dev/provenance/v1",
                "predicate": {"buildDefinition": {"buildType": "https://intrusive.ai/build/operator-host/v1",
                    "externalParameters": {"version": version, "platform": platform, "contract": expected, "interceptor_contract_version": interceptor_contract, "interceptor_contract_sha256": interceptor_identity["interceptor_contract_sha256"]},
                    "internalParameters": {"toolchain": TOOLCHAIN, **native, **inspect_binary(dest / "bin/operatorctl", platform)},
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


def compare_outputs(first, second):
    def inventory(root):
        result = {}
        for path in sorted(root.rglob("*")):
            if path.is_symlink() or not (path.is_file() or path.is_dir()):
                raise ValueError("unexpected release output: " + str(path))
            digest = None
            if path.is_file():
                with path.open("rb") as stream:
                    digest = hashlib.file_digest(stream, "sha256").hexdigest()
            result[str(path.relative_to(root))] = (path.stat().st_mode, digest)
        return result
    progress("Comparing complete first and second build inventories")
    left, right = inventory(first), inventory(second)
    changed = sorted(name for name in left.keys() | right.keys() if left.get(name) != right.get(name))
    if changed:
        raise ValueError("release builds differ: " + ", ".join(changed[:10]))
    progress("Reproducibility passed: all output bytes, paths and modes match")


def build_candidates(root, output, version, platforms, python, reproduce_output=None):
    if reproduce_output is not None:
        first, second = output.resolve(), reproduce_output.resolve()
        if first == second or first in second.parents or second in first.parents or second.exists():
            raise ValueError("reproducibility output must be a separate new directory")
    progress("Building and validating the native candidate")
    build(root, output, version, platforms, python)
    if reproduce_output is not None:
        progress("Building the reproducibility candidate")
        build(root, reproduce_output, version, platforms, python, validate_contract=False)
        compare_outputs(output, reproduce_output)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[1])
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--platform", choices=PLATFORMS, action="append")
    p.add_argument("--python", type=Path, help="prepared interpreter with locked contract dependencies")
    p.add_argument("--reproduce-output", type=Path, help="rebuild into a separate new directory and compare all output bytes; contract tests run once")
    a = p.parse_args()
    def terminate(signum, frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, terminate)
    build_candidates(a.source.resolve(), a.output.resolve(), a.version, a.platform or [native_platform()],
          (a.python or a.source / ".venv/bin/python").absolute(),
          a.reproduce_output.resolve() if a.reproduce_output else None)


if __name__ == "__main__":
    main()
