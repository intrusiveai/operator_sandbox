#!/usr/bin/env python3
"""Sign explicit candidate payloads; publication/qualification is a separate gate."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import subprocess


def sign(source, destination, fingerprint, passphrase_env=None, armor=False):
    command = ["gpg", "--batch", "--local-user", fingerprint, "--digest-algo", "SHA256"]
    options = {}
    if passphrase_env:
        if passphrase_env not in os.environ:
            raise ValueError("signing passphrase environment variable is missing")
        # Send the passphrase over stdin, never through argv or a persistent file.
        command += ["--pinentry-mode", "loopback", "--passphrase-fd", "0"]
        options["input"] = os.environ[passphrase_env] + "\n"
        options["text"] = True
    if armor:
        command.append("--armor")
    subprocess.run(command + ["--output", str(destination), "--detach-sign", str(source)],
                   check=True, **options)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--directory", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--operatorctl", type=Path, required=True)
    p.add_argument("--signing-key", required=True)
    p.add_argument("--keyring", type=Path, required=True)
    p.add_argument("--passphrase-env", help="explicit environment variable for unattended GPG signing")
    a = p.parse_args()
    if not re.fullmatch(r"[A-Fa-f0-9]{40}|[A-Fa-f0-9]{64}", a.signing_key):
        p.error("select an explicit full signing-key fingerprint")
    directory, output = a.directory.resolve(), a.output.resolve()
    if output.exists():
        p.error("output directory must be new")
    operator = str(a.operatorctl.resolve())
    output.mkdir(mode=0o700, parents=True)
    sign(directory / "release.json", directory / "release.sig", a.signing_key, a.passphrase_env)
    subprocess.run([operator, "release", "check", "--directory", str(directory),
                    "--keyring", str(a.keyring.resolve())], check=True, stdout=subprocess.DEVNULL)
    archive = output / (directory.name + ".tar.gz")
    subprocess.run([operator, "release", "archive", "--directory", str(directory),
                    "--output", str(archive)], check=True)
    checksum = output / "SHA256SUMS"
    checksum.write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + archive.name + "\n")
    sign(checksum, output / "SHA256SUMS.asc", a.signing_key, a.passphrase_env, armor=True)


if __name__ == "__main__":
    main()
