import json
from pathlib import Path
import unittest

from operator_contracts import Protocol, ContractError, canonicalize

ROOT=Path(__file__).resolve().parents[3]


def encoded(value):
    return json.dumps(value,ensure_ascii=False,separators=(',',':')).encode('utf-8')


def envelope(body,response=False):
    message=dict(api_version='operator.dev/engine-pipe/v1alpha1',kind='response' if response else 'request',
                 seq=0,campaign_id='campaign-1',launch_id='launch-1',run_revision=1,call_id='call-1',
                 operation_id='operation-1',operation='engine.model_generate')
    if response: message['result']=body
    else: message.update(timeout_ms=120000,body=body)
    return encoded(message)


class ModelTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol=Protocol(ROOT/'schemas')
        cls.cases=json.loads((ROOT/'schemas/fixtures/model-codec.json').read_text())
        cls.cases+=json.loads((ROOT/'schemas/fixtures/anthropic-model-codec.json').read_text())
        cls.cases+=json.loads((ROOT/'schemas/fixtures/bedrock-model-codec.json').read_text())

    def test_shared_model_codec(self):
        p=self.protocol
        for c in self.cases:
            with self.subTest(case=c['name']):
                def run():
                    if c['mode']=='context':
                        policy=p.model_policy_from_context(encoded(c['context']),encoded(c['tools']),c['prompt'].encode())
                        self.assertEqual(policy,canonicalize(encoded(c['policy'])))
                        p.validate_model_request(policy,encoded(c['request']))
                    elif c['mode']=='bound':
                        self.assertEqual(p.validate_model_exchange(encoded(c['policy']),encoded(c['request']),encoded(c['result'])),c['result'])
                    elif c['mode']=='wire':
                        p.validate_response(envelope(c['request']),envelope(c['result'],True))
                    elif c['mode'].endswith('continuation'):
                        continuation={'continuation':p.chat_continuation,'anthropic-continuation':p.anthropic_continuation,
                                      'bedrock-continuation':p.bedrock_continuation}[c['mode']]
                        raw=continuation(encoded(c['result']),c['tool_results'])
                        self.assertEqual(raw,c['segment_json'].encode())
                        request=json.loads(encoded(c['request']))
                        request['request']['messages'].extend(json.loads(raw))
                        p.validate_model_request(encoded(c['policy']),encoded(request))
                    else: self.fail('unknown fixture mode')
                if c['valid']:
                    run()
                    if not c['mode'].endswith('continuation') and c['mode']!='context': self.assertEqual(p.model_disposition(encoded(c['result'])),c['disposition'])
                    if 'metrics' in c: self.assertEqual(p.model_usage(encoded(c['result'])),c['metrics'])
                    if 'output_limit' in c: self.assertEqual(p.model_output_limit(encoded(c['request'])),c['output_limit'])
                else:
                    with self.assertRaises(ContractError): run()

    def test_bounds_and_registry(self):
        p=self.protocol
        op=next(o for o in p.operations() if o['name']=='engine.model_generate')
        self.assertEqual((op['timeout_ms'],op['effect'],op['receipt_policy'],op['finalization_rule']),
                         (120000,'model-call','durable','denied'))
        c=next(c for c in self.cases if c['name']=='invalid local arguments remain exact in followup')
        for content in ('\ud800','x'*(1048576+1)):
            with self.assertRaises(ContractError): p.chat_continuation(encoded(c['result']),[dict(tool_call_id='call-1',content=content)])
        request=json.loads(encoded(self.cases[0]['request']))
        request['request']['messages'][1]['content']='x'*4194304
        with self.assertRaises(ContractError): p.validate_model_request(encoded(self.cases[0]['policy']),encoded(request))


if __name__=='__main__': unittest.main()
