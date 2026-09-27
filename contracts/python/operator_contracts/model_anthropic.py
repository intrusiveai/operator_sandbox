"""Native Messages validation and lossless assistant/tool continuation."""
from .startup import require
from .validation import ORDINARY_LIMIT, ContractError
from .canonical import _canonical_value, _object_digest
from .schema_ids import ENGINE_MODEL_GENERATE_RESULT_SCHEMA

CODEC = 'anthropic-messages-text-tools-v1'
POLICY_FIELDS=('max_tokens','thinking','response_models')
MAX = 2**53-1


def calls(content):
    return [v for v in content if v['type']=='tool_use'] if type(content) is list else []


def check_tools(tools):
    names=[t['name'] for t in tools]
    require(len(names)==len(set(names)))


def check_request(request):
    check_tools(request['tools'])
    choice,thinking=request['tool_choice']['type'],request['thinking']
    require(request['tools'] or choice!='any')
    require(thinking['type']=='disabled' or choice!='any')
    require(thinking['type']!='enabled' or thinking['budget_tokens']<request['max_tokens'])
    seen,pending=set(),[]
    for index,message in enumerate(request['messages']):
        require(index!=0 or message['role']=='user')
        if message['role']=='assistant':
            require(not pending)
            for call in calls(message['content']):
                require(call['id'] not in seen)
                seen.add(call['id']); pending.append(call['id'])
            continue
        if type(message['content']) is str:
            require(not pending)
            continue
        for block in message['content']:
            if block['type']=='tool_result':
                require(pending and block['tool_use_id']==pending[0])
                pending.pop(0)
            else:
                require(not pending)
        require(not pending)
    require(not pending and request['messages'][-1]['role']=='user')


def metrics(response):
    result=dict(known=False,input_tokens=0,output_tokens=0,total_tokens=0,tool_calls=len(calls(response['content'])))
    usage=response.get('usage')
    if usage is not None:
        incoming=usage['input_tokens']+(usage.get('cache_creation_input_tokens') or 0)+(usage.get('cache_read_input_tokens') or 0)
        result.update(known=True,input_tokens=incoming,output_tokens=usage['output_tokens'],total_tokens=incoming+usage['output_tokens'])
    return result


def check_response(response):
    current=calls(response['content'])
    ids=[c['id'] for c in current]
    require(len(ids)==len(set(ids)))
    finish=response['stop_reason']
    refused=response.get('stop_details') is not None or finish=='refusal'
    require(finish!='tool_use' or (current and not refused))
    require(not (finish=='end_turn' or refused) or not current)
    usage=response.get('usage'); counts=metrics(response)
    require(counts['total_tokens']<=MAX)
    if usage is not None:
        detail=usage.get('cache_creation')
        if detail is not None:
            require(usage.get('cache_creation_input_tokens') is not None
                    and detail['ephemeral_1h_input_tokens']+detail['ephemeral_5m_input_tokens']==usage['cache_creation_input_tokens'])
        detail=usage.get('output_tokens_details')
        require(detail is None or detail['thinking_tokens']<=counts['output_tokens'])


def check_correlation(request,response):
    check_request(request); check_response(response)
    counts=metrics(response)
    require(not counts['known'] or counts['output_tokens']<=request['max_tokens'])
    require(request['tool_choice']['type']!='none' or not counts['tool_calls'])
    seen={c['id'] for message in request['messages'] for c in calls(message['content'])}
    require(all(c['id'] not in seen for c in calls(response['content'])))


def check_policy(policy,request):
    check_request(request); check_tools(policy['tools'])
    require(request['model']==policy['request_model'] and request['max_tokens']<=policy['max_tokens']
            and request['system']==policy['prompt'] and len(policy['prompt'].encode('utf-8'))<=131072
            and _object_digest(request['tools'],ORDINARY_LIMIT)==_object_digest(policy['tools'],ORDINARY_LIMIT)
            and _object_digest(request['thinking'],ORDINARY_LIMIT)==_object_digest(policy['thinking'],ORDINARY_LIMIT))


def disposition(response):
    if response.get('usage') is None: return 'usage-unknown'
    if response['stop_reason'] in ('max_tokens','pause_turn','model_context_window_exceeded'): return 'truncated'
    if response['stop_reason']=='refusal' or response.get('stop_details') is not None: return 'refusal'
    if response['stop_reason']=='tool_use': return 'tool-calls'
    return 'text'


def continuation(protocol,result_raw,results):
    result=protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    require(result['codec_id']==CODEC)
    response=result['response']; check_response(response)
    require(disposition(response) in ('tool-calls','text','refusal'))
    current=calls(response['content'])
    require(type(results) is list and len(results)==len(current))
    segment=[dict(role='assistant',content=response['content'])]
    blocks=[]
    for call,item in zip(current,results):
        require(type(item) is dict and item.keys()=={'tool_call_id','content'} and item['tool_call_id']==call['id']
                and type(item['content']) is str and len(item['content'])<=1048576)
        blocks.append(dict(type='tool_result',tool_use_id=item['tool_call_id'],content=item['content']))
    if blocks: segment.append(dict(role='user',content=blocks))
    try:
        return _canonical_value(segment,ORDINARY_LIMIT)
    except (UnicodeError,TypeError,ValueError):
        raise ContractError('contract message consistency check failed') from None
