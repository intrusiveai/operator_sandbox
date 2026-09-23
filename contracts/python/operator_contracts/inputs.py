"""Immutable launch inputs and exact prompt composition; no filesystem access."""
import hashlib
from .validation import ContractError
from .startup import require, inventory_paths, check_skill_set, check_admission
from .schema_ids import ENGINE_CONTEXT_SCHEMA, PROMPT_PROVENANCE_SCHEMA, SCENARIO_BUNDLE_SCHEMA

PROMPT_LIMIT = 131072
ENGINE_CONTEXT_LIMIT = 64 << 20
# Unicode White_Space, fixed explicitly rather than Python's broader isspace().
WHITESPACE = frozenset('\t\n\v\f\r \x85\xa0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000')
KINDS = ('target_output', 'operation_error', 'injection_delivery', 'oracle_outcome')
PROFILES = ('black-box', 'diagnostic', 'oracle-assisted')


def descriptor(raw):
    return {'size_bytes': len(raw), 'digest': 'sha256:' + hashlib.sha256(raw).hexdigest()}


def validate_prompt(raw):
    require(0 < len(raw) <= PROMPT_LIMIT)
    try:
        text = raw.decode('utf-8')
    except UnicodeDecodeError:
        raise ContractError('contract message consistency check failed') from None
    require('\0' not in text and any(char not in WHITESPACE for char in text))


def compose_prompt(mode, base, replacement=None, appends=()):
    """Compose frozen sources. Return exact bytes and safe provenance, without paths."""
    require(mode in ('default', 'replacement', 'extension') and len(appends) <= 16)
    validate_prompt(base)
    require((mode == 'replacement') == (replacement is not None))
    require((mode == 'extension') == bool(appends))
    if replacement is not None:
        validate_prompt(replacement)
    for raw in appends:
        validate_prompt(raw)
    selected = [base, *appends] if mode == 'extension' else [replacement if mode == 'replacement' else base]
    require(sum(map(len, selected)) + 2 * (len(selected) - 1) <= PROMPT_LIMIT)
    effective = b'\n\n'.join(selected)
    provenance = {'api_version': 'operator.dev/prompt-provenance/v1alpha1', 'mode': mode,
                  'base': descriptor(base), 'appends': [descriptor(raw) for raw in appends], 'effective': descriptor(effective)}
    if replacement is not None:
        provenance['replacement'] = descriptor(replacement)
    return effective, provenance


def check_provenance(p):
    if p['mode'] in ('default', 'replacement'):
        require(p['effective'] == p['base' if p['mode'] == 'default' else 'replacement'])
    else:
        require(p['effective']['size_bytes'] == p['base']['size_bytes'] + sum(d['size_bytes'] + 2 for d in p['appends']))


def validate_prompt_provenance(protocol, raw):
    p = protocol._catalog.validate(PROMPT_PROVENANCE_SCHEMA, raw, 65536)
    check_provenance(p)
    return p


def validate_engine_context(protocol, raw):
    c = protocol._catalog.validate(ENGINE_CONTEXT_SCHEMA, raw, ENGINE_CONTEXT_LIMIT)
    check_provenance(c['prompt']['provenance'])
    check_admission(protocol, c)
    # Reuse inventory semantics without encoding decoded numeric values again.
    inventory_paths(c['references'])
    ids = {c['scenario_bundle']['entry_id'], c['prompt']['entry_id']}
    require(len(ids) == 2)
    for e in c['references']:
        require(e['entry_id'] not in ids and e['path'] == 'artifacts/sha256-' + e['digest'][7:])
        ids.add(e['entry_id'])
    for key in ('artifact_bindings', 'omissions'):
        names = [item['artifact_id'] for item in c[key]]
        require(names == sorted(set(names)))
    used, bound = set(), set()
    reference_ids = {e['entry_id'] for e in c['references']}
    for a in c['artifact_bindings']:
        require(a['entry_id'] in reference_ids)
        used.add(a['entry_id'])
        bound.add(a['artifact_id'])
    require(used == reference_ids and not bound.intersection(a['artifact_id'] for a in c['omissions']))
    require(sum(e['size_bytes'] for e in c['references']) + c['scenario_bundle']['size_bytes'] + c['prompt']['provenance']['effective']['size_bytes'] + len(raw) <= 64 << 20)
    # Skill metadata semantics are shared with the manifest validator.
    check_skill_set(c['skills'])
    maximum = (1, 3, 4)[PROFILES.index(c['feedback']['profile'])]
    kinds = c['feedback']['allowed_kinds']
    require(kinds == [k for k in KINDS[:maximum] if k in kinds])
    target = c['target']['capabilities']
    for group in ('operations', 'actions', 'services', 'file_namespaces', 'evidence_classes'):
        refs = [item['ref'] for item in target[group]]
        require(refs == sorted(set(refs)))
    for op in target['operations']:
        require(op['ref'] == 'operation:' + op['operation_id'])
    require(c['model']['features'] == ['text', 'function-tools'])
    return c


def validate_launch_content(protocol, messages, tree, skill_set, skills, context, bundle, prompt):
    """Link supplied bytes; staging trust, canonical hashes and admission remain caller work."""
    protocol.validate_startup_inputs(messages, tree, skill_set, skills)
    c = validate_engine_context(protocol, context)
    b = protocol._catalog.validate(SCENARIO_BUNDLE_SCHEMA, bundle, 4 << 20)
    validate_prompt(prompt)
    bootstrap = protocol.validate_control('host', messages[0])
    admission = protocol.validate_control('host', messages[4])['body']
    for key in ('campaign_id', 'launch_id', 'run_revision'):
        require(c[key] == bootstrap[key])
    for key in ('contract', 'release'):
        require(c[key] == bootstrap['body'][key])
    for key in ('operations', 'limits'):
        require(c[key] == admission[key])
    # Startup work consumes time/budgets: admission may narrow the frozen projection.
    require(all(value <= c['remaining_limits'][key] for key, value in admission['remaining_limits'].items()))
    require(c['skills'] == protocol.validate_skill_set(skill_set))
    inventory = protocol.validate_input_tree(tree)['entries']
    core = {e['role']: e for e in inventory if e['role'] != 'reference'}
    for role, raw in (('engine-context', context), ('scenario-bundle', bundle), ('system-prompt', prompt)):
        require(all(core[role][key] == value for key, value in descriptor(raw).items()))
    require(c['scenario_bundle'] == core['scenario-bundle'])
    require(c['prompt']['entry_id'] == core['system-prompt']['entry_id'])
    require(c['prompt']['provenance']['effective'] == descriptor(prompt))
    require(c['references'] == [e for e in inventory if e['role'] == 'reference'])
    require(b['target_requirements']['target_id'] == c['target']['capabilities']['target_id'])
    require(PROFILES.index(c['feedback']['profile']) <= PROFILES.index(b['feedback']['requested_profile']))
    bindings = {a['artifact_id']: a['entry_id'] for a in c['artifact_bindings']}
    omissions = {a['artifact_id']: a['reason'] for a in c['omissions']}
    refs = {e['entry_id']: e for e in c['references']}
    artifacts = b['artifacts']
    for group in ('objectives', 'scenarios'):
        for item in b[group]:
            if item.get('required'):
                require(all(identifier in bindings for identifier in item.get('artifact_refs', [])))
    require(len({a['artifact_id'] for a in artifacts}) == len(artifacts))
    require(set(bindings) | set(omissions) == {a['artifact_id'] for a in artifacts})
    for a in artifacts:
        if a['artifact_id'] in omissions:
            require(not a['required'] and a.get('omission_reason') == omissions[a['artifact_id']])
        else:
            require('omission_reason' not in a)
            entry = refs[bindings[a['artifact_id']]]
            require(all(entry.get(key) == a.get(key) for key in ('digest', 'size_bytes', 'media_type', 'schema_id')))
