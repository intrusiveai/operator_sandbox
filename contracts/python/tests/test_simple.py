import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
from operator_contracts.validation import decode
ROOT=Path(__file__).resolve().parents[3]
class SimpleWorkflowTest(unittest.TestCase):
 def test_shared_examples(self):
  p=Protocol(ROOT/'schemas');f=json.loads((ROOT/'schemas/fixtures/simple-workflow.json').read_text());enc=lambda v:json.dumps(v,ensure_ascii=False).encode()
  for c in f['cases']:
   with self.subTest(case=c['name']):
    def check():
     if c['mode']=='input':
      result=p.resolve_direct_input(decode(enc(c['value'])))
      if c.get('artifact_receipt'):self.assertEqual(result,dict(artifact_receipt=c['artifact_receipt']))
      else:self.assertEqual(result,dict(content=c['bytes_utf8'].encode(),media_type=c['media_type'],canonicalization=c['canonicalization']))
     else:
      result=p.normalize_assessment(enc(c['value']),enc(f['binding']),f['finish_reason'])
      self.assertEqual(result['status'],'partial')
      for field in ('hypotheses','claims','coverage','uncertainties','record_refs'):self.assertIsInstance(result[field],list)
    if c['valid']:check()
    else:
     with self.assertRaises(ContractError):check()
 def test_shared_argument_diagnostics(self):
  p=Protocol(ROOT/'schemas');f=json.loads((ROOT/'schemas/fixtures/argument-diagnostics.json').read_text())
  for c in f['cases']:
   with self.subTest(case=c['name']):
    with self.assertRaises(ContractError) as caught:p.validate_tool_arguments(c['tool'],c['raw'].encode())
    self.assertEqual(caught.exception.reason,c['reason'])
    self.assertEqual(caught.exception.field_path,c['path'])
