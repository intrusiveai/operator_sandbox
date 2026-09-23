"""Validate capability-chain golden files and positive/negative admission cases.

Requires jsonschema. This checks contract fixtures, not Operator runtime readiness.
"""
from copy import deepcopy
import json
from pathlib import Path
from jsonschema import Draft202012Validator
from capability_fixture_support import (
    check_admission, fixture_jcs, project, projection_digest, raw_digest,
)

ROOT = Path(__file__).resolve().parent
DIR = ROOT/'fixtures/capability-chain'

def load(path):
    def unique_pairs(pairs):
        result = {}
        for key, value in pairs:
            if key in result: raise ValueError('duplicate JSON key')
            result[key] = value
        return result
    return json.loads(path.read_text(), object_pairs_hook=unique_pairs)

def validator(name):
    schema = load(ROOT/(name+'.schema.json'))
    Draft202012Validator.check_schema(schema)
    return Draft202012Validator(schema)

bundle_validator = validator('scenario-bundle')
public_validator = validator('target-capability-manifest')

def validate_public(export):
    public_validator.validate(export)
    fields = [('operations','operation:','operation_id'),('actions','action:injection:','surface'),('services','service:','service_id'),('file_namespaces','file_namespace:','namespace_id'),('evidence_classes','evidence:','feedback_kind')]
    for group,prefix,field in fields:
        refs = [item['ref'] for item in export['capabilities'][group]]
        if refs != sorted(set(refs)): raise ValueError('duplicate or unsorted public refs')
        for item in export['capabilities'][group]:
            if item['ref'] != prefix+item[field]: raise ValueError('public ref does not name its item')
    for service in export['capabilities']['services']:
        for group,field in [('endpoints','id'),('creatable_collections','collection')]:
            ids = [item[field] for item in service.get(group,[])]
            if ids != sorted(set(ids)): raise ValueError('duplicate or unsorted service identifiers')

def validate_bundle(bundle):
    bundle_validator.validate(bundle)
    def ids(items, field):
        values = [item[field] for item in items]
        if len(values) != len(set(values)): raise ValueError('duplicate '+field)
        return set(values)
    objectives = ids(bundle['objectives'],'objective_id')
    ids(bundle['scenarios'],'scenario_id')
    artifacts = ids(bundle['artifacts'],'artifact_id')
    if set(bundle['coverage']['objective_refs']) != objectives: raise ValueError('incomplete objective coverage')
    for item in bundle['objectives']+bundle['scenarios']:
        if not set(item.get('artifact_refs',[])) <= artifacts: raise ValueError('dangling artifact reference')
    for scenario in bundle['scenarios']:
        if not set(scenario['objective_refs']) <= objectives: raise ValueError('dangling objective reference')
    def bounded_strings(value, path=()):
        if isinstance(value,dict):
            for k,v in value.items(): bounded_strings(v,path+(k,))
        elif isinstance(value,list):
            for v in value: bounded_strings(v,path)
        elif isinstance(value,str):
            limit = 16384 if path == ('context',) else 4096
            if len(value.encode()) > limit: raise ValueError('UTF-8 byte limit')
    bounded_strings(bundle)

raw = (DIR/'interceptor-export.json').read_bytes()
native = load(DIR/'interceptor-export.json')
preimage = (DIR/'native-digest-input.json').read_bytes()
assert raw_digest(preimage) == native['digest'], 'native digest must use actual Go preimage'
assert json.loads(preimage) == dict(native,digest=''), 'native digest input differs'
public = load(DIR/'public-capabilities.json')
validate_public(public)
assert project(native,raw,'delivery-example') == public, 'native-to-public mapping drift'
canonical = (DIR/'projection-canonical.json').read_bytes()
assert canonical == fixture_jcs(public['capabilities'])
assert raw_digest(canonical) == public['capability_projection_digest']
assert public['source']['raw_digest'] == raw_digest(raw)
base = load(DIR/'submitted-bundle.json')
validate_bundle(base)
assert check_admission(base,public,public) == []
count = 1

def case(name, mutate, valid, gaps=None):
    global count
    bundle, authoring, live = deepcopy(base), deepcopy(public), deepcopy(public)
    mutate(bundle,authoring,live)
    try:
        validate_bundle(bundle)
        validate_public(authoring)
        validate_public(live)
        got = check_admission(bundle,authoring,live)
    except (ValueError, __import__('jsonschema').ValidationError):
        if valid: raise AssertionError(name+' unexpectedly rejected')
    else:
        if not valid: raise AssertionError(name+' unexpectedly accepted')
        if gaps is not None: assert got == gaps, (name,got)
    count += 1

def changed_live(live, change):
    change(live['capabilities'])
    live['capability_projection_digest'] = projection_digest(live['capabilities'])
    live['source']['capability_source_digest'] = 'sha256:'+'1'*64

case('objectives-only without requirements',lambda b,a,l:(b.update(scenarios=[]),b['target_requirements'].pop('required_capability_refs')),True,[])
case('descriptive extension',lambda b,a,l:b.update(metadata=[{'namespace':'customer','key':'note','value':'Any descriptive context'}]),True,[])
case('unknown execution field',lambda b,a,l:b.update(exec='shell'),False)
case('wrong action namespace',lambda b,a,l:b['scenarios'][0]['guidance'].update(action_refs=['operation:invoke']),False)
case('wrong evidence namespace',lambda b,a,l:b['evidence'].update(requested_classes=['service:tickets']),False)
case('unknown required ref',lambda b,a,l:b['target_requirements'].update(required_capability_refs=['operation:typo']),False)
case('unknown optional ref',lambda b,a,l:b['scenarios'][0].update(required_capability_refs=['service:missing']),True,['service:missing'])
case('unavailable suggestion on required scenario',lambda b,a,l:(b['scenarios'][0].update(required=True),b['scenarios'][0]['guidance'].update(action_refs=['action:injection:missing'])),True,['action:injection:missing'])
case('unavailable optional evidence',lambda b,a,l:b['evidence'].update(requested_classes=['evidence:oracle_outcome']),True,['evidence:oracle_outcome'])
case('unavailable required evidence',lambda b,a,l:b['target_requirements'].update(required_capability_refs=['evidence:oracle_outcome']),False)
case('source mismatch',lambda b,a,l:b['target_requirements'].update(capability_source_digest='sha256:'+'0'*64),False)
case('projection corruption',lambda b,a,l:l.update(capability_projection_digest='sha256:'+'0'*64),False)
case('different logical target',lambda b,a,l:changed_live(l,lambda c:c.update(target_id='different')),False)
case('same exact pin',lambda b,a,l:b['target_requirements'].update(capability_projection_digest=a['capability_projection_digest']),True,[])
case('unrelated live capability addition',lambda b,a,l:changed_live(l,lambda c:c['features'].append('feature:snapshots')),True,[])
case('unrelated change with exact pin',lambda b,a,l:(b['target_requirements'].update(capability_projection_digest=a['capability_projection_digest']),changed_live(l,lambda c:c['features'].append('feature:snapshots'))),False)
case('source-only change with exact pin',lambda b,a,l:(b['target_requirements'].update(capability_projection_digest=a['capability_projection_digest']),l['source'].update(capability_source_digest='sha256:'+'2'*64)),True,[])
case('required operation disappeared',lambda b,a,l:changed_live(l,lambda c:c.update(operations=[])),False)
case('optional service disappeared',lambda b,a,l:changed_live(l,lambda c:c.update(services=[])),True,['service:tickets'])
case('required service disappeared',lambda b,a,l:(b['scenarios'][0].update(required=True),changed_live(l,lambda c:c.update(services=[]))),False)
case('input contract missing',lambda b,a,l:changed_live(l,lambda c:(c['operations'][0].update(delivery_status='missing-input-contract'),c['operations'][0].pop('delivery'))),False)
case('new described input shape',lambda b,a,l:changed_live(l,lambda c:c['operations'][0]['delivery']['input']['schema'].update(required=[])),True,[])
case('changed description',lambda b,a,l:changed_live(l,lambda c:c['operations'][0]['delivery'].update(description='Updated documentation')),True,[])
case('invalid exact pin',lambda b,a,l:b['target_requirements'].update(capability_projection_digest='sha256:'+'0'*64),False)
case('duplicate objective ID',lambda b,a,l:b['objectives'].append(dict(b['objectives'][0],description='Another record')),False)
case('dangling objective',lambda b,a,l:b['scenarios'][0].update(objective_refs=['missing']),False)
case('dangling artifact',lambda b,a,l:b['objectives'][0].update(artifact_refs=['missing']),False)
case('incomplete coverage',lambda b,a,l:b['coverage'].update(objective_refs=['missing']),False)
case('UTF-8 byte bound',lambda b,a,l:b['objectives'][0].update(description='界'*2000),False)
case('malformed digest',lambda b,a,l:b['target_requirements'].update(capability_source_digest='not-a-digest'),False)
case('unknown nested field',lambda b,a,l:b['target_requirements'].update(credential='secret'),False)
case('public ref wrong namespace',lambda b,a,l:changed_live(l,lambda c:c['operations'][0].update(ref='service:invoke')),False)
case('public ref wrong item',lambda b,a,l:changed_live(l,lambda c:c['operations'][0].update(ref='operation:other')),False)
case('duplicate public ID',lambda b,a,l:changed_live(l,lambda c:c['services'].append(deepcopy(c['services'][0]))),False)
case('partial harness limits',lambda b,a,l:b.update(requested_limits={'harness':{'max_model_turns':10}}),True,[])
case('nonpositive requested limit',lambda b,a,l:b.update(requested_limits={'attempt_admissions':0}),False)
# Host policy is separately narrowed; an export alone does not authorize the operation.
try:
    check_admission(base,public,public,denied_refs=['operation:invoke'])
except ValueError: count += 1
else: raise AssertionError('host-denied required operation accepted')
for path in ROOT.glob('fixtures/scenario-bundle-*.json'):
    validate_bundle(load(path))
    count += 1
# No host routes or provider/authentication settings become public capability records.
assert 'cross_vm_operations' not in public['capabilities']
assert 'model_providers' not in public['capabilities']
assert 'method' not in public['capabilities']['operations'][0]
assert 'path' not in public['capabilities']['operations'][0]
print(f'{count} capability/bundle conformance cases passed; native and projection digest preimages verified')
