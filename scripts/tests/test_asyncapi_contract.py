import json
import sys
import unittest
from pathlib import Path
from copy import deepcopy
import jsonschema
ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/'scripts'))
from check_harness_contract import check_document
from generate_asyncapi import generate


class AsyncAPIContractTest(unittest.TestCase):
    def test_approved_offline_document(self):
        identity=check_document()
        self.assertEqual(identity['package_version'],'0.7.1')
        self.assertEqual(generate(),(ROOT/'schemas/asyncapi.json').read_bytes())

    def test_closed_local_references(self):
        doc=json.loads((ROOT/'schemas/asyncapi.json').read_bytes())
        def walk(value):
            if isinstance(value,dict):
                if '$ref' in value:
                    ref=value['$ref']
                    self.assertTrue(ref.startswith('#/'),ref)
                    target=doc
                    for key in ref[2:].split('/'):
                        target=target[key.replace('~1','/').replace('~0','~')]
                for child in value.values():walk(child)
            elif isinstance(value,list):
                for child in value:walk(child)
        walk(doc)

    def test_actual_wire_fixtures_match_declared_message_payloads(self):
        doc=json.loads((ROOT/'schemas/asyncapi.json').read_bytes())
        fixtures=json.loads((ROOT/'schemas/fixtures/ordinary-protocol.json').read_bytes())
        count=0
        for case in fixtures:
            if not case['valid']:continue # negative semantic rules have dedicated Go/Python tests
            for kind in ('request','response'):
                message=case.get(kind)
                if not message:continue
                key=message['operation']+'.'+kind
                payload=doc['components']['messages'][key]['payload']['schema']
                validator=jsonschema.Draft202012Validator(dict(payload,components=doc['components']))
                validator.validate(message)
                bad=deepcopy(message);bad['operation']='engine.unknown'
                self.assertFalse(validator.is_valid(bad))
                count+=1
        self.assertGreater(count,20)
