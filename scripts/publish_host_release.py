#!/usr/bin/env python3
"""Validate four signed host distributions and prepare a GitHub draft release."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile

PLATFORMS = {"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}


def check_candidates(hosts, harnesses):
    if {h["platform"] for h in hosts} != PLATFORMS or len(hosts) != 4:
        raise ValueError("four distinct host platforms are required")
    identity = [(h["version"], h["source_commit"], h["contract"]) for h in hosts]
    if any(item != identity[0] for item in identity):
        raise ValueError("host source/version/contract identities differ")
    if len(harnesses) != 2 or {h.get("platform") for h in harnesses} != {"linux/amd64", "linux/arm64"}:
        raise ValueError("both native harness image build reports are required")
    for h in harnesses:
        if h.get("api_version") != "intrusive.ai/attack-harness-build-report/v1alpha1":
            raise ValueError("invalid harness build report")
        if any(h.get("contract", {}).get(k) != v for k, v in hosts[0]["contract"].items()):
            raise ValueError("harness build contract differs from host")
        candidate = h.get("compatibility_candidate", {})
        if candidate.get("image_digest") != h.get("oci", {}).get("image_digest") or not re.fullmatch(r"sha256:[a-f0-9]{64}", candidate.get("image_digest", "")):
            raise ValueError("harness image identity is incomplete")
        if candidate.get("contract_package_version") != hosts[0]["contract"]["package_version"] or candidate.get("contract_package_digest") != hosts[0]["contract"]["package_digest"]:
            raise ValueError("harness compatibility candidate differs from host")
        if candidate.get("platform") != h["platform"] or candidate.get("runtime_profile") != "operator-container/v1":
            raise ValueError("harness runtime/platform is incompatible")
        minimum = candidate.get("minimum_operator_version", "")
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", minimum) or tuple(map(int, minimum.split("."))) > tuple(map(int, hosts[0]["version"].split("."))):
            raise ValueError("harness requires a newer Operator")
    return identity[0][0]


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--signed-directory", type=Path, action="append", required=True)
    p.add_argument("--harness-report", type=Path, action="append", required=True)
    p.add_argument("--operatorctl", type=Path, required=True)
    p.add_argument("--keyring", type=Path, required=True)
    p.add_argument("--create-draft", action="store_true")
    a = p.parse_args()
    keyring, operator = str(a.keyring.resolve()), str(a.operatorctl.resolve())
    hosts, assets = [], []
    with tempfile.TemporaryDirectory(prefix="operator-publish-verify-") as temp:
        for directory in a.signed_directory:
            directory = directory.resolve()
            sums, signature = directory / "SHA256SUMS", directory / "SHA256SUMS.asc"
            subprocess.run(["gpgv", "--homedir", temp, "--keyring", keyring,
                            "--weak-digest", "SHA1", "--weak-digest", "MD5",
                            "--", str(signature), str(sums)], check=True)
            match = re.fullmatch(r"([a-f0-9]{64})  ([a-z0-9-]+\.tar\.gz)\n", sums.read_text())
            if not match:
                raise ValueError("unexpected archive checksum list")
            archive = directory / match[2]
            with archive.open("rb") as stream:
                if hashlib.file_digest(stream, "sha256").hexdigest() != match[1]:
                    raise ValueError("archive checksum differs")
            host = json.loads(subprocess.check_output([operator, "release", "check", "--archive",
                             str(archive), "--keyring", keyring], text=True))
            hosts.append(host)
            # GitHub asset basenames must be unique across platform directories.
            slug = host["platform"].replace("/", "-")
            named_sums = Path(temp) / (slug + "-SHA256SUMS")
            named_signature = Path(temp) / (slug + "-SHA256SUMS.asc")
            named_sums.write_bytes(sums.read_bytes())
            named_signature.write_bytes(signature.read_bytes())
            assets.extend([str(archive), str(named_sums), str(named_signature)])
        harnesses = [json.loads(file.read_text()) for file in a.harness_report]
        version = check_candidates(hosts, harnesses)
        if a.create_draft:
            tag_commit = subprocess.check_output(["gh", "api", "repos/intrusiveai/operator_sandbox/commits/v" + version,
                                                 "--jq", ".sha"], text=True).strip()
            if tag_commit != hosts[0]["source_commit"]:
                raise ValueError("release tag does not identify the signed source commit")
            notes = Path(temp) / "notes.md"
            notes.write_text("Signed host candidates with matching harness contract pins.\n\n"
                             "Native host/provider/target qualification and image release approval "
                             "must be reviewed before publication. This draft is not runtime approval.\n")
            subprocess.run(["gh", "release", "create", "v" + version, *assets,
                            "--repo", "intrusiveai/operator_sandbox", "--draft", "--verify-tag",
                            "--title", "Operator Sandbox " + version, "--notes-file", str(notes)], check=True)
    print(json.dumps({"status": "draft-created" if a.create_draft else "publication-inputs-verified",
                      "version": version, "qualified": False}))


if __name__ == "__main__":
    main()
