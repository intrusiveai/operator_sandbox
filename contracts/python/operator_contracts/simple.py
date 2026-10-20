"""Shared simple workflow semantics; host ownership/receipt checks are separate."""
import base64
import binascii
from .validation import ContractError, CONTROL_LIMIT
from .canonical import _canonical_value
from .schema_ids import EXECUTION_INPUT_SCHEMA, ASSESSMENT_SCHEMA, CONCLUSION_BINDING_SCHEMA
from .assessment import validate_conclusion, CONCLUSION_LIMIT

DIRECT_INPUT_LIMIT = 32768
DIRECT_INPUT_DEPTH = 16


def _depth(value):
    if isinstance(value, dict): return 1 + max((_depth(v) for v in value.values()), default=0)
    if isinstance(value, list): return 1 + max((_depth(v) for v in value), default=0)
    return 0


def resolve_direct_input(protocol, value):
    protocol._catalog.validate_value(EXECUTION_INPUT_SCHEMA, value)
    if 'artifact_receipt' in value: return dict(artifact_receipt=value['artifact_receipt'])
    encoding = value['encoding']
    canonicalization = 'raw'
    if encoding == 'json':
        if _depth(value['value']) > DIRECT_INPUT_DEPTH:
            raise ContractError('Use JSON with at most 16 nested containers or publish an artifact.', reason='input_depth_exceeded', field_path='/value', correction='Reduce nesting or publish an artifact.')
        content = _canonical_value(value['value'], 4 << 20)
        media_type, canonicalization = 'application/json', 'jcs-v1'
    elif encoding == 'utf8':
        content, media_type = value['text'].encode('utf-8'), value.get('media_type', 'text/plain')
    else:
        try:
            content = base64.b64decode(value['data'], validate=True)
            if base64.b64encode(content).decode('ascii') != value['data']: raise ValueError()
        except (ValueError, binascii.Error):
            raise ContractError('Supply canonical padded base64 or use JSON/UTF-8 input.', reason='invalid_base64', field_path='/data', correction='Supply canonical padded base64 or use JSON/UTF-8 input.') from None
        media_type = value['media_type']
    if len(content) > DIRECT_INPUT_LIMIT:
        raise ContractError('Direct input is limited to 32768 bytes.', reason='direct_input_too_large', correction='Publish this input as an artifact and pass its returned artifact_receipt.')
    return dict(content=content, media_type=media_type, canonicalization=canonicalization)


def check_attempt(protocol, body):
    for key in ('input', 'injection_input'):
        if key in body:
            try: resolve_direct_input(protocol, body[key])
            except ContractError as e:
                raise ContractError(str(e), reason=e.reason, field_path='/'+key+e.field_path, correction=e.correction) from None


def normalize_assessment(protocol, raw, binding, finish):
    c = protocol._catalog.validate(ASSESSMENT_SCHEMA, raw, CONCLUSION_LIMIT)
    b = protocol._catalog.validate(CONCLUSION_BINDING_SCHEMA, binding, CONTROL_LIMIT)
    for key in ('objectives','hypotheses','claims','coverage','uncertainties','record_refs'): c.setdefault(key, [])
    collections = dict(objectives=('attempt_receipt_refs','record_refs'), hypotheses=('objective_refs','attempt_receipt_refs','record_refs'), claims=('attempt_receipt_refs','observation_refs','uncertainty_refs'), coverage=('attempt_receipt_refs',), uncertainties=('observation_refs','input_refs','record_refs'))
    for field, keys in collections.items():
        for entry in c[field]:
            for key in keys: entry.setdefault(key, [])
    c.setdefault('status', 'partial')
    c.update(api_version='operator.dev/engine-conclusion/v1alpha2', kind='EngineConclusion', binding=b, finish_reason=finish)
    c = validate_conclusion(protocol, _canonical_value(c, CONCLUSION_LIMIT))
    for field in ('objectives', 'hypotheses'):
        for i, entry in enumerate(c[field]):
            if entry['outcome'] == 'supported' and not entry['attempt_receipt_refs']:
                raise ContractError('Supported outcomes require execution evidence.', reason='evidence_required', field_path='/'+field+'/'+str(i)+'/attempt_receipt_refs', correction='Use real execution receipts or report an untested/inconclusive outcome.')
    return c


def validate_attempt_origin(body, scenarios=None):
    origin = body.get('origin')
    reason = path = correction = None
    if origin not in ('scenario', 'exploratory'):
        reason, path, correction = 'origin_required', '/origin', 'Set origin to scenario with a submitted scenario_id, or exploratory without scenario_id.'
    elif origin == 'exploratory' and 'scenario_id' in body:
        reason, path, correction = 'scenario_forbidden', '/scenario_id', 'Exploratory origin forbids scenario_id. Remove it or select scenario origin.'
    elif origin == 'scenario':
        if not isinstance(body.get('scenario_id'), str) or not body['scenario_id']:
            reason, path, correction = 'scenario_required', '/scenario_id', 'Scenario origin requires a scenario_id from the submitted bundle.'
        elif scenarios is not None and body['scenario_id'] not in scenarios:
            reason, path, correction = 'unknown_scenario', '/scenario_id', 'Select a scenario_id from the submitted bundle, or exploratory origin without scenario_id.'
    if reason:
        correction += ' Execution did not occur; correction is safe with a new operation ID.'
        raise ContractError(correction, reason=reason, field_path=path, correction=correction)
