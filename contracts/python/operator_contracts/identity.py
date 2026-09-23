"""Canonical identity checks layered over schema and launch-content validation."""
from .canonical import canonical_digest, canonicalize, raw_digest, _object_digest
from .startup import require
from .inputs import ENGINE_CONTEXT_LIMIT


def validate_launch_identities(protocol, messages, tree, skill_set, skills, context, bundle, prompt):
    protocol.validate_launch_content(messages, tree, skill_set, skills, context, bundle, prompt)
    body = protocol.validate_control('host', messages[2])['body']
    for key, raw in (('input_tree', tree), ('skill_set', skill_set)):
        require(body[key]['object_digest'] == canonical_digest(raw, 8 << 20))
    require(body['engine_context_object_digest'] == canonical_digest(context, ENGINE_CONTEXT_LIMIT))
    selection = protocol.validate_skill_set(skill_set)
    require(selection['loading_digest'] == _object_digest(
        {key: value for key, value in selection.items() if key != 'loading_digest'}, 65536))
    for entry, raw in zip(selection['skills'], skills):
        require(entry['manifest']['object_digest'] == canonical_digest(raw, 2 << 20))
    c = protocol.validate_engine_context(context)
    require(c['target']['capability_projection_digest'] == _object_digest(c['target']['capabilities'], ENGINE_CONTEXT_LIMIT))
    require(all(c['contract'][key] == digest for key, digest in protocol.registry_digests().items()))


def validate_artifact_content(protocol, begin_request, content):
    request = protocol.validate_request(begin_request)
    require(request['operation'] == 'engine.artifact_begin')
    descriptor = request['body']['artifact']
    require(descriptor['size_bytes'] == len(content) and descriptor['digest'] == raw_digest(content))
    if descriptor['canonicalization'] == 'jcs-v1':
        require(canonicalize(content, 16 << 20) == content)
