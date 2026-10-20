"""Carry Operator's actual release entries through the committed catalog publisher."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch

import test_candidate_workflow as candidate_fixture
from test_candidate_workflow import FIRST, SECOND, pull
from test_release_catalog import CatalogGitHub
import publish_host_release as release

ROOT = Path(__file__).resolve().parents[2]
PUBLISHER = ROOT / "tools/release-publisher"
spec = importlib.util.spec_from_file_location(
    "local_publisher_fixture", PUBLISHER / "scripts/tests/test_catalog.py")
publisher_fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher_fixture)


class LocalCatalogIntegrationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # The production job also disables private module and toolchain downloads.
        cls.env = patch.dict(os.environ, GOWORK="off", GOPROXY="off", GOTOOLCHAIN="local")
        cls.env.start()
        cls.addClassCleanup(cls.env.stop)
        publisher_fixture.OperatorCatalogTests.setUpClass()
        cls.addClassCleanup(publisher_fixture.OperatorCatalogTests.tearDownClass)

    def test_rolling_candidate_promotion_and_retry_preserve_package_assets(self):
        publisher = publisher_fixture.OperatorCatalogTests()
        self.addCleanup(publisher.doCleanups)
        publisher.setUp()

        class LocalCatalogGitHub(CatalogGitHub):
            def api(self, path, method="GET", data=None, missing=False):
                result = super().api(path, method, data, missing)
                if path.endswith("publish-catalog.yml/dispatches"):
                    # Execute the real Go merger and shell publication script;
                    # AWS storage and public delivery are local test services.
                    publisher.publish(self.public_catalog)
                return result

        fixture = candidate_fixture.CandidateWorkflowTest()
        self.addCleanup(fixture.doCleanups)
        fixture.setUp()
        github = LocalCatalogGitHub()
        fixture.github = github
        publisher.publish(publisher.entry("v0.9.0", tag="release"))

        fixture.publish(FIRST)
        release.sync_catalog(github, "v1.2.3", FIRST)
        first = json.loads(publisher.get(publisher.key))["releases"][0]
        self.assertEqual(first["tags"], ["rc"])
        self.assertEqual(first["contract_package_version"], "0.2.1")
        self.assertEqual(len(first["artifacts"]), 4)

        github.head = SECOND
        fixture.publish(SECOND, contract_version="0.2.2")
        release.sync_catalog(github, "v1.2.3", SECOND)
        candidate = json.loads(publisher.get(publisher.key))["releases"][0]
        self.assertNotEqual(candidate["artifacts"], first["artifacts"])
        self.assertEqual(candidate["contract_package_version"], "0.2.2")
        package_assets = {k: v for k, v in github.uploaded.items() if k != release.CATALOG_ASSET}

        github.pr = pull(SECOND)
        release.promote(github, 42)
        release.sync_catalog(github, "v1.2.3", SECOND)
        promoted = publisher.get(publisher.key)
        entries = json.loads(promoted)["releases"]
        self.assertEqual([e["version"] for e in entries], ["v1.2.3", "v0.9.0"])
        self.assertEqual(entries[0]["tags"], ["release"])
        self.assertEqual(entries[0]["artifacts"], candidate["artifacts"])
        self.assertEqual(entries[0]["contract_package_version"], "0.2.2")
        self.assertEqual(github.tag, SECOND)
        self.assertEqual({k: v for k, v in github.uploaded.items() if k != release.CATALOG_ASSET}, package_assets)

        release.sync_catalog(github, "v1.2.3", SECOND)
        self.assertEqual(publisher.get(publisher.key), promoted)
        publisher.publish(candidate, success=False)
        publisher.publish(dict(first, tags=["release"]), success=False)
        publisher.publish(dict(candidate, tags=["release"], contract_package_version="0.2.1"), success=False)
        self.assertEqual(publisher.get(publisher.key), promoted)

    def test_export_matches_reviewed_provenance(self):
        manifest = json.loads((PUBLISHER / "publisher-source.json").read_text())
        self.assertEqual(manifest["product"], "operator_sandbox")
        self.assertFalse(manifest["working_tree"])
        self.assertEqual(manifest["source_revision"], "0a88c3564b5266581dc94d3f1ce71880cac2376a")
        for path, expected in manifest["sha256"].items():
            with self.subTest(path=path):
                self.assertEqual(hashlib.sha256((PUBLISHER / path).read_bytes()).hexdigest(), expected)


if __name__ == "__main__":
    unittest.main()
