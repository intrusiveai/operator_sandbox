import hashlib
import json
from pathlib import Path
import unittest
from jsonschema import Draft202012Validator
from operator_contracts import Protocol, ContractError
from operator_contracts.model_tools import tool_guidance

ROOT=Path(__file__).resolve().parents[3]/'schemas'

def encoded(value):
    return json.dumps(value,ensure_ascii=False,separators=(',',':')).encode()

class ModelToolsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol=Protocol(ROOT)

    def test_shared_tools(self):
        p=self.protocol
        for c in json.loads((ROOT/'fixtures/model-tools.json').read_text()):
            with self.subTest(case=c['name']):
                def run():
                    if c['mode']=='arguments':
                        p.validate_tool_arguments(c['tool'],encoded(c['arguments']))
                        return
                    raw=p.model_tools(c['codec'],c['operations'])
                    self.assertEqual(len(raw),c['size_bytes'])
                    self.assertEqual('sha256:'+hashlib.sha256(raw).hexdigest(),c['digest'])
                    tools=json.loads(raw)
                    if c['codec'] in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1'):
                        for tool in tools:
                            parameters=(tool['function'] if 'function' in tool else tool)['parameters']
                            self.assertEqual(parameters['type'],'object')
                            self.assertIs(parameters['additionalProperties'],False)
                            self.assertIn('properties',parameters)
                            self.assertFalse(set(parameters)&{'oneOf','anyOf','allOf','enum','const','not'})
                    if c['codec']=='gemini-text-tools-v1':names=[v['name'] for v in tools[0]['functionDeclarations']]
                    elif c['codec']=='openai-chat-text-tools-v1':names=[v['function']['name'] for v in tools]
                    elif c['codec']=='bedrock-converse-text-tools-v1':names=[v['toolSpec']['name'] for v in tools]
                    else:names=[v['name'] for v in tools]
                    self.assertEqual(names,c['names'])
                    tools.clear()
                    self.assertEqual(p.model_tools(c['codec'],c['operations']),raw)
                if c['valid']:run()
                else:
                    with self.assertRaises(ContractError):run()

    def test_projection_and_runtime_constraints(self):
        cases=json.loads((ROOT/'fixtures/model-tools.json').read_text())
        for codec in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1'):
            tools=json.loads(self.protocol.model_tools(codec,cases[0]['operations']))
            declarations=[t.get('function',t) for t in tools]
            validators={t['name']:Draft202012Validator(t['parameters']) for t in declarations}
            for case in cases:
                if 'openai_valid' not in case:continue
                with self.subTest(codec=codec,case=case['name']):
                    self.assertEqual(validators[case['tool']].is_valid(case['arguments']),case['openai_valid'])
                    if case['valid']:
                        self.protocol.validate_tool_arguments(case['tool'],encoded(case['arguments']))
                    else:
                        with self.assertRaises(ContractError):
                            self.protocol.validate_tool_arguments(case['tool'],encoded(case['arguments']))

if __name__=='__main__':unittest.main()

class PresentationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol = Protocol(ROOT)

    def test_guidance_is_deferred_and_discoverable(self):
        cases = json.loads((ROOT/'fixtures/model-tools.json').read_text())
        tools = json.loads(self.protocol.model_tools('openai-chat-text-tools-v1', cases[0]['operations']))
        declarations = {v['function']['name']: v['function'] for v in tools}
        reference = declarations['reference_read']['description']
        for text in ('offset 0', 'active scenario', 'no pagination', 'tool-guide:<tool_name>'):
            self.assertIn(text, reference)
        for name, declaration in declarations.items():
            self.assertNotIn('Runtime enforces identifier', declaration['description'])
            if name != 'reference_read':
                self.assertNotIn('tool-guide:', declaration['description'])
        finish = declarations['finish']['description']
        self.assertIn('entire campaign', finish)
        self.assertIn('cleanup', finish)
        self.assertNotIn('No tests ran', finish)
        self.assertIn('ordinary progress does not require', declarations['record_append']['description'])
        self.assertIn('skips later calls', declarations['restore_request']['description'])
        guide = json.loads(tool_guidance(self.protocol, 'finish'))['examples']
        self.protocol.validate_tool_arguments('finish', encoded(guide['minimal_inline_example']))
        for field in ('uncertainty_kinds', 'objective_outcomes', 'handles', 'alternatives', 'correction'):
            self.assertTrue(guide[field])
        observation = json.loads(tool_guidance(self.protocol, 'observation_read'))['examples']
        example = observation['pagination_example']
        self.assertEqual(example['next_request'], example['result_handles']['continuation']['arguments'])
        self.assertNotEqual(example['result_handles']['readable_observation'], example['result_handles']['citation'])
        self.assertIn('truncated', observation['rules'])
        self.assertIn('partial read', observation['rules'])
        database = json.loads(tool_guidance(self.protocol, 'attempt_execute'))['examples']['database_setup']
        for key in ('discovery', 'query', 'state', 'lifecycle'):
            self.assertTrue(database[key])
        for fact in ('key columns', 'writable columns', 'row-creation', 'call ordinals', '/rows/', 'fidelity', 'NULL', 'restore'):
            self.assertIn(fact, json.dumps(database))
