import json
from pathlib import Path
import unittest
from operator_contracts import Protocol
from operator_contracts.canonical import canonicalize,raw_digest
from operator_contracts.target_context import compact_target_context

class TargetContextTest(unittest.TestCase):
    def test_shared_context_and_tools(self):
        root=Path(__file__).resolve().parents[3]/'schemas';p=Protocol(root)
        enc=lambda x:json.dumps(x).encode()
        for case in json.loads((root/'fixtures/compact-target-context.json').read_text()):
            view,files=compact_target_context(p,enc(case['target']))
            self.assertEqual(view,canonicalize(enc(case['view'])))
            self.assertEqual(files,{k:canonicalize(enc(v)) for k,v in case['references'].items()})
        for case in json.loads((root/'fixtures/selected-model-tools.json').read_text()):
            raw=p.model_tools(case['codec'],case['operations'],case['surfaces'])
            self.assertEqual((len(raw),raw_digest(raw)),(case['size_bytes'],case['digest']))
        self.assertEqual(p.validate_tool_arguments('reference_read',enc(dict(reference_id='reference-1',offset=0)))['max_bytes'],4096)
