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
                    if c['codec'] in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1'):
                        for tool in tools:
                            declaration=tool.get('function',tool)
                            parameters=declaration.get('parameters',declaration.get('input_schema'))
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

    def test_anthropic_selected_catalog_keeps_nested_rules(self):
        ops=sorted(op['name'] for op in self.protocol.operations())
        for surfaces in ([], ['model_response'], ['database_response','environment_state'],
                         ['database_response','environment_state','mcp_tool_result','model_response','model_tool_result','service_response']):
            tools=json.loads(self.protocol.model_tools('anthropic-messages-text-tools-v1',ops,surfaces))
            self.assertEqual(len(tools),17)
            args={tool['name']:tool['input_schema'] for tool in tools}
            for name, parameters in args.items():
                with self.subTest(tool=name,surfaces=surfaces):
                    self.assertEqual(parameters['type'],'object')
                    self.assertFalse(set(parameters)&{'oneOf','anyOf','allOf'})
                    Draft202012Validator.check_schema(parameters)
            # Root restrictions must not erase nested evidence or injection rules.
            self.assertIn('allOf',args['finish']['properties']['claims']['items'])
            if surfaces:
                self.assertIn('oneOf',args['attempt_execute']['properties']['injection'])
                self.assertEqual(len(args['attempt_execute']['properties']['setup']['items']['oneOf']),len(surfaces))

    def test_projection_and_runtime_constraints(self):
        cases=json.loads((ROOT/'fixtures/model-tools.json').read_text())
        for codec in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1','bedrock-converse-text-tools-v1','gemini-text-tools-v1'):
            tools=json.loads(self.protocol.model_tools(codec,cases[0]['operations']))
            if codec=='gemini-text-tools-v1':declarations=tools[0]['functionDeclarations']
            else:declarations=[t.get('function',t.get('toolSpec',t)) for t in tools]
            declarations=[dict(t,parameters=t.get('parameters',t.get('input_schema',t.get('parametersJsonSchema',t.get('inputSchema',{}).get('json'))))) for t in declarations]
            validators={t['name']:Draft202012Validator(t['parameters']) for t in declarations}
            for case in cases:
                if 'object_root_valid' not in case:continue
                with self.subTest(codec=codec,case=case['name']):
                    expected=case['object_root_valid'] if codec in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1') else case['valid']
                    self.assertEqual(validators[case['tool']].is_valid(case['arguments']),expected)
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
        reference = declarations['scenario_select']['description'] + declarations['reference_read']['description']
        for text in ('active scenario', 'confirmation', 'tool-guide:<tool_name>'):
            self.assertIn(text, reference)
        for name, declaration in declarations.items():
            self.assertNotIn('Runtime enforces identifier', declaration['description'])
            if name != 'reference_read':
                self.assertNotIn('tool-guide:', declaration['description'])
        finish = declarations['finish']['description']
        self.assertIn('entire campaign', finish)
        self.assertIn('cleanup', finish)
        self.assertNotIn('No tests ran', finish)
        self.assertIn('Optional', declarations['record_progress']['description'])
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
        discovered = json.loads(tool_guidance(self.protocol, 'attempt_execute'))['examples']['discovered_setup']
        for fact in ('exact allowlists', 'administrator', 'HEAD', 'existing key string', 'mutable_fields', 'not injection payloads', 'does not undo'):
            self.assertIn(fact, json.dumps(discovered))


class AdjacentConstraintTest(unittest.TestCase):
    def test_provider_root_exceptions_are_documented_and_other_codecs_keep_rules(self):
        p=Protocol(ROOT)
        ops=sorted(op['name'] for op in p.operations())
        for codec in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1','bedrock-converse-text-tools-v1','gemini-text-tools-v1'):
            tools=json.loads(p.model_tools(codec,ops))
            decl=tools[0]['functionDeclarations'] if codec.startswith('gemini') else [t.get('function',t.get('toolSpec',t)) for t in tools]
            args={t['name']:t.get('parameters',t.get('input_schema',t.get('parametersJsonSchema',t.get('inputSchema',{}).get('json')))) for t in decl}
            for tool,key,text in (
                ('attempt_execute','origin','scenario_id'),('attempt_execute','scenario_id','forbidden'),
                ('attempt_execute','json','exactly one'),('attempt_execute','json','application/json'),
                ('attempt_execute','base64','media_type'),('attempt_execute','artifact','payload'),
                ('attempt_execute','setup','retain_injections'),('attempt_execute','retain_injections','false'),
                ('attempt_execute','injection','purpose payload'),
                ('artifact_publish','purpose','conclusion'),('artifact_publish','purpose','assessment byte'),
                ('artifact_publish','json','exactly one'),('artifact_publish','json','application/json'),
                ('record_hypothesis','parent','hypothesis'),('record_lineage','parent','Execution'),
                ('record_progress','observations','assessment.interpretation'),('finish','status','coverage-limit'),
                ('observation_read','observation','Readable'),('restore_request','snapshot','readiness'),
                ('snapshot_list','session','session'),('injection_delete','injection','injection'),
                ('snapshot_request','description','4096 UTF-8 bytes'),
                ('attempt_execute','text','1 MiB UTF-8 bytes')):
                with self.subTest(codec=codec,tool=tool,field=key):
                    self.assertIn(text,args[tool]['properties'][key]['description'])
            self.assertNotIn('json, text, base64, artifact', args['artifact_publish']['properties']['json']['description'])
            self.assertNotIn('exactly one', args['attempt_execute']['properties']['injection']['properties']['json']['description'])
            self.assertEqual(set(args['finish']['required']),{'finish_reason','summary','objectives'})
            self.assertNotIn('record_append',args)
            for tool in ('record_hypothesis','record_progress','record_lineage','record_coverage'):
                self.assertNotIn('kind',args[tool]['properties'])
            self.assertEqual(set(args['scenario_select']['properties']),{'reference'})
            self.assertEqual(set(args['attempt_execute']['properties']['injection']['properties']),{'json','artifact'})
            if codec in ('bedrock-converse-text-tools-v1','gemini-text-tools-v1'):
                self.assertIn('allOf',args['attempt_execute'])
                self.assertIn('allOf',args['artifact_publish'])
            # Encoded byte count is a runtime condition, not JSON Schema maxLength.
            value={'description':'é'*2049}
            self.assertTrue(Draft202012Validator(args['snapshot_request']).is_valid(value))
            with self.assertRaises(ContractError):p.validate_tool_arguments('snapshot_request',encoded(value))
