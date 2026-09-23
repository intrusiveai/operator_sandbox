import base64
import json
from pathlib import Path
import unittest

from operator_contracts import Protocol, ContractError, canonicalize, canonical_digest

ROOT = Path(__file__).resolve().parents[3]
encode = lambda value: json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode()


class CanonicalTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol = Protocol(ROOT / 'schemas')

    def test_shared_canonicalization(self):
        cases = json.loads((ROOT / 'schemas/fixtures/canonicalization.json').read_text())
        for case in cases:
            with self.subTest(case=case['name']):
                raw = base64.b64decode(case['raw_base64'], validate=True)
                if not case['valid']:
                    with self.assertRaises(ContractError):
                        canonicalize(raw, case['maximum'])
                    with self.assertRaises(ContractError):
                        canonical_digest(raw, case['maximum'])
                    continue
                output = canonicalize(raw, case['maximum'])
                self.assertEqual(output, base64.b64decode(case['canonical_base64'], validate=True))
                self.assertEqual(canonical_digest(raw, case['maximum']), case['digest'])
                self.assertEqual(canonicalize(output, case['maximum']), output)

    def test_shared_identities(self):
        cases = json.loads((ROOT / 'schemas/fixtures/identity-validation.json').read_text())
        for case in cases:
            with self.subTest(case=case['name']):
                decode = lambda key: base64.b64decode(case[key], validate=True)
                if case['mode'] == 'launch':
                    args = ([encode(m) for m in case['messages']], decode('tree_base64'), decode('skill_set_base64'),
                            [base64.b64decode(s, validate=True) for s in case['skills_base64']],
                            decode('context_base64'), decode('bundle_base64'), decode('prompt_base64'))
                    # Ensure corrupted canonical pins are invisible to the prior byte/shape checks.
                    self.protocol.validate_launch_content(*args)
                    run = lambda: self.protocol.validate_launch_identities(*args)
                elif case['mode'] == 'artifact':
                    run = lambda: self.protocol.validate_artifact_content(encode(case['request']), decode('content_base64'))
                else:
                    self.fail('unknown fixture mode')
                if case['valid']:
                    run()
                else:
                    with self.assertRaises(ContractError):
                        run()

    def test_registry_digest_isolation(self):
        pins = self.protocol.registry_digests()
        pins['catalog_digest'] = 'changed'
        self.assertNotEqual(self.protocol.registry_digests()['catalog_digest'], 'changed')


if __name__ == '__main__':
    unittest.main()
