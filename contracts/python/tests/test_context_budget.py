import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
from operator_contracts.model import check_context_limits

class ContextBudgetTest(unittest.TestCase):
    def test_correlated_budget(self):
        root=Path(__file__).resolve().parents[3]/'schemas'
        p=Protocol(root)
        f=json.loads((root/'fixtures/model-codec.json').read_text())[0]
        enc=lambda x:json.dumps(x).encode()
        policy=f['policy'];policy['context_limits']=dict(maximum_prompt_tokens=32768,maximum_completion_tokens=1024,warning_threshold_tokens=26214)
        result=f['result'];result.pop('context_budget',None)
        with self.assertRaises(ContractError):p.validate_model_exchange(enc(policy),enc(f['request']),enc(result))
        result['context_budget']=dict(input_tokens=100,output_tokens=20,total_tokens=120,requested_completion_tokens=1024,reserved_tokens=33792,remaining_prompt_tokens=32668,near_prompt_limit=False,remaining_model_tokens=5000,remaining_model_turns=8)
        p.validate_model_exchange(enc(policy),enc(f['request']),enc(result))
        for field,value in [('input_tokens',101),('near_prompt_limit',True),('remaining_prompt_tokens',0),('reserved_tokens',33791)]:
            original=result['context_budget'][field];result['context_budget'][field]=value
            with self.assertRaises(ContractError):p.validate_model_exchange(enc(policy),enc(f['request']),enc(result))
            result['context_budget'][field]=original
        with self.assertRaises(ContractError):check_context_limits(dict(maximum_prompt_tokens=1,maximum_completion_tokens=2**53-1,warning_threshold_tokens=1))
