#!/usr/bin/env python3
"""Publish a rolling candidate, or promote its existing GitHub assets."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import uuid

PLATFORMS = {"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}
VERSION = r"(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})"
MARKER = "<!-- operator-candidate-release/v1 -->"
CATALOG_ASSET = "release-index-entry.json"
INVENTORY_ASSET = "candidate.json"
INVENTORY_LIMIT = 64 * 1024


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
    contract_version(identity[0][2])
    return identity[0][0]


def contract_version(contract):
    version = contract.get("package_version") if isinstance(contract, dict) else None
    if (not isinstance(version, str) or len(version) > 128 or
            not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version)):
        raise ValueError("build contract.package_version must be an exact major.minor.patch version")
    return version


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

    def upload(self, tag, files, replace=False):
        subprocess.run(["gh", "release", "upload", tag, *map(str, files),
                        "--repo", self.repository] + (["--clobber"] if replace else []), check=True)

    def download_catalog(self, tag):
        with tempfile.TemporaryDirectory(prefix="operator-catalog-") as directory:
            subprocess.run(["gh", "release", "download", tag, "--repo", self.repository,
                            "--pattern", CATALOG_ASSET, "--dir", directory], check=True)
            return json.loads((Path(directory) / CATALOG_ASSET).read_text())

    def download_inventory(self, tag):
        with tempfile.TemporaryDirectory(prefix="operator-inventory-") as directory:
            subprocess.run(["gh", "release", "download", tag, "--repo", self.repository,
                            "--pattern", INVENTORY_ASSET, "--dir", directory], check=True)
            with (Path(directory) / INVENTORY_ASSET).open("rb") as stream:
                return stream.read(INVENTORY_LIMIT + 1)

    def release(self, tag):
        return self.api("releases/tags/" + tag, missing=True)

    def assets(self, release):
        return self.api(f"releases/{release['id']}/assets?per_page=100")


def checksum(path):
    with path.open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def asset_stem(version, platform):
    if not isinstance(version, str) or not re.fullmatch(VERSION, version):
        raise ValueError("asset version must be numeric major.minor.patch")
    if platform not in PLATFORMS:
        raise ValueError("unsupported host platform")
    return "operator_sandbox-v" + version + "-" + platform.replace("/", "_")


def assemble(directory, output, operatorctl=None):
    """Reuse the existing manifest checker and archive builder."""
    directories = [directory / p.replace("/", "-") for p in sorted(PLATFORMS)]
    hosts = [json.loads((d / "release.json").read_text()) for d in directories]
    version = check_candidates(hosts)
    if any(h["platform"].replace("/", "-") != d.name for h, d in zip(hosts, directories)):
        raise ValueError("candidate platform differs from its directory")
    operator = str((operatorctl or directory / "linux-amd64/bin/operatorctl").resolve())
    output.mkdir(mode=0o700)
    for host, d in zip(hosts, directories):
        stem = asset_stem(version, host["platform"])
        archive = output / (stem + ".tar.gz")
        subprocess.run([operator, "release", "archive", "--directory", str(d),
                        "--output", str(archive)], check=True)
        (output / (stem + "-SHA256SUMS")).write_text(
            checksum(archive).removeprefix("sha256:") + "  " + archive.name + "\n")
    files = sorted(output.iterdir())
    inventory = {"version": version, "source_commit": hosts[0]["source_commit"],
                 "contract": hosts[0]["contract"], "assets": {f.name: checksum(f) for f in files}}
    record = output / "candidate.json"
    record.write_text(json.dumps(inventory, sort_keys=True, indent=2) + "\n")
    return inventory, sorted(output.iterdir())


def expected_assets(version):
    names = {"candidate.json"}
    for platform in PLATFORMS:
        slug = asset_stem(version, platform)
        names.update({slug + ".tar.gz", slug + "-SHA256SUMS"})
    return names


def verify_uploaded(github, release, digests):
    # The catalog is mutable publication metadata, outside the package set.
    assets = [a for a in github.assets(release) if a["name"] != CATALOG_ASSET]
    if len(assets) != len(digests) or {a["name"] for a in assets} != set(digests):
        raise ValueError("release asset inventory is incomplete")
    for asset in assets:
        if asset["state"] != "uploaded" or asset.get("digest") != digests[asset["name"]]:
            raise ValueError("uploaded release asset digest differs")


def verify_inventory(raw, record):
    """Bind the verified build's contract to precisely the published archive bytes."""
    if len(raw) > INVENTORY_LIMIT or "sha256:" + hashlib.sha256(raw).hexdigest() != record["assets"][INVENTORY_ASSET]:
        raise ValueError("candidate inventory exceeds its bound or differs from its published digest")
    inventory = json.loads(raw)
    if (not isinstance(inventory, dict) or inventory.get("version") != record["version"] or
            inventory.get("source_commit") != record["source_commit"] or
            inventory.get("assets") != {k: v for k, v in record["assets"].items() if k != INVENTORY_ASSET}):
        raise ValueError("candidate inventory does not describe the published source and artifacts")
    contract_version(inventory.get("contract"))
    return inventory


def catalog_entry(github, tag, source):
    if github.repository != "intrusiveai/operator_sandbox":
        raise ValueError("catalog publication is restricted to Operator's repository")
    version = candidate_version("candidate-" + tag)
    release = github.release(tag)
    if not release or release["draft"] or not (release.get("body") or "").startswith(MARKER + "\n"):
        raise ValueError("catalog requires a complete managed GitHub release")
    record = json.loads(release["body"].removeprefix(MARKER + "\n"))
    if record["version"] != version or record["source_commit"] != source or github.api("commits/" + tag)["sha"] != source:
        raise ValueError("catalog source/tag differs from the published candidate")
    if set(record["assets"]) != expected_assets(version):
        raise ValueError("catalog requires all package assets")
    verify_uploaded(github, release, record["assets"])
    inventory = verify_inventory(github.download_inventory(tag), record)
    if not release["prerelease"]:
        number = record.get("promotion_pr")
        if type(number) is not int or number < 1:
            raise ValueError("stable catalog requires verified candidate PR promotion; rerun the promotion job")
        pr = github.api("pulls/" + str(number))
        if pr["head"]["ref"] != "candidate-" + tag or pr["head"]["sha"] != source:
            raise ValueError("stable catalog promotion PR differs from the release")
        promote(github, number)
    assets = {a["name"]: a for a in github.assets(release)}
    artifacts = []
    for platform in sorted(PLATFORMS):
        name = asset_stem(version, platform) + ".tar.gz"
        asset = assets[name]
        url = f"https://github.com/{github.repository}/releases/download/{tag}/{name}"
        if asset.get("browser_download_url") != url:
            raise ValueError("catalog download URL differs from the product release")
        artifacts.append({"platform": platform, "filename": name, "url": asset["browser_download_url"],
                          "sha256": asset["digest"].removeprefix("sha256:")})
    return {"version": tag, "latest": True, "tags": ["rc" if release["prerelease"] else "release"],
            "contract_package_version": contract_version(inventory["contract"]), "artifacts": artifacts}


def catalog_context(github, tag, source):
    expected = catalog_entry(github, tag, source)
    if github.download_catalog(tag) != expected:
        raise ValueError("catalog asset is stale or differs from the actual release")
    return expected


def sync_catalog(github, tag, source, timeout=1800):
    entry = catalog_entry(github, tag, source)
    with tempfile.TemporaryDirectory(prefix="operator-catalog-") as directory:
        path = Path(directory) / CATALOG_ASSET
        path.write_text(json.dumps(entry, sort_keys=True, indent=2) + "\n")
        github.upload(tag, [path], replace=True)
    catalog_context(github, tag, source)
    request = "operator-catalog-" + uuid.uuid4().hex
    workflow = "actions/workflows/publish-catalog.yml"
    github.api(workflow + "/dispatches", "POST", {"ref": tag, "inputs": {"request_id": request}})
    deadline = time.monotonic() + timeout
    run = None
    while time.monotonic() < deadline:
        if run is None:
            runs = github.api(workflow + "/runs?event=workflow_dispatch&head_sha=" + source + "&per_page=100")["workflow_runs"]
            matches = [r for r in runs if r["display_title"] == request and r["head_sha"] == source]
            if len(matches) > 1:
                raise RuntimeError("ambiguous catalog workflow dispatch")
            run = matches[0] if matches else None
        else:
            run = github.api(f"actions/runs/{run['id']}")
        if run and run["status"] == "completed":
            if run["conclusion"] != "success":
                raise RuntimeError("catalog publication failed: " + run["html_url"])
            return run["html_url"]
        time.sleep(10)
    # Do not leave a known publisher running after releasing the parent job's lock.
    if run:
        github.api(f"actions/runs/{run['id']}/cancel", "POST")
    raise RuntimeError("catalog publication timed out; inspect/cancel the dispatched run before retrying")


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
    if set(digests) != expected_assets(version):
        raise ValueError("candidate file set is incomplete")
    record = {"version": version, "source_commit": source, "assets": digests}
    inventory_file = next(f for f in files if f.name == INVENTORY_ASSET)
    with inventory_file.open("rb") as stream:
        saved = verify_inventory(stream.read(INVENTORY_LIMIT + 1), record)
    if saved != inventory:
        raise ValueError("candidate inventory differs from the verified build")
    body = MARKER + "\n" + json.dumps(record, sort_keys=True)
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
    if set(record["assets"]) != expected_assets(version):
        raise ValueError("prerelease is missing required artifacts")
    if github.api("commits/v" + version)["sha"] != source:
        raise ValueError("release tag differs from built candidate")
    candidate_tree = github.api("git/commits/" + source)["tree"]["sha"]
    merged_tree = github.api("git/commits/" + pr["merge_commit_sha"])["tree"]["sha"]
    if candidate_tree != merged_tree:
        raise ValueError("main merge changed the candidate tree; merge main into the candidate and rebuild before release")
    verify_uploaded(github, release, record["assets"])
    verify_inventory(github.download_inventory("v" + version), record)
    promoted_record = dict(record, promotion_pr=number)
    body = MARKER + "\n" + json.dumps(promoted_record, sort_keys=True)
    if not release["prerelease"]:
        if record != promoted_record:
            github.api(f"releases/{release['id']}", "PATCH", {"body": body})
        return "already-stable"
    github.api(f"releases/{release['id']}", "PATCH", {"prerelease": False, "make_latest": "legacy", "body": body})
    return "promoted-existing-assets"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("operation", choices=("context", "publish", "promote", "catalog-sync", "catalog-context"))
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--operatorctl", type=Path, help="native packager; defaults to the Linux AMD64 CI candidate")
    parser.add_argument("--branch")
    parser.add_argument("--source")
    parser.add_argument("--pr", type=int)
    parser.add_argument("--tag")
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
    if args.operation in ("catalog-sync", "catalog-context"):
        if not args.tag or not args.source:
            parser.error("catalog operations require --tag and --source")
        if args.operation == "catalog-context":
            result = catalog_context(github, args.tag, args.source)
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                output.write("json=" + json.dumps(result, separators=(",", ":")) + "\n")
        else:
            result = sync_catalog(github, args.tag, args.source)
    elif args.operation == "promote":
        if not args.pr or args.pr < 1:
            parser.error("--pr is required")
        result = promote(github, args.pr)
    else:
        if not all((args.directory, args.branch, args.source)):
            parser.error("publish requires directory, branch and source")
        with tempfile.TemporaryDirectory(prefix="operator-publish-") as temporary:
            inventory, files = assemble(args.directory.resolve(), Path(temporary) / "assets", args.operatorctl)
            result = publish(github, args.branch, args.source, inventory, files)
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                output.write("published=" + str(result == "prerelease-published").lower() + "\n")
    print(json.dumps({"status": result}))


if __name__ == "__main__":
    main()
