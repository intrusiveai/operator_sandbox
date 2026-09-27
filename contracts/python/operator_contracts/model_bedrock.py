"""Converse wire semantics; model routing is independently pinned by the host."""
import base64
import binascii
from .startup import require
from .validation import ORDINARY_LIMIT, ContractError
from .canonical import _canonical_value, _object_digest
from .schema_ids import ENGINE_MODEL_GENERATE_RESULT_SCHEMA

CODEC='bedrock-converse-text-tools-v1'
POLICY_FIELDS=('max_tokens',)


def calls(content):
    return [v['toolUse'] for v in content if 'toolUse' in v]


def check_tools(tools):
    names=[v['toolSpec']['name'] for v in tools]
    require(len(names)==len(set(names)))


def check_content(content):
    for block in content:
        encoded=block.get('reasoningContent',{}).get('redactedContent')
        if encoded is not None:
            try:
                raw=base64.b64decode(encoded,validate=True)
                require(base64.b64encode(raw).decode('ascii')==encoded)
            except (ValueError,binascii.Error):
                raise ContractError('contract message consistency check failed') from None


def check_request(request):
    config=request.get('toolConfig')
    if config is not None:check_tools(config['tools'])
    seen,pending=set(),[]
    for index,message in enumerate(request['messages']):
        require(index!=0 or message['role']=='user')
        check_content(message['content'])
        if message['role']=='assistant':
            require(not pending)
            for call in calls(message['content']):
                require(config is not None and call['toolUseId'] not in seen)
                seen.add(call['toolUseId']);pending.append(call['toolUseId'])
            continue
        for block in message['content']:
            if 'toolResult' in block:
                require(pending and block['toolResult']['toolUseId']==pending[0]);pending.pop(0)
            else: require(not pending)
        require(not pending)
    require(not pending and request['messages'][-1]['role']=='user')


def message(response):return response['output']['message']


def metrics(response):
    result=dict(known=False,input_tokens=0,output_tokens=0,total_tokens=0,tool_calls=len(calls(message(response)['content'])))
    usage=response.get('usage')
    if usage is not None:
        incoming=usage['inputTokens']+usage.get('cacheReadInputTokens',0)+usage.get('cacheWriteInputTokens',0)
        result.update(known=True,input_tokens=incoming,output_tokens=usage['outputTokens'],total_tokens=incoming+usage['outputTokens'])
    return result


def check_response(response):
    content=message(response)['content'];check_content(content)
    current=calls(content);ids=[c['toolUseId'] for c in current]
    require(len(ids)==len(set(ids)))
    finish=response['stopReason']
    require(finish!='tool_use' or current)
    require(finish not in ('end_turn','stop_sequence') or not current)
    counts=metrics(response);require(counts['total_tokens']<=2**53-1)
    usage=response.get('usage')
    if usage is not None:
        require(usage['totalTokens'] in (counts['total_tokens'],usage['inputTokens']+usage['outputTokens']))
        if 'cacheDetails' in usage:
            details=usage['cacheDetails'];ttls=[d['ttl'] for d in details]
            require(ttls==sorted(set(ttls)) and sum(d['inputTokens'] for d in details)==usage.get('cacheWriteInputTokens',0))


def check_policy(policy,request):
    check_request(request);check_tools(policy['tools'])
    require(request['system'][0]['text']==policy['prompt'] and len(policy['prompt'].encode('utf-8'))<=131072
            and request['inferenceConfig']['maxTokens']<=policy['max_tokens'])
    if 'toolConfig' in request:
        require(_object_digest(request['toolConfig']['tools'],ORDINARY_LIMIT)==_object_digest(policy['tools'],ORDINARY_LIMIT))


def check_correlation(request,response):
    check_request(request);check_response(response);counts=metrics(response)
    require(not counts['known'] or counts['output_tokens']<=request['inferenceConfig']['maxTokens'])
    require('toolConfig' in request or not counts['tool_calls'])
    seen={c['toolUseId'] for message in request['messages'] for c in calls(message['content'])}
    require(all(c['toolUseId'] not in seen for c in calls(message(response)['content'])))


def disposition(response):
    if response.get('usage') is None:return 'usage-unknown'
    reason=response['stopReason']
    if reason=='tool_use':return 'tool-calls'
    if reason in ('guardrail_intervened','content_filtered'):return 'filtered'
    if reason in ('max_tokens','malformed_model_output','malformed_tool_use','model_context_window_exceeded'):return 'truncated'
    return 'text'


def continuation(protocol,result_raw,results):
    body=protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    require(body['codec_id']==CODEC)
    response=body['response'];check_response(response)
    require(disposition(response) in ('tool-calls','text'))
    assistant=message(response);current=calls(assistant['content'])
    require(type(results) is list and len(current)==len(results))
    segment=[assistant];blocks=[]
    for call,item in zip(current,results):
        require(type(item) is dict and item.keys()=={'tool_call_id','content'} and item['tool_call_id']==call['toolUseId']
                and type(item['content']) is str and len(item['content'])<=1048576)
        blocks.append(dict(toolResult=dict(toolUseId=item['tool_call_id'],content=[dict(text=item['content'])])))
    if blocks:segment.append(dict(role='user',content=blocks))
    try:
        return _canonical_value(segment,ORDINARY_LIMIT)
    except (UnicodeError,TypeError,ValueError):
        raise ContractError('contract message consistency check failed') from None
