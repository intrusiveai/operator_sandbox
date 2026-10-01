"""Pinned native Chat Completions subset; no provider I/O or dispatch authority."""
from .model_azure import filtered as azure_filtered
from .validation import ORDINARY_LIMIT, ContractError, decode
from .startup import require
from .canonical import _canonical_value, _object_digest, raw_digest
from .schema_ids import MODEL_CODEC_POLICY_SCHEMA, ENGINE_MODEL_GENERATE_REQUEST_SCHEMA, ENGINE_MODEL_GENERATE_RESULT_SCHEMA
from . import model_anthropic as anthropic
from . import model_bedrock as bedrock

from . import model_gemini as gemini

from . import model_responses as responses

NATIVE_CODECS={responses.CODEC:responses,anthropic.CODEC:anthropic,bedrock.CODEC:bedrock,gemini.CODEC:gemini}


def model_policy_from_context(protocol, context_raw, tools_raw, prompt_raw):
    context = protocol.validate_engine_context(context_raw)
    model = context['model']
    require((model['codec_id']=='openai-chat-text-tools-v1' or model['codec_id'] in NATIVE_CODECS) and 'engine.model_generate' in context['operations'])
    require(type(prompt_raw) is bytes and len(prompt_raw)<=131072)
    try:
        prompt=prompt_raw.decode('utf-8')
    except UnicodeError:
        raise ContractError('contract message consistency check failed') from None
    effective=context['prompt']['provenance']['effective']
    require(effective['digest']==raw_digest(prompt_raw) and effective['size_bytes']==len(prompt_raw))
    tools=decode(tools_raw,ORDINARY_LIMIT)
    settings=model['codec_settings']
    require(settings['tools_digest']==_object_digest(tools,ORDINARY_LIMIT))
    policy={key:model[key] for key in ('codec_id','profile_id','profile_digest')}
    policy.update(request_model=model['model_id'],prompt=prompt,tools=tools)
    codec=NATIVE_CODECS.get(model['codec_id'])
    keys=codec.POLICY_FIELDS if codec else ('instruction_role','max_completion_tokens','response_models')
    policy.update({key:settings[key] for key in keys})
    protocol._catalog.validate_value(MODEL_CODEC_POLICY_SCHEMA,policy)
    (codec.check_tools if codec else check_tools)(tools)
    return _canonical_value(policy,ORDINARY_LIMIT)


def calls(message):
    return message.get('tool_calls') or []


def check_tools(tools):
    names = [item['function']['name'] for item in tools]
    require(len(names)==len(set(names)))


def check_request(request):
    check_tools(request['tools'])
    require(request['tools'] or request['tool_choice']!='required')
    seen, pending = set(), []
    for index, message in enumerate(request['messages']):
        role = message['role']
        require((role in ('system','developer')) if index==0 else (role not in ('system','developer')))
        require(index!=1 or role=='user')
        if role=='tool':
            require(pending and message['tool_call_id']==pending[0])
            pending.pop(0)
            continue
        require(not pending)
        current = calls(message)
        require(not current or message.get('refusal') is None)
        for call in current:
            require(call['id'] not in seen)
            seen.add(call['id']); pending.append(call['id'])
    require(not pending)


def check_response(response):
    choice = response['choices'][0]
    message, finish = choice['message'], choice['finish_reason']
    current = calls(message)
    ids = [call['id'] for call in current]
    require(len(ids)==len(set(ids)))
    require(finish!='tool_calls' or (current and message.get('refusal') is None))
    require(finish!='stop' or not current)
    usage = response.get('usage')
    if usage is not None:
        require(usage['prompt_tokens']+usage['completion_tokens']==usage['total_tokens'])
        for group, name, maximum in [('prompt_tokens_details','cached_tokens',usage['prompt_tokens']),
                                     ('completion_tokens_details','reasoning_tokens',usage['completion_tokens'])]:
            detail = usage.get(group)
            require(detail is None or detail.get(name,0)<=maximum)


def check_correlation(body, result):
    require(all(body[key]==result[key] for key in ('codec_id','profile_id','profile_digest')))
    request, response = body['request'], result['response']
    codec=NATIVE_CODECS.get(body['codec_id'])
    if codec:return codec.check_correlation(request,response)
    check_request(request); check_response(response)
    usage = response.get('usage')
    require(usage is None or usage['completion_tokens']<=request['max_completion_tokens'])
    seen = {call['id'] for message in request['messages'] for call in calls(message)}
    current = calls(response['choices'][0]['message'])
    require(request['tool_choice']!='none' or not current)
    require(all(call['id'] not in seen for call in current))


def validate_model_request(protocol, policy_raw, request_raw):
    policy = protocol._catalog.validate(MODEL_CODEC_POLICY_SCHEMA,policy_raw)
    body = protocol._catalog.validate(ENGINE_MODEL_GENERATE_REQUEST_SCHEMA,request_raw)
    require(all(body[key]==policy[key] for key in ('codec_id','profile_id','profile_digest')))
    request = body['request']
    codec=NATIVE_CODECS.get(body['codec_id'])
    if codec:
        codec.check_policy(policy,request)
        return body
    check_request(request); check_tools(policy['tools'])
    first = request['messages'][0]
    require(request['model']==policy['request_model']
            and request['max_completion_tokens']<=policy['max_completion_tokens']
            and first['role']==policy['instruction_role'] and first['content']==policy['prompt']
            and len(policy['prompt'].encode('utf-8'))<=131072
            and _object_digest(request['tools'],ORDINARY_LIMIT)==_object_digest(policy['tools'],ORDINARY_LIMIT))
    return body


def validate_model_exchange(protocol, policy_raw, request_raw, result_raw):
    body = validate_model_request(protocol,policy_raw,request_raw)
    result = protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    check_correlation(body,result)
    policy = protocol._catalog.validate(MODEL_CODEC_POLICY_SCHEMA,policy_raw)
    if 'response_models' in policy:
        if body['codec_id']==gemini.CODEC:
            model=result['response'].get('modelVersion')
            require((model is None and gemini.blocked(result['response'])) or model in policy['response_models'])
        else:require(result['response']['model'] in policy['response_models'])
    return result


def disposition(response):
    if response.get('usage') is None: return 'usage-unknown'
    if azure_filtered(response):return 'filtered'
    choice = response['choices'][0]
    if choice['finish_reason']=='length': return 'truncated'
    if choice['finish_reason']=='content_filter': return 'filtered'
    if choice['message'].get('refusal') is not None: return 'refusal'
    if choice['finish_reason']=='tool_calls': return 'tool-calls'
    return 'text'


def model_disposition(protocol, result_raw):
    result = protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    codec=NATIVE_CODECS.get(result['codec_id'])
    if codec:
        codec.check_response(result['response'])
        return codec.disposition(result['response'])
    check_response(result['response'])
    return disposition(result['response'])


def chat_continuation(protocol, result_raw, results):
    """One complete assistant/tool segment; call only after bound receipt validation.

    results contains {tool_call_id, content} per original call, including explicit
    invalid or not-executed local results. Never parses native argument strings.
    """
    result = protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    require(result['codec_id']=='openai-chat-text-tools-v1')
    response = result['response']
    check_response(response)
    require(disposition(response) in ('tool-calls','text','refusal'))
    message = response['choices'][0]['message']
    assistant = {key:message[key] for key in ('role','content','refusal','tool_calls')
                 if key in message and not (key=='tool_calls' and message[key] is None)}
    current = calls(message)
    require(type(results) is list and len(results)==len(current))
    segment = [assistant]
    for call, item in zip(current,results):
        require(type(item) is dict and item.keys()=={'tool_call_id','content'}
                and item['tool_call_id']==call['id'] and type(item['content']) is str
                and len(item['content'])<=1048576)
        segment.append(dict(role='tool',**item))
    try:
        return _canonical_value(segment,ORDINARY_LIMIT)
    except (UnicodeError, TypeError, ValueError):
        raise ContractError('contract message consistency check failed') from None


def native_request(body):
    codec=NATIVE_CODECS.get(body['codec_id'])
    (codec.check_request if codec else check_request)(body['request'])


def native_response(body):
    codec=NATIVE_CODECS.get(body['codec_id'])
    (codec.check_response if codec else check_response)(body['response'])


def model_output_limit(protocol,raw):
    body=protocol._catalog.validate(ENGINE_MODEL_GENERATE_REQUEST_SCHEMA,raw)
    native_request(body)
    if body['codec_id']==responses.CODEC:return body['request']['max_output_tokens']
    if body['codec_id']==gemini.CODEC:return body['request']['generationConfig']['maxOutputTokens']
    if body['codec_id']==bedrock.CODEC:return body['request']['inferenceConfig']['maxTokens']
    return body['request']['max_tokens' if body['codec_id']==anthropic.CODEC else 'max_completion_tokens']


def model_usage(protocol,raw):
    body=protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,raw)
    native_response(body)
    response=body['response']
    codec=NATIVE_CODECS.get(body['codec_id'])
    if codec:return codec.metrics(response)
    result=dict(known=False,input_tokens=0,output_tokens=0,total_tokens=0,tool_calls=len(calls(response['choices'][0]['message'])))
    usage=response.get('usage')
    if usage is not None:
        result.update(known=True,input_tokens=usage['prompt_tokens'],output_tokens=usage['completion_tokens'],total_tokens=usage['total_tokens'])
    return result
