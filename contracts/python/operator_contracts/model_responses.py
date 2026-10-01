"""Stateless Responses history, encrypted reasoning and client function results."""
from .startup import require
from .validation import ORDINARY_LIMIT, ContractError
from .canonical import _canonical_value, _object_digest
from .schema_ids import ENGINE_MODEL_GENERATE_RESULT_SCHEMA
CODEC='openai-responses-text-tools-v1'
POLICY_FIELDS=('max_output_tokens','reasoning','response_models')


def calls(items):return [item for item in items if item.get('type')=='function_call']
def refusal(items):return any(c['type']=='refusal' for item in items if item.get('role')=='assistant' for c in item['content'])
def equal(left,right):return _object_digest(left,ORDINARY_LIMIT)==_object_digest(right,ORDINARY_LIMIT)


def check_tools(tools):
    names=[t['name'] for t in tools];require(len(names)==len(set(names)))


def check_items(items,complete):
    ids,call_ids=set(),set()
    for item in items:
        if item.get('id') is not None:
            require(item['id'] not in ids);ids.add(item['id'])
        require(not complete or item.get('status') in (None,'completed'))
        require(not complete or item.get('type')!='reasoning' or item.get('encrypted_content') is not None)
        if item.get('type')=='function_call':
            require(item['call_id'] not in call_ids);call_ids.add(item['call_id'])


def check_request(request):
    check_tools(request['tools']);items=request['input'];check_items(items,True)
    require(request['tools'] or request['tool_choice']!='required')
    require(items[0].get('role')=='user')
    pending=[];consuming=False
    for item in items:
        if item.get('type')=='function_call_output':
            require(pending and item['call_id']==pending[0]['call_id'] and equal(item.get('caller'),pending[0].get('caller')))
            pending.pop(0);consuming=True;continue
        require(not (consuming or item.get('role')=='user') or not pending)
        consuming=False
        if item.get('type')=='function_call':pending.append(item)
    require(not pending and (items[-1].get('role')=='user' or items[-1].get('type')=='function_call_output'))


def metrics(response):
    result=dict(known=False,input_tokens=0,output_tokens=0,total_tokens=0,tool_calls=len(calls(response['output'])))
    usage=response.get('usage')
    if usage is not None:result.update(known=True,input_tokens=usage['input_tokens'],output_tokens=usage['output_tokens'],total_tokens=usage['total_tokens'])
    return result


def check_response(response):
    complete=response['status']=='completed';check_items(response['output'],complete)
    require(not refusal(response['output']) or not calls(response['output']))
    require(not complete or (response.get('error') is None and response.get('incomplete_details') is None))
    require((response['status']=='failed')==(response.get('error') is not None))
    require((response['status']=='incomplete')==(response.get('incomplete_details') is not None))
    usage=response.get('usage')
    if usage is not None:
        require(usage['input_tokens']+usage['output_tokens']==usage['total_tokens'])
        detail=usage.get('input_tokens_details') or {}
        require(detail.get('cached_tokens',0)<=usage['input_tokens'] and detail.get('cache_write_tokens',0)<=usage['input_tokens'])
        require((usage.get('output_tokens_details') or {}).get('reasoning_tokens',0)<=usage['output_tokens'])


def check_policy(policy,request):
    check_request(request);check_tools(policy['tools'])
    require(request['model']==policy['request_model'] and request['instructions']==policy['prompt']
        and len(policy['prompt'].encode('utf-8'))<=131072 and request['max_output_tokens']<=policy['max_output_tokens']
        and equal(request['reasoning'],policy['reasoning']) and equal(request['tools'],policy['tools']))


def check_correlation(request,response):
    check_request(request);check_response(response);counts=metrics(response)
    require(not counts['known'] or counts['output_tokens']<=request['max_output_tokens'])
    require(request['tool_choice']!='none' or not counts['tool_calls'])
    for key in ('instructions','max_output_tokens','tool_choice'):
        if response.get(key) is not None:require(equal(response[key],request[key]))
    if 'tools' in response:
        # Ignore only the response-only null default; retain the native objects.
        echo=[{k:v for k,v in tool.items() if k!='output_schema' or v is not None}
              for tool in response['tools']]
        require(equal(echo,request['tools']))
    if isinstance(request['reasoning'],dict) and isinstance(response.get('reasoning'),dict):
        for key,value in request['reasoning'].items():
            if value is not None:require(equal(value,response['reasoning'].get(key)))
    ids={item['id'] for item in request['input'] if item.get('id') is not None}
    call_ids={item['call_id'] for item in request['input'] if item.get('type')=='function_call'}
    for item in response['output']:
        require(item.get('id') not in ids)
        require(item['type']!='function_call' or item['call_id'] not in call_ids)


def disposition(response):
    if response.get('usage') is None:return 'usage-unknown'
    if response['status']!='completed':
        return 'filtered' if (response.get('incomplete_details') or {}).get('reason')=='content_filter' else 'truncated'
    if refusal(response['output']):return 'refusal'
    if calls(response['output']):return 'tool-calls'
    return 'text'


def continuation(protocol,result_raw,results):
    body=protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    require(body['codec_id']==CODEC);response=body['response'];check_response(response)
    require(disposition(response) in ('text','tool-calls','refusal'))
    current=calls(response['output']);require(type(results) is list and len(results)==len(current))
    segment=list(response['output'])
    for call,result in zip(current,results):
        require(type(result) is dict and result.keys()=={'tool_call_id','content'} and result['tool_call_id']==call['call_id']
                and type(result['content']) is str and len(result['content'])<=1048576)
        item=dict(type='function_call_output',call_id=result['tool_call_id'],output=result['content'])
        if 'caller' in call:item['caller']=call['caller']
        segment.append(item)
    try:return _canonical_value(segment,ORDINARY_LIMIT)
    except (UnicodeError,TypeError,ValueError):raise ContractError('contract message consistency check failed') from None
