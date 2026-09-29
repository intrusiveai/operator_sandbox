#!/usr/bin/env python3
"""Sign explicit candidate payloads; publication/qualification is a separate gate."""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--directory", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--operatorctl", type=Path, required=True)
    p.add_argument("--signing-key", required=True)
    p.add_argument("--keyring", type=Path, required=True)
    a = p.parse_args()
    if not re.fullmatch(r"[A-Fa-f0-9]{40}|[A-Fa-f0-9]{64}", a.signing_key):
        p.error("select an explicit full signing-key fingerprint")
    directory, output = a.directory.resolve(), a.output.resolve()
    if output.exists():
        p.error("output directory must be new")
    operator = str(a.operatorctl.resolve())
    output.mkdir(mode=0o700, parents=True)
    subprocess.run(["gpg", "--batch", "--local-user", a.signing_key,
                    "--digest-algo", "SHA256", "--output", str(directory / "release.sig"),
                    "--detach-sign", str(directory / "release.json")], check=True)
    subprocess.run([operator, "release", "check", "--directory", str(directory),
                    "--keyring", str(a.keyring.resolve())], check=True)
    archive = output / (directory.name + ".tar.gz")
    subprocess.run([operator, "release", "archive", "--directory", str(directory),
                    "--output", str(archive)], check=True)
    checksum = output / "SHA256SUMS"
    checksum.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + archive.name + "\n")
    subprocess.run(["gpg", "--batch", "--local-user", a.signing_key,
                    "--digest-algo", "SHA256", "--armor", "--output", str(output / "SHA256SUMS.asc"),
                    "--detach-sign", str(checksum)], check=True)


if __name__ == "__main__":
    main()
