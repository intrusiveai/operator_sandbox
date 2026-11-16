"""Provider-independent relay validation and host accounting."""
from .validation import ORDINARY_LIMIT, decode
from .startup import require
from .schema_ids import ENGINE_MODEL_GENERATE_REQUEST_SCHEMA, ENGINE_MODEL_GENERATE_RESULT_SCHEMA

def validate_model_request(protocol, request_raw):
    return protocol._catalog.validate(ENGINE_MODEL_GENERATE_REQUEST_SCHEMA, request_raw)

def validate_model_exchange(protocol, model_raw, request_raw, result_raw):
    validate_model_request(protocol, request_raw)
    result = protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA, result_raw)
    limits = decode(model_raw, ORDINARY_LIMIT)['context_limits']
    check_context_limits(limits)
    b = result['context_budget']
    require(b['input_tokens'] + b['output_tokens'] == b['total_tokens'])
    require(b['requested_completion_tokens'] == limits['maximum_completion_tokens'])
    require(b['reserved_tokens'] == limits['maximum_prompt_tokens'] + limits['maximum_completion_tokens'])
    require(b['input_tokens'] <= limits['maximum_prompt_tokens'] and b['total_tokens'] <= b['reserved_tokens'])
    require(b['remaining_prompt_tokens'] == limits['maximum_prompt_tokens'] - b['input_tokens'])
    require(b['near_prompt_limit'] == (b['input_tokens'] >= limits['warning_threshold_tokens']))
    return result

def check_context_limits(limits):
    require(type(limits) is dict and all(type(limits.get(k)) is int for k in
        ('maximum_prompt_tokens','maximum_completion_tokens','warning_threshold_tokens')))
    require(0 < limits['warning_threshold_tokens'] <= limits['maximum_prompt_tokens'])
    require(0 < limits['maximum_completion_tokens'] <= 2**53-1-limits['maximum_prompt_tokens'])
