"""The interface document identity is independent of its implementation package."""
import json
import re
from .validation import decode, ContractError
from .canonical import raw_digest
from .startup import require

DOCUMENT = 'asyncapi.json'
LIMIT = 16 << 20
FORMAT = 'application/schema+json;version=draft-2020-12'


def schema_projection(value, catalog, current):
    if isinstance(value, list):
        return [schema_projection(v, catalog, current) for v in value]
    if not isinstance(value, dict):
        return value
    result = {}
    for key, child in value.items():
        if key == '$id':
            continue
        if key == '$ref':
            require(isinstance(child, str))
            resource, _, fragment = child.partition('#')
            resource = resource or current
            require(resource in catalog)
            child = '#/components/schemas/' + catalog[resource] + '/schema' + fragment
        else:
            child = schema_projection(child, catalog, current)
        result[key] = child
    return result


def document_identity(raw):
    value = decode(raw, LIMIT)
    require(isinstance(value, dict) and value.get('asyncapi') == '3.0.0')
    info = value.get('info')
    require(isinstance(info, dict))
    version = info.get('version')
    require(isinstance(version, str) and len(version) <= 128 and re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version))
    return {'package_version': version, 'package_digest': raw_digest(raw)}


def verify_interface(files, expected):
    raw = files.get(DOCUMENT, b'')
    require(document_identity(raw) == expected)
    document = decode(raw, LIMIT)
    catalog = decode(files['catalog.json'])
    require(document.get('x-operator-catalog') == catalog)
    require(document.get('x-operator-registry') == decode(files['operations.json']))
    components = document.get('components', {}).get('schemas', {})
    require(len(components) == len(catalog))
    for identifier, name in catalog.items():
        schema = decode(files[name], LIMIT)
        require(schema.get('$id') == identifier)
        require(components.get(name) == {'schemaFormat': FORMAT, 'schema': schema_projection(schema, catalog, identifier)})


def build_document(version, files, semantics):
    catalog = decode(files['catalog.json'])
    registry = decode(files['operations.json'])
    components = {name: {'schemaFormat': FORMAT, 'schema': schema_projection(decode(files[name], LIMIT), catalog, identifier)}
                  for identifier, name in sorted(catalog.items())}
    channels, messages, operations = {}, {}, {}
    lanes = {'ordinary-out': ('receive', 'engine-pipe-request.schema.json'),
             'ordinary-in': ('send', 'engine-pipe-response.schema.json'),
             'control-out': ('receive', 'engine-guest-control.schema.json'),
             'control-in': ('send', 'engine-host-control.schema.json')}
    for lane, (action, schema) in lanes.items():
        channels[lane] = {'address': lane, 'messages': {}}
        if lane.startswith('control'):
            messages[lane] = {'payload': {'$ref': '#/components/schemas/' + schema}}
            channels[lane]['messages'][lane] = {'$ref': '#/components/messages/' + lane}
            operations[lane] = {'action': action, 'channel': {'$ref': '#/channels/' + lane},
                                'messages': [{'$ref': '#/channels/' + lane + '/messages/' + lane}]}
    for op in registry['operations']:
        name = op['name']
        for suffix, lane, schema in (('request', 'ordinary-out', 'engine-pipe-request.schema.json'),
                                     ('response', 'ordinary-in', 'engine-pipe-response.schema.json')):
            key = name + '.' + suffix
            messages[key] = {
                'payload': {'schemaFormat': FORMAT, 'schema': {'allOf': [
                    {'$ref': '#/components/schemas/' + schema + '/schema'},
                    {'properties': {'operation': {'const': name}}}]}},
                'correlationId': {'location': '$message.payload#/call_id'}}
            channels[lane]['messages'][key] = {'$ref': '#/components/messages/' + key}
        operations[name] = {'action': 'receive', 'channel': {'$ref': '#/channels/ordinary-out'},
                            'messages': [{'$ref': '#/channels/ordinary-out/messages/' + name + '.request'}],
                            'reply': {'channel': {'$ref': '#/channels/ordinary-in'},
                                      'messages': [{'$ref': '#/channels/ordinary-in/messages/' + name + '.response'}]}}
    return {'asyncapi': '3.0.0', 'info': {'title': 'Operator–Attack Harness interface', 'version': version},
            'defaultContentType': 'application/json', 'channels': channels, 'operations': operations,
            'components': {'schemas': components, 'messages': messages},
            'x-operator-catalog': catalog, 'x-operator-registry': registry,
            'x-operator-semantics': semantics}


def interface_resources(raw, expected):
    """Extract offline validator inputs from approved bytes, not an Operator checkout."""
    require(document_identity(raw) == expected)
    document = decode(raw, LIMIT)
    catalog = document.get('x-operator-catalog', {})
    reverse = {name: identifier for identifier, name in catalog.items()}
    def restore(value):
        if isinstance(value, list):
            return [restore(v) for v in value]
        if not isinstance(value, dict):
            return value
        result = {}
        for key, child in value.items():
            if key == '$ref':
                require(isinstance(child, str) and child.startswith('#/components/schemas/'))
                name, separator, fragment = child.removeprefix('#/components/schemas/').partition('/schema')
                require(separator and name in reverse)
                child = reverse[name] + ('#' + fragment if fragment else '')
            else:
                child = restore(child)
            result[key] = child
        return result
    def encode(value):
        return json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode()
    files = {DOCUMENT: raw, 'catalog.json': encode(catalog), 'operations.json': encode(document.get('x-operator-registry'))}
    schemas = document.get('components', {}).get('schemas', {})
    for name, identifier in reverse.items():
        component = schemas.get(name, {})
        require(component.get('schemaFormat') == FORMAT)
        value = restore(component.get('schema'))
        require(isinstance(value, dict))
        value['$id'] = identifier
        files[name] = encode(value)
    verify_interface(files, expected)
    return files
