import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError

class SizePolicyTest(unittest.TestCase):
    def test_shared_size_policy(self):
        root=Path(__file__).resolve().parents[3]/'schemas'
        p=Protocol(root)
        for c in json.loads((root/'fixtures/size-policy.json').read_text()):
            with self.subTest(case=c['name']):
                if c['repeat'] is not None: c['body']['input']['text']='x'*c['repeat']
                q=dict(api_version='operator.dev/engine-pipe/v1alpha1',kind='request',seq=0,
                       campaign_id='campaign-1',launch_id='launch-1',run_revision=1,
                       call_id='call-1',operation_id='request-1',operation=c['operation'],
                       timeout_ms=123456,body=c['body'])
                raw=json.dumps(q).encode()
                if c['valid']: p.validate_request_sizes(raw,c['limits'])
                else:
                    with self.assertRaises(ContractError) as caught: p.validate_request_sizes(raw,c['limits'])
                    self.assertEqual(caught.exception.reason,'configured_size_exceeded')
                    self.assertEqual(caught.exception.field_path,c['field_path'])
