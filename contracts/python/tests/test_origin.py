import json
from pathlib import Path
import unittest
from operator_contracts.simple import validate_attempt_origin
from operator_contracts.validation import ContractError

class OriginTests(unittest.TestCase):
    def test_shared_origin_cases(self):
        fixtures = json.loads((Path(__file__).resolve().parents[3]/'schemas/fixtures/attempt-origin.json').read_text())
        for case in fixtures['cases']:
            with self.subTest(case=case['name']):
                if case.get('valid'):
                    validate_attempt_origin(case['body'], fixtures['scenarios'])
                else:
                    with self.assertRaises(ContractError) as caught:
                        validate_attempt_origin(case['body'], fixtures['scenarios'])
                    self.assertEqual((caught.exception.reason,caught.exception.field_path),(case['reason'],case['path']))
