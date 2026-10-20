"""Bounded assessment validation with safe, deterministic rule/path diagnostics."""
from .validation import ContractError, MAX_SAFE_INTEGER
from .schema_ids import ENGINE_CONCLUSION_SCHEMA

CONCLUSION_LIMIT = 1 << 20


def _issue(reason, path, correction):
    raise ContractError('Assessment validation failed: '+reason, reason=reason,
                        field_path=path, correction=correction)


def _ranges(value, path=''):
    if isinstance(value, dict):
        if 'range' in value:
            span = value['range']
            if span['offset'] + span['length'] > MAX_SAFE_INTEGER:
                _issue('invalid_range', path+'/range', 'Use an offset and length whose sum is JSON-safe.')
        for key in sorted(value): _ranges(value[key], path+'/'+key)
    elif isinstance(value, list):
        for i, child in enumerate(value): _ranges(child, path+'/'+str(i))


def _hypothesis(value, path=''):
    if value['provenance'].get('parent_hypothesis_id') == value['hypothesis_id']:
        _issue('self_parent', path+'/provenance/parent_hypothesis_id', 'A hypothesis cannot be its own parent.')


def _observations_linked(value, path=''):
    known = set(value['attempt_receipt_refs'])
    if any(item['attempt_receipt_id'] not in known for item in value['observation_refs']):
        _issue('observation_attempt_mismatch', path+'/observation_refs', 'Include each observation\'s execution receipt in attempt_receipt_refs.')


def check_record(body):
    record = body['record']
    _ranges(record, '/record')
    if body['record_kind'] == 'hypothesis': _hypothesis(record, '/record')
    elif body['record_kind'] == 'progress': _observations_linked(record, '/record')
    elif body['record_kind'] == 'lineage' and record.get('parent_attempt_receipt_id') == record['attempt_receipt_id']:
        _issue('self_parent', '/record/parent_attempt_receipt_id', 'An attempt cannot be its own parent.')


def validate_conclusion(protocol, raw):
    """External references may name history; only runtime/host state resolves it."""
    c = protocol._catalog.validate(ENGINE_CONCLUSION_SCHEMA, raw, CONCLUSION_LIMIT)
    _ranges(c)
    parents = {}
    for i, h in enumerate(c['hypotheses']):
        path = '/hypotheses/'+str(i)
        identifier = h['hypothesis_id']
        if identifier in parents:
            _issue('duplicate_hypothesis', path+'/hypothesis_id', 'Define each local hypothesis ID once.')
        _hypothesis(h, path)
        parents[identifier] = h['provenance'].get('parent_hypothesis_id')
    for i, h in enumerate(c['hypotheses']):
        identifier, seen = h['hypothesis_id'], set()
        while identifier is not None:
            if identifier in seen:
                _issue('hypothesis_cycle', '/hypotheses/'+str(i)+'/provenance/parent_hypothesis_id', 'Remove the cycle in hypothesis parents.')
            seen.add(identifier)
            identifier = parents.get(identifier)
    for field, identifier in (('objectives','objective_id'), ('claims','claim_id'), ('uncertainties','gap_id')):
        ids = [e[identifier] for e in c[field]]
        if len(ids) != len(set(ids)): _issue('duplicate_entry', '/'+field, 'Use distinct identifiers.')
    gaps = {g['gap_id'] for g in c['uncertainties']}
    for i, g in enumerate(c['uncertainties']):
        if g['kind'] == 'coverage-limit' and c['status'] == 'completed':
            _issue('coverage_status', '/uncertainties/'+str(i)+'/kind', 'Report partial coverage when coverage limits remain.')
    for i, claim in enumerate(c['claims']):
        path = '/claims/'+str(i)
        _observations_linked(claim, path)
        if not set(claim['uncertainty_refs']) <= gaps:
            _issue('unresolved_uncertainty', path+'/uncertainty_refs', 'Reference an uncertainty defined in this assessment.')
    return c
