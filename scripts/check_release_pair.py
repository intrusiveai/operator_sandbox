#!/usr/bin/env python3
"""Fail closed unless host and harness candidate reports name identical contracts."""
import argparse
import json
from pathlib import Path


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--host", type=Path, required=True)
    p.add_argument("--harness-lock", type=Path, required=True)
    p.add_argument("--harness-report", type=Path, action="append", default=[])
    a = p.parse_args()
    host = json.loads(a.host.read_text())
    expected = host["contract"]
    locked = json.loads(a.harness_lock.read_text())["package"]
    for key in ("package_version", "package_digest"):
        if expected[key] != locked[key]:
            raise SystemExit("host/harness contract pin mismatch")
    for report_file in a.harness_report:
        report = json.loads(report_file.read_text())
        # Report shape is defined by Attack Harness build/inspect_oci.py.
        if report.get("api_version") != "intrusive.ai/attack-harness-build-report/v1alpha1" or any(report.get("contract", {}).get(k) != v for k, v in expected.items()):
            raise SystemExit("harness build report lacks the exact matching contract pin")
    print(json.dumps({"status": "matching-contract-candidates", "contract": expected,
                      "qualified": False, "harness_reports": len(a.harness_report)}))


if __name__ == "__main__":
    main()
