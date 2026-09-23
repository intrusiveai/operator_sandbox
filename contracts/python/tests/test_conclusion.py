import base64
import json
from pathlib import Path
import unittest

from operator_contracts import Protocol, ContractError

ROOT = Path(__file__).resolve().parents[3]


def encode(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


class ConclusionTest(unittest.TestCase):
    def test_shared_conclusion_fixtures(self):
        protocol = Protocol(ROOT / "schemas")
        cases = json.loads((ROOT / "schemas/fixtures/conclusion-contract.json").read_text())
        for case in cases:
            with self.subTest(case=case["name"]):
                raw = base64.b64decode(case["conclusion_base64"], validate=True)
                def check():
                    if "completion" in case:
                        return protocol.validate_completion(conclusion=raw, **{
                            key: encode(value) for key, value in case["completion"].items()
                        })
                    return protocol.validate_conclusion(raw)
                if case["valid"]:
                    check()
                else:
                    with self.assertRaises(ContractError):
                        check()

    def test_byte_limits(self):
        protocol = Protocol(ROOT / "schemas")
        conclusion = (ROOT / "schemas/fixtures/conclusion-example.json").read_bytes().ljust(1 << 20)
        protocol.validate_conclusion(conclusion)
        with self.assertRaises(ContractError):
            protocol.validate_conclusion(conclusion + b" ")
        cases = json.loads((ROOT / "schemas/fixtures/ordinary-protocol.json").read_text())
        case = next(c for c in cases if c["name"] == "record hypothesis response")
        request, response = encode(case["request"]).ljust(65536), encode(case["response"]).ljust(65536)
        protocol.validate_response(request, response)
        with self.assertRaises(ContractError):
            protocol.validate_request(request + b" ")
        with self.assertRaises(ContractError):
            protocol.validate_response(request, response + b" ")


if __name__ == "__main__":
    unittest.main()
