import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
spec = importlib.util.spec_from_file_location("builder", ROOT / "scripts/build_host_release.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class UnattendedSigningTest(unittest.TestCase):
    def test_encrypted_key_signs_manifest_and_checksum_without_prompt(self):
        spec = importlib.util.spec_from_file_location("signer", ROOT / "scripts/sign_host_release.py")
        signer = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(signer)
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary)
            home.chmod(0o700)
            with patch.dict(os.environ, {"GNUPGHOME": str(home), "TEST_RELEASE_PASSPHRASE": "fixture-only"}):
                try:
                    subprocess.run(["gpg", "--batch", "--pinentry-mode", "loopback",
                                    "--passphrase-fd", "0", "--quick-generate-key",
                                    "Release CI Fixture <fixture@example.invalid>", "ed25519", "sign", "0"],
                                   input="fixture-only\n", text=True, check=True, capture_output=True)
                    listing = subprocess.check_output(["gpg", "--batch", "--with-colons", "--list-keys"], text=True)
                    fingerprint = next(line.split(":")[9] for line in listing.splitlines() if line.startswith("fpr:"))
                    subprocess.run(["gpg", "--batch", "--output", str(home / "trusted.gpg"), "--export", fingerprint], check=True)
                    for name, armor in (("release.json", False), ("SHA256SUMS", True)):
                        source = home / name
                        source.write_text("fixture payload\n")
                        signature = home / (name + ".sig")
                        signer.sign(source, signature, fingerprint, "TEST_RELEASE_PASSPHRASE", armor=armor)
                        subprocess.run(["gpgv", "--keyring", str(home / "trusted.gpg"),
                                        str(signature), str(source)], check=True, capture_output=True)
                    with self.assertRaises(ValueError):
                        signer.sign(source, home / "missing.sig", fingerprint, "ABSENT_SIGNING_VARIABLE")
                finally:
                    subprocess.run(["gpgconf", "--homedir", str(home), "--kill", "gpg-agent"], check=False)


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
        hosts[0]["contract"]["package_digest"] = "sha256:" + "b" * 64
        with self.assertRaises(ValueError):
            check_candidates(hosts)


if __name__ == "__main__":
    unittest.main()
