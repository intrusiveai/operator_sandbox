import base64
import json
from pathlib import Path
import tempfile
import unittest
from operator_contracts import Catalog, ContractError, decode

ROOT = Path(__file__).resolve().parents[3]

class ContractsTest(unittest.TestCase):
    def test_shared_fixtures(self):
        catalog = Catalog(ROOT / "schemas")
        cases = json.loads((ROOT / "schemas/fixtures/validation-foundation.json").read_text())
        for case in cases:
            with self.subTest(case=case["name"]):
                raw = base64.b64decode(case["raw_base64"], validate=True)
                def check():
                    if case["schema"] is None:
                        return decode(raw, case["maximum_bytes"])
                    return catalog.validate(case["schema"], raw, case["maximum_bytes"])
                if case["valid"]:
                    check()
                else:
                    with self.assertRaises(ContractError): check()
        self.assertEqual(catalog.ids(), sorted(json.loads((ROOT / "schemas/catalog.json").read_text())))

    def test_catalog_is_offline(self):
        for ref in ["https://example.invalid/forbidden.json", "file:///etc/passwd", "urn:missing"]:
            with self.subTest(ref=ref), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                (root / "catalog.json").write_text(json.dumps({"urn:test": "test.schema.json"}))
                (root / "test.schema.json").write_text(json.dumps({"$schema":"https://json-schema.org/draft/2020-12/schema", "$id":"urn:test", "anyOf":[{"type":"null"},{"$ref":ref}]}))
                with self.assertRaises(ContractError): Catalog(root)

    def test_invalid_catalog_and_unknown_schema(self):
        for data in ["{}", '{"urn:test":"../outside.schema.json"}', '{"urn:test":"x.schema.json","urn:test":"y.schema.json"}']:
            with tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                (root / "catalog.json").write_text(data)
                with self.assertRaises(ContractError): Catalog(root)
        with self.assertRaises(ContractError):
            Catalog(ROOT / "schemas").validate("https://example.invalid/unknown", b"{}")

if __name__ == "__main__": unittest.main()
