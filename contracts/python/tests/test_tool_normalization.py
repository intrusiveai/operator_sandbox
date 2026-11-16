import json
from pathlib import Path
import unittest
from operator_contracts import Protocol,ContractError
from operator_contracts.tool_normalization import remap_tool_path
ROOT=Path(__file__).resolve().parents[3]/'schemas'
class ToolNormalizationTest(unittest.TestCase):
    def test_shared_normalization(self):
        p=Protocol(ROOT)
        for case in json.loads((ROOT/'fixtures/tool-normalization.json').read_text()):
            with self.subTest(case=case['name']):
                raw=json.dumps(case['arguments']).encode()
                if 'reason' in case:
                    with self.assertRaises(ContractError) as caught:p.normalize_tool_arguments(case['tool'],raw,case['context'])
                    self.assertEqual((caught.exception.reason,caught.exception.field_path),(case['reason'],case['path']))
                    if 'correction' in case:self.assertEqual(caught.exception.correction,case['correction'])
                else:
                    result=p.normalize_tool_arguments(case['tool'],raw,case['context'])
                    self.assertEqual(result,case['expected'])
                    self.assertEqual(result,p.normalize_tool_arguments(case['tool'],raw,case['context']))
                    if case['name']=='derived claim receipts':
                        self.assertEqual(result['arguments']['assessment']['claims'][0]['attempt_receipt_refs'],['receipt-123'])
                        self.assertEqual(remap_tool_path(result['paths'],'/assessment/claims/0/attempt_receipt_refs/0'),'/claims/0/observations/0')

    def test_publication_size_fallback(self):
        p=Protocol(ROOT);value='x'*((1<<20)+1)
        p.validate_tool_arguments('artifact_publish',json.dumps(dict(purpose='payload',media_type='application/json',json=value)).encode())
        with self.assertRaises(ContractError) as caught:p.validate_tool_arguments('attempt_execute',json.dumps(dict(origin='exploratory',operation_id='invoke',json=value)).encode())
        self.assertEqual(caught.exception.reason,'direct_input_too_large')
