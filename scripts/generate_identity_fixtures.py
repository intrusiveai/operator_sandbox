#!/usr/bin/env python3
"""Regenerate digest fixtures using Node's ECMAScript number/string serializer.

Node is an authoring oracle only; make test and both libraries do not require it.
No production canonicalization helper is imported here. Review changed golden
bytes/digests whenever registry or source fixture changes alter these vectors.
"""
import base64
from copy import deepcopy
import hashlib
import json
import math
from pathlib import Path
import random
import struct
import subprocess

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / 'schemas/fixtures'
enc = lambda v: json.dumps(v, ensure_ascii=False, separators=(',', ':')).encode()
b64 = lambda raw: base64.b64encode(raw).decode()
unb64 = base64.b64decode
digest = lambda raw: 'sha256:' + hashlib.sha256(raw).hexdigest()
descriptor = lambda raw: {'size_bytes': len(raw), 'digest': digest(raw)}
ORACLE = r'''
const fs = require('node:fs');
function canonical(value) {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  return '{' + Object.keys(value).sort().map(key => JSON.stringify(key) + ':' + canonical(value[key])).join(',') + '}';
}
const inputs = JSON.parse(fs.readFileSync(0, 'utf8'));
process.stdout.write(JSON.stringify(inputs.map(raw => Buffer.from(canonical(JSON.parse(Buffer.from(raw, 'base64').toString('utf8')))).toString('base64'))));
'''


def oracle_many(raws):
    output = subprocess.run(['node', '-e', ORACLE], input=enc([b64(raw) for raw in raws]), capture_output=True, check=True)
    return [unb64(value) for value in json.loads(output.stdout)]


def canonical(value):
    return oracle_many([enc(value)])[0]


def object_digest(value):
    return digest(canonical(value))


def main():
    cases = []

    def add(name, raw, valid=True, maximum=4 << 20):
        cases.append({'name': name, 'raw_base64': b64(raw), 'valid': valid, 'maximum': maximum})

    for name, value in [
        ('recursive sorted objects', {'z': [{'b': 2, 'a': 1}], 'a': {'c': None, 'b': True}}),
        ('UTF16 key order', {'\ue000': 1, '\U00010000': 2, '\U0001f600': 3, '\uffff': 4}),
        ('numeric property names are strings', {'10': 10, '2': 2, '01': 1}),
        ('escaped string content', {'quote': '"\\/\b\t\n\f\r\x00\x1f', 'html': '<>&', 'separators': '\u2028\u2029'}),
        ('no Unicode normalization', {'é': 'é', 'e\u0301': 'e\u0301'}),
        ('empty property and prototype names', {'': 0, '__proto__': {'constructor': 'data'}}),
        ('null differs from absent', {'present': None}),
        ('empty object', {}), ('empty array', []), ('top level string', 'text'),
        ('top level null', None), ('booleans', [True, False]),
        ('array order retained', [3, 1, 2]),
        ('no generic self-field omission', {'digest': 'ordinary data', 'signature': {'value': 'also data'}}),
    ]:
        add(name, json.dumps(value, ensure_ascii=True, indent=2).encode())
    for index, raw in enumerate([
        b'-0.000e+100', b'[1,1.0,1e0,1.00000000000000001]',
        b'333333333.33333329', b'1424953923781206.25', b'0.000001', b'9.999999999999997e-7',
        b'0.0000010000000000000002', b'1e-7', b'-1e-7', b'5e-324', b'-5e-324',
        b'2.2250738585072014e-308', b'9007199254740991', b'-9007199254740991',
        b'1000000000000000', b'1e+15', b'1.234567890123456789012345', b'0e-999999999',
    ]):
        add(f'numeric boundary {index}', raw)
    # Reproducible sample of finite binary64 values in the contract's domain.
    rng = random.Random(8785)
    count = 0
    while count < 256:
        bits = rng.getrandbits(64)
        value = struct.unpack('>d', bits.to_bytes(8, 'big'))[0]
        if not math.isfinite(value) or value == 0 or value.is_integer() and abs(value) > 2**53 - 1:
            continue
        add(f'binary64 {bits:016x}', repr(value).encode())
        count += 1
    for name, raw in [
        ('duplicate keys', b'{"x":1,"x":2}'), ('escaped duplicate', b'{"a":1,"\\u0061":2}'),
        ('lone surrogate', b'"\\ud800"'), ('invalid UTF8', b'"\xff"'), ('BOM', b'\xef\xbb\xbf{}'),
        ('nonfinite', b'NaN'), ('overflow', b'1e309'), ('underflow', b'1e-9999'),
        ('unsafe integer', b'9007199254740992'), ('unsafe rounded integer', b'9007199254740991.9'),
        ('large RFC number outside contract', b'1e30'), ('trailing JSON', b'{}[]'),
        ('depth 33', b'[' * 33 + b'0' + b']' * 33),
    ]: add(name, raw, False)
    add('depth 32', b'[' * 32 + b'0' + b']' * 32)
    add('input ceiling exceeded', b'{} ', False, 2)
    add('output ceiling exceeded', b'1e3', False, 3)
    add('expanded output fits', b'1e3', True, 4)
    add('zero ceiling', b'0', False, 0)
    accepted = [c for c in cases if c['valid']]
    for c, raw in zip(accepted, oracle_many([unb64(c['raw_base64']) for c in accepted])):
        c.update(canonical_base64=b64(raw), digest=digest(raw))
    (FIXTURES / 'canonicalization.json').write_text(json.dumps(cases, indent=2) + '\n')

    source = next(c for c in json.loads((FIXTURES / 'engine-inputs.json').read_text()) if c['name'] == 'bound launch bytes')
    context = json.loads(unb64(source['context_base64']))
    context['contract']['catalog_digest'] = object_digest(json.loads((ROOT / 'schemas/catalog.json').read_text()))
    context['contract']['operations_digest'] = object_digest(json.loads((ROOT / 'schemas/operations.json').read_text()))
    skill = next(c['document'] for c in json.loads((FIXTURES / 'startup-manifests.json').read_text()) if c['name'] == 'complete skill inventory')
    skill_raw = enc(skill)
    set_ = context['skills']
    set_['skills'] = [{'skill_id': skill['skill_id'], 'bundle_digest': 'sha256:' + '0' * 64,
                       'manifest': {'slot': 0, 'schema_id': 'urn:operator:schema:skill-manifest:v1alpha1', **descriptor(skill_raw), 'object_digest': object_digest(skill)}}]
    set_['loading_digest'] = object_digest({key: value for key, value in set_.items() if key != 'loading_digest'})
    context['target']['capability_projection_digest'] = object_digest(context['target']['capabilities'])
    identity_cases = []

    def launch(name, mutate=lambda c: None, alter=lambda data: None, valid=False):
        c = deepcopy(context)
        mutate(c)
        if name != 'wrong skill loading digest':
            c['skills']['loading_digest'] = object_digest({key: value for key, value in c['skills'].items() if key != 'loading_digest'})
        data = deepcopy(source)
        data.update(name=name, mode='launch', valid=valid)
        cr, sr = enc(c), enc(c['skills'])
        tree = json.loads(unb64(data['tree_base64']))
        for entry in tree['entries']:
            if entry['role'] == 'engine-context': entry.update(descriptor(cr))
        tr = enc(tree)
        data.update(context_base64=b64(cr), skill_set_base64=b64(sr), tree_base64=b64(tr), skills_base64=[b64(skill_raw)])
        messages = data['messages']
        for index in [0, 1, 3]: messages[index]['body']['contract'] = deepcopy(c['contract'])
        for index in [2, 3]:
            body = messages[index]['body']
            body['input_tree'].update(**descriptor(tr), object_digest=object_digest(tree))
            body['skill_set'].update(**descriptor(sr), object_digest=object_digest(c['skills']))
            body['engine_context_object_digest'] = object_digest(c)
        for index in [2, 3, 4]:
            messages[index]['body']['binding'].update(input_tree_digest=digest(tr), engine_context_digest=digest(cr), skill_set_digest=digest(sr))
        alter(data)
        identity_cases.append(data)

    bad = 'sha256:' + 'f' * 64
    launch('fully bound launch identities', valid=True)
    launch('wrong skill object digest', lambda c: c['skills']['skills'][0]['manifest'].update(object_digest=bad))
    launch('wrong skill loading digest', lambda c: c['skills'].update(loading_digest=bad))
    launch('wrong capability projection digest', lambda c: c['target'].update(capability_projection_digest=bad))
    launch('wrong installed catalog digest', lambda c: c['contract'].update(catalog_digest=bad))
    launch('wrong installed operation digest', lambda c: c['contract'].update(operations_digest=bad))
    for key in ['input_tree', 'skill_set']:
        def alter(data, key=key):
            for index in [2, 3]: data['messages'][index]['body'][key]['object_digest'] = bad
        launch('wrong ' + key + ' object digest', alter=alter)
    def alter_context(data):
        for index in [2, 3]: data['messages'][index]['body']['engine_context_object_digest'] = bad
    launch('wrong context object digest', alter=alter_context)

    wire_cases = json.loads((FIXTURES / 'ordinary-protocol.json').read_text())
    begin = next(c['request'] for c in wire_cases if c.get('request', {}).get('operation') == 'engine.artifact_begin')
    def artifact(name, raw, valid=True, canonicalization='jcs-v1', mutation=lambda r: None):
        request = deepcopy(begin)
        request['body']['purpose'] = 'supporting-data'
        request['body']['artifact'] = {**descriptor(raw), 'media_type': 'application/json', 'canonicalization': canonicalization}
        mutation(request)
        identity_cases.append({'name': name, 'mode': 'artifact', 'valid': valid, 'request': request, 'content_base64': b64(raw)})
    artifact('canonical artifact bytes', b'{"a":1,"z":2}')
    artifact('noncanonical object order', b'{"z":2,"a":1}', False)
    artifact('noncanonical whitespace', b'{ "a":1}', False)
    artifact('noncanonical numeric spelling', b'{"a":1.0}', False)
    artifact('noncanonical escaped text', b'"\\u0061"', False)
    artifact('canonical raw digest mismatch', b'{}', False, mutation=lambda r:r['body']['artifact'].update(digest=bad))
    artifact('canonical byte count mismatch', b'{}', False, mutation=lambda r:r['body']['artifact'].update(size_bytes=1))
    artifact('canonical invalid JSON', b'{', False)
    artifact('raw noncanonical JSON preserved', b'{ "z":2,"a":1}', canonicalization='raw')
    artifact('raw binary bytes', b'\xff\x00', canonicalization='raw', mutation=lambda r:r['body']['artifact'].update(media_type='application/octet-stream'))
    artifact('raw empty bytes', b'', canonicalization='raw')
    artifact('canonical empty invalid', b'', False)
    (FIXTURES / 'identity-validation.json').write_text(json.dumps(identity_cases, indent=2) + '\n')
    print(f'{len(cases)} canonicalization cases; {len(identity_cases)} identity cases')


if __name__ == '__main__':
    main()
