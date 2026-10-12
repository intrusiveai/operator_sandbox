#!/usr/bin/env python3
"""Publish a rolling signed candidate, or promote its existing GitHub assets."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

from sign_host_release import sign

PLATFORMS = {"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}
VERSION = r"(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})"
MARKER = "<!-- operator-candidate-release/v1 -->"


def candidate_version(branch):
    if not re.fullmatch("candidate-v" + VERSION, branch):
        raise ValueError("candidate branch must be candidate-vMAJOR.MINOR.PATCH")
    return branch.removeprefix("candidate-v")


def event_context(event, name, ref, sha, repository):
    result = {"mode": "verify", "version": "0.1.0", "source": sha, "branch": ref.removeprefix("refs/heads/"), "pr": ""}
    if name == "pull_request":
        pr = event["pull_request"]
        if event["action"] == "closed":
            result["mode"] = "ignore"
            if pr["merged"] and pr["base"]["ref"] == "main" and pr["head"]["ref"].startswith("candidate-v"):
                if pr["head"]["repo"]["full_name"] != repository:
                    raise ValueError("release candidates must belong to this repository")
                result.update(mode="promote", version=candidate_version(pr["head"]["ref"]),
                              source=pr["head"]["sha"], branch=pr["head"]["ref"], pr=str(pr["number"]))
        elif pr["base"]["ref"].startswith("candidate-v"):
            result["version"] = candidate_version(pr["base"]["ref"])
        elif pr["base"]["ref"] == "main" and pr["head"]["ref"].startswith("candidate-v"):
            result["version"] = candidate_version(pr["head"]["ref"])
    elif name in ("push", "workflow_dispatch") and result["branch"].startswith("candidate-v"):
        if event.get("deleted"):
            result["mode"] = "ignore"
        else:
            result.update(mode="publish", version=candidate_version(result["branch"]))
    return result


def check_candidates(hosts):
    if len(hosts) != 4 or {h["platform"] for h in hosts} != PLATFORMS:
        raise ValueError("four distinct host platforms are required")
    identity = [(h["version"], h["source_commit"], h["contract"]) for h in hosts]
    if any(item != identity[0] for item in identity):
        raise ValueError("host source/version/contract identities differ")
    return identity[0][0]


class GitHub:
    def __init__(self, repository):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise ValueError("invalid GitHub repository")
        self.repository = repository
        self.prefix = "repos/" + repository + "/"

    def api(self, path, method="GET", data=None, missing=False):
        command = ["gh", "api", self.prefix + path, "--method", method]
        if data is not None:
            command += ["--input", "-"]
        result = subprocess.run(command, input=json.dumps(data) if data is not None else None,
                                text=True, capture_output=True)
        if result.returncode:
            if missing and "(HTTP 404)" in result.stderr:
                return None
            raise RuntimeError("GitHub request failed: " + method + " " + path)
        return json.loads(result.stdout) if result.stdout.strip() else None

    def upload(self, tag, files):
        subprocess.run(["gh", "release", "upload", tag, *map(str, files),
                        "--repo", self.repository], check=True)

    def release(self, tag):
        return self.api("releases/tags/" + tag, missing=True)

    def assets(self, release):
        return self.api(f"releases/{release['id']}/assets?per_page=100")


def checksum(path):
    with path.open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def assemble(directory, output, keyring, fingerprint, operatorctl=None):
    """Reuse the existing manifest verifier, signer and archive builder."""
    directories = [directory / p.replace("/", "-") for p in sorted(PLATFORMS)]
    hosts = [json.loads((d / "release.json").read_text()) for d in directories]
    version = check_candidates(hosts)
    if any(h["platform"].replace("/", "-") != d.name for h, d in zip(hosts, directories)):
        raise ValueError("candidate platform differs from its directory")
    operator = str((operatorctl or directory / "linux-amd64/bin/operatorctl").resolve())
    output.mkdir(mode=0o700)
    for d in directories:
        subprocess.run(["python3", str(Path(__file__).with_name("sign_host_release.py")),
                        "--directory", str(d), "--output", str(output / d.name),
                        "--operatorctl", operator, "--signing-key", fingerprint,
                        "--keyring", str(keyring), "--passphrase-env", "RELEASE_SIGNING_PASSPHRASE"], check=True)
        # Keep archive filenames identical to the signed checksum contents.
        (output / d.name / (d.name + ".tar.gz")).rename(output / (d.name + ".tar.gz"))
        for suffix in ("SHA256SUMS", "SHA256SUMS.asc"):
            (output / d.name / suffix).rename(output / (d.name + "-" + suffix))
        (output / d.name).rmdir()
    files = sorted(output.iterdir())
    inventory = {"version": version, "source_commit": hosts[0]["source_commit"],
                 "contract": hosts[0]["contract"], "assets": {f.name: checksum(f) for f in files}}
    record = output / "candidate.json"
    record.write_text(json.dumps(inventory, sort_keys=True, indent=2) + "\n")
    sign(record, output / "candidate.json.asc", fingerprint, "RELEASE_SIGNING_PASSPHRASE", armor=True)
    return inventory, sorted(output.iterdir())


def expected_assets():
    names = {"candidate.json", "candidate.json.asc"}
    for platform in PLATFORMS:
        slug = platform.replace("/", "-")
        names.update({slug + ".tar.gz", slug + "-SHA256SUMS", slug + "-SHA256SUMS.asc"})
    return names


def verify_uploaded(github, release, digests):
    assets = github.assets(release)
    if len(assets) != len(digests) or {a["name"] for a in assets} != set(digests):
        raise ValueError("release asset inventory is incomplete")
    for asset in assets:
        if asset["state"] != "uploaded" or asset.get("digest") != digests[asset["name"]]:
            raise ValueError("uploaded release asset digest differs")


def publish(github, branch, source, inventory, files):
    version = candidate_version(branch)
    if inventory["version"] != version or inventory["source_commit"] != source:
        raise ValueError("build does not match candidate event")
    if not re.fullmatch(r"[a-f0-9]{40}", source):
        raise ValueError("invalid source commit")
    if github.api("git/ref/heads/" + branch)["object"]["sha"] != source:
        return "superseded"
    tag = "v" + version
    release = github.release(tag)
    if release and (not release["prerelease"] or release.get("immutable") or MARKER not in (release.get("body") or "")):
        raise ValueError("cannot replace a stable, immutable or unmanaged release")
    digests = {f.name: checksum(f) for f in files}
    if set(digests) != expected_assets():
        raise ValueError("candidate file set is incomplete")
    body = MARKER + "\n" + json.dumps({"version": version, "source_commit": source, "assets": digests}, sort_keys=True)
    # A failed upload remains a draft. Promotion never accepts incomplete files.
    if release:
        github.api(f"releases/{release['id']}", "PATCH", {"draft": True})
        for asset in github.assets(release):
            github.api(f"releases/assets/{asset['id']}", "DELETE")
    ref = github.api("git/ref/tags/" + tag, missing=True)
    if ref and not release and ref["object"]["sha"] != source:
        raise ValueError("refusing to move a tag without a managed candidate release")
    if ref:
        github.api("git/refs/tags/" + tag, "PATCH", {"sha": source, "force": True})
    else:
        github.api("git/refs", "POST", {"ref": "refs/tags/" + tag, "sha": source})
    metadata = {"tag_name": tag, "target_commitish": source, "name": "Operator Sandbox " + version,
                "body": body, "draft": True, "prerelease": True, "make_latest": "false"}
    if release:
        github.api(f"releases/{release['id']}", "PATCH", metadata)
    else:
        release = github.api("releases", "POST", metadata)
    github.upload(tag, files)
    verify_uploaded(github, release, digests)
    if github.api("git/ref/heads/" + branch)["object"]["sha"] != source:
        return "superseded"
    github.api(f"releases/{release['id']}", "PATCH", {"draft": False, "prerelease": True, "make_latest": "false"})
    return "prerelease-published"


def promote(github, number):
    pr = github.api("pulls/" + str(number))
    if not pr["merged"] or pr["base"]["ref"] != "main" or pr["head"]["repo"]["full_name"] != github.repository:
        raise ValueError("promotion requires a merged same-repository candidate PR into main")
    version = candidate_version(pr["head"]["ref"])
    source = pr["head"]["sha"]
    release = github.release("v" + version)
    if not release or release["draft"]:
        raise ValueError("candidate prerelease is missing or incomplete; finish publication and rerun promotion")
    body = release.get("body") or ""
    if not body.startswith(MARKER + "\n"):
        raise ValueError("release is not managed by the candidate workflow")
    record = json.loads(body.removeprefix(MARKER + "\n"))
    if record["source_commit"] != source or record["version"] != version:
        raise ValueError("prerelease was not built from the merged candidate head")
    if set(record["assets"]) != expected_assets():
        raise ValueError("prerelease is missing required artifacts")
    if github.api("commits/v" + version)["sha"] != source:
        raise ValueError("release tag differs from built candidate")
    candidate_tree = github.api("git/commits/" + source)["tree"]["sha"]
    merged_tree = github.api("git/commits/" + pr["merge_commit_sha"])["tree"]["sha"]
    if candidate_tree != merged_tree:
        raise ValueError("main merge changed the candidate tree; merge main into the candidate and rebuild before release")
    verify_uploaded(github, release, record["assets"])
    if not release["prerelease"]:
        return "already-stable"
    github.api(f"releases/{release['id']}", "PATCH", {"prerelease": False, "make_latest": "legacy"})
    return "promoted-existing-assets"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("operation", choices=("context", "publish", "promote"))
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--keyring", type=Path)
    parser.add_argument("--signing-key")
    parser.add_argument("--operatorctl", type=Path, help="native verifier; defaults to the Linux AMD64 CI candidate")
    parser.add_argument("--branch")
    parser.add_argument("--source")
    parser.add_argument("--pr", type=int)
    args = parser.parse_args()
    if args.operation == "context":
        result = event_context(json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text()),
                               os.environ["GITHUB_EVENT_NAME"], os.environ["GITHUB_REF"],
                               os.environ["GITHUB_SHA"], os.environ["GITHUB_REPOSITORY"])
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            for key, value in result.items():
                if "\n" in value or "\r" in value:
                    raise ValueError("invalid workflow output")
                output.write(f"{key}={value}\n")
        return
    github = GitHub(os.environ["GITHUB_REPOSITORY"])
    if args.operation == "promote":
        if not args.pr or args.pr < 1:
            parser.error("--pr is required")
        result = promote(github, args.pr)
    else:
        if not all((args.directory, args.keyring, args.signing_key, args.branch, args.source)):
            parser.error("publish requires directory, keyring, signing-key, branch and source")
        with tempfile.TemporaryDirectory(prefix="operator-publish-") as temporary:
            inventory, files = assemble(args.directory.resolve(), Path(temporary) / "assets", args.keyring.resolve(), args.signing_key, args.operatorctl)
            result = publish(github, args.branch, args.source, inventory, files)
    print(json.dumps({"status": result}))


if __name__ == "__main__":
    main()
