"""Frozen host sizing policy; transport ceilings remain structural validation."""
import base64
from .canonical import _canonical_value
from .validation import ContractError, ORDINARY_LIMIT
from .simple import resolve_direct_input

CEILINGS = dict(direct_input_bytes=1<<20, assessment_bytes=1<<20,
                artifact_bytes=16<<20, artifact_part_bytes=1<<20,
                observation_chunk_bytes=1<<20, snapshot_page_items=100)

def validate_sizes(sizes):
    if (type(sizes) is not dict or set(sizes) != set(CEILINGS)
            or any(type(n) is not int or not 0 < n <= CEILINGS[k] for k,n in sizes.items())):
        raise ContractError('Invalid host size limits.')

def validate_request_sizes(protocol, raw, limits):
    validate_sizes(limits)
    q = protocol.validate_request(raw)
    b, op = q['body'], q['operation']
    def check(path, name, actual):
        if actual > limits[name]:
            message = f'Size {actual} exceeds configured {name}={limits[name]}. Reduce the requested size; for direct input use a committed artifact receipt. Execution did not occur; correction is safe.'
            raise ContractError(message, reason='configured_size_exceeded', field_path=path, correction=message)
    if op == 'engine.attempt_execute':
        for field in ('input','injection_input'):
            if field in b:
                x = resolve_direct_input(protocol,b[field])
                if 'artifact_receipt' not in x: check('/'+field,'direct_input_bytes',len(x['content']))
    elif op == 'engine.artifact_begin':
        check('/artifact/size_bytes','artifact_bytes',b['artifact']['size_bytes'])
        if b['purpose']=='conclusion': check('/artifact/size_bytes','assessment_bytes',b['artifact']['size_bytes'])
    elif op == 'engine.artifact_put_part': check('/content','artifact_part_bytes',len(base64.b64decode(b['content'])))
    elif op == 'engine.observation_read': check('/max_bytes','observation_chunk_bytes',b['max_bytes'])
    elif op == 'engine.snapshot_list' and 'limit' in b: check('/limit','snapshot_page_items',b['limit'])
    elif op == 'engine.finish' and 'assessment' in b:
        check('/assessment','assessment_bytes',len(_canonical_value(b['assessment'],ORDINARY_LIMIT)))
