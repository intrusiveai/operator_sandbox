import hashlib
import json
from pathlib import Path
import unittest
from jsonschema import Draft202012Validator
from operator_contracts import Protocol, ContractError

ROOT=Path(__file__).resolve().parents[3]/'schemas'

def encoded(value):
    return json.dumps(value,ensure_ascii=False,separators=(',',':')).encode()

class ModelToolsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol=Protocol(ROOT)
        cls.models=[]
        for file in ('model-codec','responses-model-codec','anthropic-model-codec','bedrock-model-codec','gemini-model-codec'):
            cls.models.extend(json.loads((ROOT/'fixtures'/f'{file}.json').read_text()))

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
                    base=next(v for v in self.models if v['mode']=='context' and v['valid'] and v['policy']['codec_id']==c['codec'])
                    policy=json.loads(encoded(base['policy']));request=json.loads(encoded(base['request']))
                    policy['tools']=tools
                    if c['codec']=='bedrock-converse-text-tools-v1':request['request']['toolConfig']['tools']=tools
                    else:request['request']['tools']=tools
                    p.validate_model_request(encoded(policy),encoded(request))
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
