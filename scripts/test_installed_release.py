#!/usr/bin/env python3
"""Exercise real local packages without changing machine installation or accounts."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def run(args, env, **kwargs):
    return subprocess.check_output([str(a) for a in args], env=env, text=True, **kwargs).strip()


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--candidate", type=Path, required=True)
    p.add_argument("--upgrade", type=Path)
    a = p.parse_args()
    source = Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="op-install-", dir="/tmp") as temporary:
        root = Path(temporary)
        home = root / "gpg"
        home.mkdir(mode=0o700)
        env = dict(os.environ, GNUPGHOME=str(home))
        try:
            run(["gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
                 "--quick-generate-key", "Operator Installation Fixture <fixture@example.invalid>",
                 "ed25519", "sign", "0"], env, stderr=subprocess.DEVNULL)
            keys = run(["gpg", "--batch", "--with-colons", "--list-keys"], env)
            fingerprint = next(line.split(":")[9] for line in keys.splitlines() if line.startswith("fpr:"))
            keyring = root / "trusted.gpg"
            subprocess.run(["gpg", "--batch", "--output", str(keyring), "--export"], env=env, check=True)
            archives, manifests = [], []
            candidates = [a.candidate] + ([a.upgrade] if a.upgrade else [])
            for index, candidate in enumerate(candidates):
                copied = root / f"candidate-{index}" / candidate.name
                shutil.copytree(candidate, copied)
                output = root / f"signed-{index}"
                run(["python3", source / "scripts/sign_host_release.py", "--directory", copied,
                     "--output", output, "--operatorctl", copied / "bin/operatorctl",
                     "--signing-key", fingerprint, "--keyring", keyring], env)
                archives.append(next(output.glob("*.tar.gz")))
                manifests.append(json.loads((copied / "release.json").read_text()))
            executable = a.candidate.resolve() / "bin/operatorctl"
            common = ["--root", root / "installation with spaces and 'quote", "--config", root / "config/config.yaml",
                      "--state-root", root / "state", "--image", "fixture/local:harness", "--keyring", keyring]
            first = json.loads(run([executable, "install", "--archive", archives[0], *common], env))
            command = Path(first["command"])
            version = json.loads(run([command, "version"], env))
            assert version["version"] == manifests[0]["version"]
            assert version["contract_digest"] == manifests[0]["contract"]["package_digest"]
            run([command, "contract", "check", "--package-dir", Path(first["release_directory"]) / "contract",
                 "--package-version", version["contract_version"], "--package-digest", version["contract_digest"]], env)
            config = root / "config/config.yaml"
            old_config = config.read_bytes()
            evidence = root / "state/retained-evidence"
            evidence.write_text("preserved")
            repeat = json.loads(run([command, "install", "--archive", archives[0], *common], env))
            assert repeat["configuration"] == "preserved"
            if len(archives) > 1:
                run([command, "install", "--archive", archives[1], *common], env)
                assert json.loads(run([command, "version"], env))["version"] == manifests[1]["version"]
                assert json.loads(run([Path(first["release_directory"]) / "bin/operatorctl", "version"], env))["version"] == manifests[0]["version"]
                rejected = subprocess.run([str(x) for x in [command, "install", "--archive", archives[0], *common]], env=env, capture_output=True)
                assert rejected.returncode != 0
                run([command, "install", "--archive", archives[0], "--allow-downgrade", *common], env)
            assert config.read_bytes() == old_config and evidence.read_text() == "preserved"
            corrupted = root / "corrupt.tar.gz"
            corrupted.write_bytes(archives[0].read_bytes()[:100])
            rejected = subprocess.run([str(x) for x in [command, "install", "--archive", corrupted, *common]], env=env, capture_output=True)
            assert rejected.returncode != 0
            assert json.loads(run([command, "version"], env))["version"] == manifests[0]["version"]
            print(json.dumps({"status": "installed-package-tests-passed", "versions": [m["version"] for m in manifests],
                              "platform": manifests[0]["platform"], "native_runtime_qualified": False}))
        finally:
            subprocess.run(["gpgconf", "--homedir", str(home), "--kill", "gpg-agent"], env=env, check=False)


if __name__ == "__main__":
    main()
