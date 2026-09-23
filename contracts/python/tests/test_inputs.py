import base64
import json
from pathlib import Path
import unittest

from operator_contracts import Protocol, ContractError
from operator_contracts.inputs import compose_prompt, validate_prompt, descriptor, PROMPT_LIMIT

ROOT = Path(__file__).resolve().parents[3]
encode = lambda value: json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode()


class InputsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol = Protocol(ROOT / 'schemas')
        cls.cases = json.loads((ROOT / 'schemas/fixtures/engine-inputs.json').read_text())

    def test_shared_fixtures(self):
        for case in self.cases:
            with self.subTest(case=case['name']):
                def run():
                    p, mode = self.protocol, case['mode']
                    decode = lambda key: base64.b64decode(case[key], validate=True)
                    if mode == 'context':
                        return p.validate_engine_context(encode(case['document']))
                    if mode == 'provenance':
                        return p.validate_prompt_provenance(encode(case['document']))
                    if mode == 'compose':
                        output, provenance = compose_prompt(case['prompt_mode'], decode('base_base64'),
                            decode('replacement_base64') if 'replacement_base64' in case else None,
                            [base64.b64decode(x, validate=True) for x in case['appends_base64']])
                        self.assertEqual(output, decode('effective_base64'))
                        p.validate_prompt_provenance(encode(provenance))
                        self.assertEqual(provenance['effective'], descriptor(output))
                        return
                    if mode == 'launch':
                        return p.validate_launch_content([encode(m) for m in case['messages']],
                            decode('tree_base64'), decode('skill_set_base64'),
                            [base64.b64decode(s, validate=True) for s in case['skills_base64']],
                            decode('context_base64'), decode('bundle_base64'), decode('prompt_base64'))
                    self.fail('unknown fixture mode')
                if case['valid']:
                    run()
                else:
                    with self.assertRaises(ContractError):
                        run()

    def test_prompt_byte_limits(self):
        for mode, base, replacement, appends in [
            ('default', b'x' * PROMPT_LIMIT, None, []),
            ('replacement', b'base', b'x' * PROMPT_LIMIT, []),
            ('extension', b'x' * (PROMPT_LIMIT - 3), None, [b'y']),
        ]:
            with self.subTest(mode=mode):
                output, _ = compose_prompt(mode, base, replacement, appends)
                self.assertEqual(len(output), PROMPT_LIMIT)
                if mode == 'replacement': replacement += b'x'
                else: base += b'x'
                with self.assertRaises(ContractError):
                    compose_prompt(mode, base, replacement, appends)
        validate_prompt(('é' * (PROMPT_LIMIT // 2)).encode())
        with self.assertRaises(ContractError):
            validate_prompt(('é' * (PROMPT_LIMIT // 2) + 'x').encode())


if __name__ == '__main__':
    unittest.main()
