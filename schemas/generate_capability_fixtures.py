"""Explicitly regenerate public golden outputs after reviewing the native export."""
import json
from pathlib import Path
from capability_fixture_support import fixture_jcs, project

ROOT = Path(__file__).resolve().parent
DIR = ROOT/'fixtures/capability-chain'
raw = (DIR/'interceptor-export.json').read_bytes()
public = project(json.loads(raw), raw, 'delivery-example')
(DIR/'public-capabilities.json').write_text(json.dumps(public, indent=2)+'\n')
(DIR/'projection-canonical.json').write_bytes(fixture_jcs(public['capabilities']))
bundle = json.loads((ROOT/'fixtures/scenario-bundle-scenarios.json').read_text())
bundle['bundle_id'] = 'delivery-example-scenarios'
bundle['target_requirements'] = {'target_id':'delivery-example', 'capability_source_digest':public['source']['capability_source_digest'], 'required_capability_refs':['operation:invoke']}
scenario = bundle['scenarios'][0]
scenario['required_capability_refs'] = ['action:injection:mcp_tool_result','service:tickets']
scenario['guidance']['action_refs'] = ['action:injection:mcp_tool_result']
bundle['evidence']['requested_classes'] = ['evidence:target_output']
(DIR/'submitted-bundle.json').write_text(json.dumps(bundle,indent=2)+'\n')
print('Regenerated public projection, canonical preimage and compatible bundle; no exact pin.')
