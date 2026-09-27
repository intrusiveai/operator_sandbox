"""GenerateContent's native history, positional tool results and usage."""
from .startup import require
from .validation import ORDINARY_LIMIT, ContractError
from .canonical import _canonical_value, _object_digest
from .schema_ids import ENGINE_MODEL_GENERATE_RESULT_SCHEMA

CODEC='gemini-text-tools-v1'
POLICY_FIELDS=('max_output_tokens','thinking_config','response_models')


def calls(parts):return [p['functionCall'] for p in parts if 'functionCall' in p]
def mode(request):return request['toolConfig']['functionCallingConfig']['mode']
def candidate(response):return (response.get('candidates') or [{}])[0]
def parts(response):return candidate(response).get('content',{}).get('parts',[])
def blocked(response):return response.get('promptFeedback',{}).get('blockReason') not in (None,'BLOCK_REASON_UNSPECIFIED')


def check_tools(tools):
    names=[d['name'] for t in tools for d in t['functionDeclarations']]
    require(len(names)==len(set(names)))


def check_parts(values):
    require(all('functionCall' not in p or not p.get('thought',False) for p in values))


def check_request(request):
    check_tools(request['tools']);require(request['tools'] or mode(request)!='ANY')
    seen,pending=set(),[]
    for index,message in enumerate(request['contents']):
        require(index!=0 or message['role']=='user');check_parts(message['parts'])
        if message['role']=='model':
            require(not pending)
            for call in calls(message['parts']):
                if 'id' in call:
                    require(call['id'] not in seen);seen.add(call['id'])
                pending.append(call)
            continue
        for part in message['parts']:
            if 'functionResponse' in part:
                result=part['functionResponse']
                require(pending and result['name']==pending[0]['name'] and result.get('id')==pending[0].get('id'))
                pending.pop(0)
            else:require(not pending)
        require(not pending)
    require(not pending and request['contents'][-1]['role']=='user')


def metrics(response):
    counts=dict(known=False,input_tokens=0,output_tokens=0,total_tokens=0,tool_calls=len(calls(parts(response))))
    usage=response.get('usageMetadata')
    if usage is not None:
        output=usage.get('candidatesTokenCount',0)+usage.get('thoughtsTokenCount',0)
        counts.update(known=True,input_tokens=usage['promptTokenCount'],output_tokens=output,total_tokens=usage['promptTokenCount']+output)
    return counts


def check_response(response):
    current=candidate(response)
    require(not current if blocked(response) else current and 'modelVersion' in response)
    values=parts(response);check_parts(values)
    require(current.get('finishReason')!='STOP' or 'content' in current)
    ids=[c['id'] for c in calls(values) if 'id' in c];require(len(ids)==len(set(ids)))
    counts=metrics(response);require(counts['total_tokens']<=2**53-1)
    usage=response.get('usageMetadata')
    if usage is not None:
        require(usage['totalTokenCount']==counts['total_tokens'] and usage.get('cachedContentTokenCount',0)<=usage['promptTokenCount'])
        for details,total in [('promptTokensDetails','promptTokenCount'),('cacheTokensDetails','cachedContentTokenCount'),('candidatesTokensDetails','candidatesTokenCount')]:
            if details in usage:require(sum(d['tokenCount'] for d in usage[details])==usage.get(total,0))


def check_policy(policy,request):
    check_request(request);check_tools(policy['tools']);config=request['generationConfig']
    require(request['systemInstruction']['parts'][0]['text']==policy['prompt'] and len(policy['prompt'].encode('utf-8'))<=131072
        and config['maxOutputTokens']<=policy['max_output_tokens']
        and _object_digest(config['thinkingConfig'],ORDINARY_LIMIT)==_object_digest(policy['thinking_config'],ORDINARY_LIMIT)
        and _object_digest(request['tools'],ORDINARY_LIMIT)==_object_digest(policy['tools'],ORDINARY_LIMIT))


def check_correlation(request,response):
    check_request(request);check_response(response);counts=metrics(response)
    require(not counts['known'] or counts['output_tokens']<=request['generationConfig']['maxOutputTokens'])
    require((mode(request)!='NONE' and request['tools']) or not counts['tool_calls'])
    seen={c['id'] for m in request['contents'] for c in calls(m['parts']) if 'id' in c}
    require(all(c.get('id') not in seen for c in calls(parts(response))))


def disposition(response):
    if response.get('usageMetadata') is None:return 'usage-unknown'
    if blocked(response):return 'filtered'
    finish=candidate(response)['finishReason']
    if finish=='STOP':return 'tool-calls' if calls(parts(response)) else 'text'
    if finish in ('SAFETY','RECITATION','BLOCKLIST','PROHIBITED_CONTENT','SPII','IMAGE_SAFETY','IMAGE_PROHIBITED_CONTENT','IMAGE_RECITATION','ESCALATION','PUP_LIMITED_DISABLED'):return 'filtered'
    return 'truncated'


def continuation(protocol,result_raw,results):
    body=protocol._catalog.validate(ENGINE_MODEL_GENERATE_RESULT_SCHEMA,result_raw)
    require(body['codec_id']==CODEC);response=body['response'];check_response(response)
    require(disposition(response) in ('tool-calls','text'))
    current=[(i,p['functionCall']) for i,p in enumerate(parts(response)) if 'functionCall' in p]
    require(type(results) is list and len(current)==len(results))
    segment=[candidate(response)['content']];blocks=[]
    for (index,call),result in zip(current,results):
        require(type(result) is dict and result.keys()=={'part_index','content'} and type(result['part_index']) is int
                and result['part_index']==index and type(result['content']) is str and len(result['content'])<=1048576)
        reply=dict(name=call['name'],response=dict(output=result['content']))
        if 'id' in call:reply['id']=call['id']
        blocks.append(dict(functionResponse=reply))
    if blocks:segment.append(dict(role='user',parts=blocks))
    try:return _canonical_value(segment,ORDINARY_LIMIT)
    except (UnicodeError,TypeError,ValueError):raise ContractError('contract message consistency check failed') from None
