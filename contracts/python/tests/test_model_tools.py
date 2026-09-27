import hashlib
import json
from pathlib import Path
import unittest
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

if __name__=='__main__':unittest.main()
