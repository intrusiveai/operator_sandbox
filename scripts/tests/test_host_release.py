import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
spec = importlib.util.spec_from_file_location("builder", ROOT / "scripts/build_host_release.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class ReleaseMetadataTest(unittest.TestCase):
    def test_module_inventory_is_sorted_and_rejects_local_replacements(self):
        modules = builder.module_stream('{"Path":"b","Version":"v1"}\n{"Path":"a","Version":"v2"}')
        self.assertEqual([m["Path"] for m in modules], ["a", "b"])
        with self.assertRaises(ValueError):
            builder.module_stream('{"Path":"a","Replace":{"Path":"/tmp/local"}}')
        packages = builder.module_stream('{"Module":null} {"Module":{"Path":"a","Version":"v2"}} {"Module":{"Path":"a","Version":"v2"}}')
        self.assertEqual(packages, [{"Path": "a", "Version": "v2"}])
        sbom = builder.sbom(modules, "0.1.0", "linux/arm64", "a" * 40)
        self.assertEqual(sbom["spdxVersion"], "SPDX-2.3")
        self.assertEqual(len(sbom["packages"]), 3)

    def test_harness_candidate_pin_must_match_host(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pin = {"package_version": "0.0.0", "package_digest": "sha256:" + "a" * 64}
            (root / "host.json").write_text(json.dumps({"contract": pin}))
            (root / "lock.json").write_text(json.dumps({"package": pin}))
            report = {"api_version": "intrusive.ai/attack-harness-build-report/v1alpha1", "contract": dict(pin)}
            (root / "report.json").write_text(json.dumps(report))
            command = [sys.executable, str(ROOT / "scripts/check_release_pair.py"),
                       "--host", str(root / "host.json"), "--harness-lock", str(root / "lock.json"),
                       "--harness-report", str(root / "report.json")]
            self.assertEqual(subprocess.run(command, capture_output=True).returncode, 0)
            report["contract"]["package_digest"] = "sha256:" + "b" * 64
            (root / "report.json").write_text(json.dumps(report))
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)

class PublicationPreflightTest(unittest.TestCase):
    def test_contract_version_is_required_and_exact(self):
        from publish_host_release import contract_version, check_candidates, PLATFORMS
        for version in (None, 123, "", "v0.2.1", "01.2.1", "0.2", "0.2.1-rc1", "0.2.1+build", "0.2.1\n", "^0.2.1", "1" * 125 + ".0.0"):
            with self.subTest(version=version):
                with self.assertRaises(ValueError):
                    contract_version({"package_version": version})
        self.assertEqual(contract_version({"package_version": "0.2.1"}), "0.2.1")
        boundary = "1" * 124 + ".0.0"
        self.assertEqual(contract_version({"package_version": boundary}), boundary)
        for contract in (None, {}, {"package_version": ""}):
            hosts = [{"platform": p, "version": "1.2.3", "source_commit": "a" * 40, "contract": contract} for p in PLATFORMS]
            with self.assertRaises(ValueError):
                check_candidates(hosts)

    def test_public_asset_names_identify_product_version_and_platform(self):
        from publish_host_release import expected_assets, asset_stem
        stems = {
            "operator_sandbox-v0.1.3-linux_amd64", "operator_sandbox-v0.1.3-linux_arm64",
            "operator_sandbox-v0.1.3-darwin_amd64", "operator_sandbox-v0.1.3-darwin_arm64",
        }
        self.assertEqual(expected_assets("0.1.3"), {"candidate.json"} |
                         {s + suffix for s in stems for suffix in (".tar.gz", "-SHA256SUMS")})
        with self.assertRaises(ValueError):
            asset_stem("0.1.3", "windows/amd64")
        for version in (None, "v0.1.3", "01.1.3", "0.1", "0.1.3-rc1", "0.1.3/other", "0.1.3\n"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                asset_stem(version, "linux/amd64")
        self.assertEqual(expected_assets("0.1.3") & expected_assets("0.1.4"), {"candidate.json"})

    def test_requires_all_platforms_matching_source_and_contract(self):
        from publish_host_release import PLATFORMS, check_candidates
        pin = {"package_version": "0.0.0", "package_digest": "sha256:" + "a" * 64}
        hosts = [{"platform": p, "version": "0.1.0", "source_commit": "a" * 40, "contract": dict(pin)}
                 for p in PLATFORMS]
        self.assertEqual(check_candidates(hosts), "0.1.0")
        with self.assertRaises(ValueError):
            check_candidates(hosts[:3])
        hosts[0]["source_commit"] = "b" * 40
        with self.assertRaises(ValueError):
            check_candidates(hosts)
        hosts[0]["source_commit"] = "a" * 40
        hosts[0]["contract"]["package_version"] = "0.2.2"
        with self.assertRaises(ValueError):
            check_candidates(hosts)
        hosts[0]["contract"]["package_version"] = "0.0.0"
        hosts[0]["contract"]["package_digest"] = "sha256:" + "b" * 64
        with self.assertRaises(ValueError):
            check_candidates(hosts)


if __name__ == "__main__":
    unittest.main()
