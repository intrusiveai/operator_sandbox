import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("builder", ROOT / "scripts/build_host_release.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class ReleaseMetadataTest(unittest.TestCase):
    def test_module_inventory_is_sorted_and_rejects_local_replacements(self):
        modules = builder.module_stream('{"Path":"b","Version":"v1"}\n{"Path":"a","Version":"v2"}')
        self.assertEqual([m["Path"] for m in modules], ["a", "b"])
        with self.assertRaises(ValueError):
            builder.module_stream('{"Path":"a","Replace":{"Path":"/tmp/local"}}')
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
    def test_requires_all_platforms_matching_contracts_and_compatible_images(self):
        spec = importlib.util.spec_from_file_location("publisher", ROOT / "scripts/publish_host_release.py")
        publisher = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(publisher)
        pin = {"package_version": "0.0.0", "package_digest": "sha256:" + "a" * 64}
        hosts = [{"platform": p, "version": "0.1.0", "source_commit": "a" * 40, "contract": pin}
                 for p in publisher.PLATFORMS]
        images = []
        for platform in ("linux/amd64", "linux/arm64"):
            image = "sha256:" + "b" * 64
            images.append({"api_version": "intrusive.ai/attack-harness-build-report/v1alpha1",
                           "platform": platform, "contract": dict(pin), "oci": {"image_digest": image},
                           "compatibility_candidate": {"image_digest": image, "platform": platform,
                               "runtime_profile": "operator-container/v1", "minimum_operator_version": "0.1.0",
                               "contract_package_version": pin["package_version"],
                               "contract_package_digest": pin["package_digest"]}})
        self.assertEqual(publisher.check_candidates(hosts, images), "0.1.0")
        with self.assertRaises(ValueError):
            publisher.check_candidates(hosts[:3], images)
        with self.assertRaises(ValueError):
            publisher.check_candidates(hosts, images[:1])
        images[0]["compatibility_candidate"]["minimum_operator_version"] = "0.2.0"
        with self.assertRaises(ValueError):
            publisher.check_candidates(hosts, images)


if __name__ == "__main__":
    unittest.main()
