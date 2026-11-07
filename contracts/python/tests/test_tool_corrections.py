import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
from operator_contracts.schema_diagnostic import schema_argument_issue
from jsonschema import Draft202012Validator, ValidationError
ROOT=Path(__file__).resolve().parents[3]

class ToolCorrections(unittest.TestCase):
    def test_shared_tools(self):
        p=Protocol(ROOT/'schemas')
        for c in json.loads((ROOT/'schemas/fixtures/tool-corrections.json').read_text()):
            with self.subTest(c['name']):
                with self.assertRaises(ContractError) as caught:p.validate_tool_arguments(c['tool'],json.dumps(c['bad']).encode())
                e=caught.exception
                self.assertEqual(e.field_path,c['path'])
                self.assertEqual(e.correction,c['correction'])
                for part in c['contains']:self.assertIn(part,e.correction)
                self.assertNotIn('SYNTHETIC_SECRET',str(e))
                p.validate_tool_arguments(c['tool'],json.dumps(c['good']).encode())
    def test_constraint_families(self):
        cases=[({'oneOf':[{'type':'integer','maximum':1},{'type':'string'}]},2,1,'less than or equal to 1'),({'type':'integer'},'x',1,'Expected integer'),({'const':'fixed'},'wrong','fixed','fixed'),
            ({'minProperties':1},{},{'x':1},'at least 1 properties'),({'maxProperties':1},{'x':1,'y':2},{'x':1},'at most 1 properties'),
            ({'minItems':1},[],[1],'at least 1 items'),({'maxItems':1},[1,2],[1],'at most 1 items'),
            ({'exclusiveMinimum':0},0,1,'greater than 0'),({'exclusiveMaximum':2},2,1,'less than 2'),
            ({'multipleOf':2},3,4,'multiple of 2'),
            ({'oneOf':[{'required':['text']},{'required':['json']}]},{},{'text':'x'},'Choose exactly one alternative'),
            ({'anyOf':[{'required':['text']},{'required':['json']}]},{},{'text':'x'},'Choose a matching alternative'),
            ({'if':{'required':['text']},'then':{'required':['media_type']}},{'text':'x'},{'text':'x','media_type':'text/plain'},'media_type')]
        for schema,bad,good,expect in cases:
            with self.subTest(schema=schema):
                with self.assertRaises(ValidationError) as caught:Draft202012Validator(schema).validate(bad)
                self.assertIn(expect,schema_argument_issue(caught.exception).correction)
                Draft202012Validator(schema).validate(good)

    def test_normalization_preserves_host_constraint(self):
        p=Protocol(ROOT/'schemas')
        # A model-valid label expands into a host request whose canonical
        # artifact identity must come from trusted state, never the model.
        context=dict(campaign_id='campaign-1',call_id='call-1',resources={'artifact-1':dict(kind='artifact',campaign_id='campaign-1',value={'purpose':'payload'},available=True)})
        with self.assertRaises(ContractError) as caught:
            p.normalize_tool_arguments('attempt_execute',json.dumps(dict(origin='exploratory',operation_id='invoke',artifact='artifact-1')).encode(),context)
        self.assertEqual(caught.exception.reason,'invalid_resolution_context')
        self.assertIn('do not supply internal identities',caught.exception.correction)
