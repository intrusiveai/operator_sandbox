import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
ROOT=Path(__file__).resolve().parents[3]
class ModelTest(unittest.TestCase):
    def test_shared_relay(self):
        p=Protocol(ROOT/'schemas')
        for c in json.loads((ROOT/'schemas/fixtures/model-relay.json').read_text()):
            with self.subTest(case=c['name']):
                args=[json.dumps(c[k]).encode() for k in ('model','request','result')]
                if c['valid']:p.validate_model_exchange(*args)
                else:
                    with self.assertRaises(ContractError):p.validate_model_exchange(*args)
        with self.assertRaises(ContractError):p.validate_model_request(b'{"purpose":"generation","conversation":[],"conversation":[]}')
