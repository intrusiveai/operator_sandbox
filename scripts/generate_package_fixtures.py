#!/usr/bin/env python3
"""Author package byte-integrity fixtures using the independent Node oracle.

This small payload exercises metadata verification only. It is intentionally not
an executable contract release. Loader tests separately use the actual catalog.
"""
import json
from copy import deepcopy
from pathlib import Path
from generate_identity_fixtures import canonical, object_digest, descriptor, digest, b64, enc

ROOT = Path(__file__).resolve().parents[1]
D = 'sha256:' + 'f' * 64
files = {'catalog.json': enc({'urn:operator:fixture:sample': 'sample.schema.json'}),
         'operations.json': enc({'fixture': 'registry bytes; not a runnable registry'}),
         'sample.schema.json': enc({'$schema': 'https://json-schema.org/draft/2020-12/schema', '$id': 'urn:operator:fixture:sample', 'type': 'object'}),
         'semantics/digests.md': b'Fixture digest semantics.\n',
         'semantics/paths.md': b'Fixture path semantics.\n',
         'semantics/limits.md': b'Fixture limit semantics.\n',
         'python/validator.py': b'# Fixture source, never executed by the verifier.\n',
         'fixtures/example.json': b'{}\n'}
profiles = {'jcs-v1': 'semantics/digests.md', 'manifest-paths-v1': 'semantics/paths.md', 'harness-loop-v1': 'semantics/limits.md'}
manifest = {'api_version': 'operator.dev/contract-package/v1alpha1', 'package_version': '0.0.0',
            'files': [{'path': path, **descriptor(raw)} for path, raw in sorted(files.items())],
            'catalog': {'path': 'catalog.json', 'object_digest': object_digest(json.loads(files['catalog.json']))},
            'operations': {'path': 'operations.json', 'object_digest': object_digest(json.loads(files['operations.json']))},
            'semantic_profiles': [{'profile_id': key, 'path': path, 'digest': digest(files[path])} for key, path in sorted(profiles.items())]}
cases = []

def case(name, mutate=lambda m, f: None, valid=False, mode='verify', expected_change=None, pretty=False):
    m, f = deepcopy(manifest), dict(files)
    mutate(m, f)
    raw = enc(m) if not pretty else json.dumps(m, indent=2).encode()
    identity = {'package_version': m['package_version'], 'package_digest': object_digest(m)}
    if expected_change: identity.update(expected_change)
    item = {'name': name, 'mode': mode, 'valid': valid, 'manifest_base64': b64(raw), 'expected': identity}
    if mode != 'manifest': item['files_base64'] = {path: b64(raw) for path, raw in f.items()}
    if mode == 'build': item.update(profiles=profiles, canonical_base64=b64(canonical(m)))
    cases.append(item)

case('valid payload identity', valid=True)
case('manifest whitespace does not change identity', valid=True, pretty=True)
case('builder emits expected canonical manifest', valid=True, mode='build')
case('wrong expected digest', expected_change={'package_digest': D})
case('wrong expected version', expected_change={'package_version': '0.0.1'})
case('missing payload', lambda m,f:f.pop('python/validator.py'))
case('extra payload', lambda m,f:f.update({'unlisted': b'x'}))
case('same size changed payload', lambda m,f:f.update({'fixtures/example.json': b'[]\n'}))
case('changed payload length', lambda m,f:f.update({'fixtures/example.json': b'{}'}))
case('inventory digest tampered', lambda m,f:m['files'][0].update(digest=D))
case('catalog object digest tampered', lambda m,f:m['catalog'].update(object_digest=D))
case('operations object digest tampered', lambda m,f:m['operations'].update(object_digest=D))
case('semantic profile digest tampered', lambda m,f:m['semantic_profiles'][0].update(digest=D))
case('semantic profile resource absent', lambda m,f:m['semantic_profiles'][0].update(path='absent.md'))
case('missing required semantic profile', lambda m,f:m['semantic_profiles'][0].update(profile_id='custom-v1'))
case('repeated semantic profile ID', lambda m,f:m['semantic_profiles'].append(deepcopy(m['semantic_profiles'][0])))
case('unordered semantic profiles', lambda m,f:m['semantic_profiles'].reverse())
case('unordered inventory', lambda m,f:m['files'].reverse())
case('repeated inventory file', lambda m,f:m['files'].append(deepcopy(m['files'][0])))
for name, path in [('absolute', '/payload'), ('traversal', '../payload'), ('backslash', 'a\\b'), ('empty component', 'a//b'), ('self manifest', 'package.json'), ('manifest directory', 'package.json/child'), ('signatures', 'signatures/file'), ('case folded signatures', 'SIGNATURES/file')]:
    def mutate(m, f, path=path):
        m['files'].append({'path': path, **descriptor(b'x')});m['files'].sort(key=lambda e:e['path'])
    case(name + ' inventory path', mutate, mode='manifest')
case('missing catalog inventory', lambda m,f:m.update(files=[e for e in m['files'] if e['path'] != 'catalog.json']), mode='manifest')
case('private manifest field', lambda m,f:m.update(secret='forbidden'), mode='manifest')
case('prerelease package version', lambda m,f:m.update(package_version='0.1.0-beta'), mode='manifest')
case('unknown manifest version', lambda m,f:m.update(api_version='operator.dev/contract-package/v9'), mode='manifest')
case('per file size ceiling', lambda m,f:m['files'][0].update(size_bytes=16777217), mode='manifest')

def aggregate(m, f):
    m['files']=[{'path':'catalog.json',**descriptor(b'{}')},{'path':'operations.json',**descriptor(b'{}')},*[{'path':f'payload-{i}', 'size_bytes':16777216,'digest':D} for i in range(9)]]
case('aggregate size ceiling', aggregate, mode='manifest')

def collision(m,f):
    m['files'] += [{'path':'A/file','size_bytes':1,'digest':D},{'path':'a/other','size_bytes':1,'digest':D}];m['files'].sort(key=lambda e:e['path'])
case('directory case collision', collision, mode='manifest')

def rewrite_catalog(m, f, content):
    f['catalog.json']=enc(content);m['catalog']['object_digest']=object_digest(content)
    for entry in m['files']:
        if entry['path']=='catalog.json':entry.update(descriptor(f['catalog.json']))
case('catalog references absent file', lambda m,f:rewrite_catalog(m,f,{'urn:fixture':'missing.schema.json'}))
case('catalog references nested path', lambda m,f:rewrite_catalog(m,f,{'urn:fixture':'nested/sample.schema.json'}))
case('catalog duplicates schema file', lambda m,f:rewrite_catalog(m,f,{'urn:a':'sample.schema.json','urn:b':'sample.schema.json'}))
case('catalog must be nonempty', lambda m,f:rewrite_catalog(m,f,{}))
case('catalog must be object', lambda m,f:rewrite_catalog(m,f,[]))
(ROOT/'schemas/fixtures/package-integrity.json').write_text(json.dumps(cases,indent=2)+'\n')
print(f'{len(cases)} package integrity cases')
