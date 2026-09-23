import base64
import json
from pathlib import Path
import unittest

from operator_contracts import Protocol, ContractError

ROOT = Path(__file__).resolve().parents[3]


def encode(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


class StartupTest(unittest.TestCase):
    def setUp(self):
        self.protocol = Protocol(ROOT / "schemas")
        self.cases = json.loads((ROOT / "schemas/fixtures/startup-manifests.json").read_text())

    def test_shared_fixtures(self):
        p = self.protocol
        for case in self.cases:
            with self.subTest(case=case["name"]):
                def check():
                    mode = case["mode"]
                    if mode == "control":
                        return p.validate_control(case["direction"], encode(case["document"]))
                    if mode == "startup":
                        return p.validate_startup([encode(m) for m in case["messages"]])
                    if mode in ("input_tree", "skill_manifest", "skill_set"):
                        return {"input_tree": p.validate_input_tree, "skill_manifest": p.validate_skill_manifest,
                                "skill_set": p.validate_skill_set}[mode](encode(case["document"]))
                    tree = base64.b64decode(case["tree_base64"], validate=True)
                    skill_set = base64.b64decode(case["skill_set_base64"], validate=True)
                    skills = [base64.b64decode(s, validate=True) for s in case["skills_base64"]]
                    if mode == "manifest_set":
                        return p.validate_manifest_set(tree, skill_set, skills)
                    if mode == "startup_inputs":
                        return p.validate_startup_inputs([encode(m) for m in case["messages"]], tree, skill_set, skills)
                    self.fail("unknown fixture mode")
                if case["valid"]:
                    check()
                else:
                    with self.assertRaises(ContractError):
                        check()

    def test_encoded_limits(self):
        p = self.protocol
        validators = {
            "control bootstrap": (65536, lambda raw: p.validate_control("host", raw)),
            "complete input inventory": (8 << 20, p.validate_input_tree),
            "complete skill inventory": (2 << 20, p.validate_skill_manifest),
            "selected skill set": (65536, p.validate_skill_set),
        }
        for case in self.cases:
            if case["name"] not in validators:
                continue
            with self.subTest(case=case["name"]):
                maximum, validate = validators[case["name"]]
                raw = encode(case["document"]).ljust(maximum)
                validate(raw)
                with self.assertRaises(ContractError):
                    validate(raw + b" ")


if __name__ == "__main__":
    unittest.main()
