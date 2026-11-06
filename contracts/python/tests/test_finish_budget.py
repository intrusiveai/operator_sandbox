import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
from operator_contracts.model_tools import tool_guidance

ROOT = Path(__file__).resolve().parents[3] / 'schemas'
class FinishBudgetTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls): cls.p = Protocol(ROOT)

    def test_budget_fixtures(self):
        for case in json.loads((ROOT/'fixtures/budget-diagnostics.json').read_text()):
            with self.subTest(case=case['name']):
                raw = json.dumps(case['request']).encode()
                if case['valid']: self.p.validate_request(raw)
                else:
                    with self.assertRaises(ContractError): self.p.validate_request(raw)

    def test_finish_corrections(self):
        for case in json.loads((ROOT/'fixtures/finish-diagnostics.json').read_text()):
            with self.subTest(case=case['name']):
                with self.assertRaises(ContractError) as caught:
                    self.p.validate_tool_arguments('finish',json.dumps(case['arguments']).encode())
                issue = caught.exception
                self.assertEqual((issue.field_path,issue.correction),(case['path'],case['correction']))
                self.assertNotIn('SYNTHETIC_SECRET',str(issue))

    def test_guidance_examples(self):
        guide = json.loads(tool_guidance(self.p, 'finish'))['examples']
        self.p.validate_tool_arguments('finish',json.dumps(guide['minimal_inline_example']).encode())
        guide = json.loads(tool_guidance(self.p, 'observation_read'))['examples']['pagination_example']
        self.p.validate_tool_arguments('observation_read',json.dumps(guide['next_request']).encode())
