from copy import deepcopy
import json
from pathlib import Path
import shutil
import tempfile
import unittest

from operator_contracts import Protocol, ContractError

ROOT = Path(__file__).resolve().parents[3]


def encode(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


class ProtocolTest(unittest.TestCase):
    def test_shared_protocol_fixtures(self):
        protocol = Protocol(ROOT / "schemas")
        cases = json.loads((ROOT / "schemas/fixtures/ordinary-protocol.json").read_text())
        covered = set()
        for case in cases:
            with self.subTest(case=case["name"]):
                def check():
                    if "ack" in case:
                        return protocol.validate_ack(encode(case["ack"]))
                    request = encode(case["request"])
                    if "response" in case:
                        return protocol.validate_response(request, encode(case["response"]))
                    return protocol.validate_request(request)
                if case["valid"]:
                    check()
                    if "response" in case:
                        covered.add(case["request"]["operation"])
                else:
                    with self.assertRaises(ContractError):
                        check()
        self.assertEqual(covered, {item["name"] for item in protocol.operations()})

    def test_registry_and_ack_guards(self):
        protocol = Protocol(ROOT / "schemas")
        ops = protocol.operations()
        ops[0]["error_codes"][0] = "MUTATED"
        self.assertNotEqual(protocol.operations()[0]["error_codes"][0], "MUTATED")
        ack = encode({"api_version": "operator.dev/engine-spool-ack/v1alpha1", "launch_id": "launch-1", "ordinary_seq": None, "control_seq": 0})
        ack = ack.ljust(1024)
        protocol.validate_ack(ack)
        with self.assertRaises(ContractError):
            protocol.validate_ack(ack + b" ")
        original = json.loads((ROOT / "schemas/operations.json").read_text())
        for mutation in ("duplicate", "missing-schema", "unknown-field", "unsorted", "bad-finalization-rule", "integer-exponent"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temp:
                directory = Path(temp)
                for source in (ROOT / "schemas").glob("*.json"):
                    shutil.copyfile(source, directory / source.name)
                registry = deepcopy(original)
                items = registry["operations"]
                if mutation == "duplicate": items.append(items[0])
                elif mutation == "missing-schema": items[0]["request_schema"] = "urn:missing"
                elif mutation == "unknown-field": items[0]["guest_override"] = True
                elif mutation == "unsorted": items[0], items[1] = items[1], items[0]
                elif mutation == "bad-finalization-rule": items[0]["finalization_rule"] = "anything"
                (directory / "operations.json").write_bytes(encode(registry))
                if mutation == "integer-exponent":
                    raw = (directory / "operations.json").read_bytes().replace(b'"timeout_ms":30000,', b'"timeout_ms":3e4,')
                    (directory / "operations.json").write_bytes(raw)
                    self.assertEqual(Protocol(directory).operations()[0]["timeout_ms"], 30000)
                else:
                    with self.assertRaises(ContractError):
                        Protocol(directory)


if __name__ == "__main__":
    unittest.main()
