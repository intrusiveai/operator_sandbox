"""Independent Converse fixtures; no validator implementation imports."""
from copy import deepcopy
import hashlib
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]/'schemas/fixtures'
binding=dict(codec_id='bedrock-converse-text-tools-v1',profile_id='fixture-bedrock',profile_digest='sha256:'+'1'*64)
tool=dict(toolSpec=dict(name='snapshot_list',description='List snapshots.',inputSchema=dict(json=dict(type='object',properties={},additionalProperties=False))))
policy=dict(binding,request_model='fixture.model-v1:0',prompt='Frozen instructions.',max_tokens=2048,tools=[tool])
request=dict(binding,request=dict(system=[dict(text=policy['prompt'])],messages=[dict(role='user',content=[dict(text='Inspect the target.')])],
    inferenceConfig=dict(maxTokens=2048),toolConfig=dict(tools=[tool],toolChoice=dict(auto={}))))
response=dict(binding,receipt_id='receipt-1',response=dict(output=dict(message=dict(role='assistant',content=[dict(text='Ready.')])),
    stopReason='end_turn',metrics=dict(latencyMs=100),usage=dict(inputTokens=100,outputTokens=20,totalTokens=120,
        cacheReadInputTokens=30,cacheWriteInputTokens=10,cacheDetails=[dict(ttl='1h',inputTokens=0),dict(ttl='5m',inputTokens=10)])))


def call(identifier='tool-1',name='snapshot_list',arguments=None):
    return dict(toolUse=dict(toolUseId=identifier,name=name,input={} if arguments is None else arguments))


def with_calls(values=None):
    result=deepcopy(response)
    result['response']['output']['message']['content']=values or [call()]
    result['response']['stopReason']='tool_use'
    return result


def metrics(known=True,count=0):
    return dict(known=known,input_tokens=140 if known else 0,output_tokens=20 if known else 0,total_tokens=160 if known else 0,tool_calls=count)


cases=[]
def case(name,mutate=lambda p,q,r:None,valid=True,disposition='text',base=None,mode='bound',counts=None):
    p,q,r=deepcopy(policy),deepcopy(request),deepcopy(base or response);mutate(p,q,r)
    item=dict(name='bedrock: '+name,mode=mode,policy=p,request=q,result=r,valid=valid)
    if valid:item.update(disposition=disposition,output_limit=2048)
    if counts is not None:item['metrics']=counts
    cases.append(item);return item


case('native text with separate caches',counts=metrics())
case('inclusive total does not double charge',lambda p,q,r:r['response']['usage'].update(totalTokens=160),counts=metrics())
case('multiple tools',base=with_calls([call(),call('tool-2')]),disposition='tool-calls',counts=metrics(count=2))
case('tool-free generation',lambda p,q,r:q['request'].pop('toolConfig'))
case('missing usage',lambda p,q,r:r['response'].pop('usage'),disposition='usage-unknown',counts=metrics(False))
case('null usage',lambda p,q,r:r['response'].update(usage=None),disposition='usage-unknown',counts=metrics(False))
case('unknown function is local error',base=with_calls([call(name='unknown')]),disposition='tool-calls')
case('reserved argument is local error',base=with_calls([call(arguments=dict(attempt_index=9))]),disposition='tool-calls')
for finish in ('max_tokens','malformed_tool_use','malformed_model_output','model_context_window_exceeded'):
    case(finish+' suppresses dispatch',lambda p,q,r,finish=finish:r['response'].update(stopReason=finish),base=with_calls(),disposition='truncated')
for finish in ('guardrail_intervened','content_filtered'):
    case(finish+' suppresses dispatch',lambda p,q,r,finish=finish:r['response'].update(stopReason=finish),base=with_calls(),disposition='filtered')
case('changed profile',lambda p,q,r:(q.update(profile_id='other'),r.update(profile_id='other')),False)
case('changed prompt',lambda p,q,r:q['request']['system'][0].update(text='Changed'),False)
case('changed tool catalog',lambda p,q,r:q['request']['toolConfig']['tools'][0]['toolSpec'].update(description='Changed'),False)
case('duplicate tool definitions',lambda p,q,r:q['request']['toolConfig']['tools'].append(deepcopy(tool)),False)
case('unapproved generation option',lambda p,q,r:q['request']['inferenceConfig'].update(temperature=1),False)
case('body model override',lambda p,q,r:q['request'].update(modelId='other'),False)
case('server tool',lambda p,q,r:r['response']['output']['message']['content'][0]['toolUse'].update(type='server_tool_use'),False,base=with_calls())
case('provider-specific options',lambda p,q,r:q['request'].update(additionalModelRequestFields={'other':True}),False)
case('provider-specific result semantics',lambda p,q,r:r['response'].update(additionalModelResponseFields={'other':True}),False)
case('empty native additional metadata',lambda p,q,r:r['response'].update(additionalModelResponseFields={}))
case('max output changed',lambda p,q,r:q['request']['inferenceConfig'].update(maxTokens=2049),False)
case('usage above request',lambda p,q,r:r['response']['usage'].update(outputTokens=2049,totalTokens=2149),False)
case('inconsistent native total',lambda p,q,r:r['response']['usage'].update(totalTokens=121),False)
case('cache overflow',lambda p,q,r:r['response']['usage'].update(cacheReadInputTokens=2**53-1),False)
case('cache breakdown mismatch',lambda p,q,r:r['response']['usage']['cacheDetails'][0].update(inputTokens=1),False)
case('cache breakdown ordering',lambda p,q,r:r['response']['usage']['cacheDetails'].reverse(),False)
case('duplicate tool response',base=with_calls([call(),call()]),valid=False)
case('tool finish with no calls',lambda p,q,r:r['response'].update(stopReason='tool_use'),False)
case('end turn with calls',lambda p,q,r:r['response'].update(stopReason='end_turn'),False,base=with_calls())
case('tool call without tool config',lambda p,q,r:q['request'].pop('toolConfig'),False,base=with_calls())
case('tool input scalar',base=with_calls([call(arguments=1)]),valid=False)
case('initial assistant',lambda p,q,r:q['request']['messages'][0].update(role='assistant'),False)
case('orphan result',lambda p,q,r:q['request']['messages'].append(dict(role='user',content=[dict(toolResult=dict(toolUseId='missing',content=[dict(text='x')]))])),False)
case('unfinished tools',lambda p,q,r:q['request']['messages'].append(dict(role='assistant',content=[call()])),False)
case('assistant prefill',lambda p,q,r:q['request']['messages'].append(dict(role='assistant',content=[dict(text='Finished.')])),False)

reasoning=[dict(reasoningContent=dict(reasoningText=dict(text='Native reasoning.',signature='opaque-signature'))),
           dict(reasoningContent=dict(redactedContent='eA=='))]
base=with_calls(reasoning+[call(),call('tool-2')])
item=case('lossless reasoning continuation',base=base,mode='bedrock-continuation')
item['tool_results']=[dict(tool_call_id='tool-1',content='{"snapshots":[]}'),dict(tool_call_id='tool-2',content='{"code":"TARGET_REVISION_CHANGED"}')]
segment=[base['response']['output']['message'],dict(role='user',content=[dict(toolResult=dict(toolUseId=r['tool_call_id'],content=[dict(text=r['content'])])) for r in item['tool_results']])]
item['segment_json']=json.dumps(segment,sort_keys=True,separators=(',',':'))
for label,results in [('missing',item['tool_results'][:1]),('reordered',list(reversed(item['tool_results'])))]:
    bad=deepcopy(item);bad.update(name='bedrock: '+label+' continuation',valid=False,tool_results=results);cases.append(bad)
case('malformed redacted bytes',lambda p,q,r:r['response']['output']['message']['content'][1]['reasoningContent'].update(redactedContent='bad'),False,base=base)
case('unsigned native reasoning retained',lambda p,q,r:r['response']['output']['message']['content'][0]['reasoningContent']['reasoningText'].pop('signature'),base=base,disposition='tool-calls')
case('historical call ID reuse',lambda p,q,r:q['request']['messages'].extend(segment),False,base=with_calls())
case('tool history without config',lambda p,q,r:(q['request']['messages'].extend(segment),q['request'].pop('toolConfig')),False)
case('out of order historical results',lambda p,q,r:(q['request']['messages'].extend(deepcopy(segment)),q['request']['messages'][-1]['content'].reverse()),False)
case('native wire shape',mode='wire')
case('wire suppression',lambda p,q,r:q['request'].pop('toolConfig'),False,base=with_calls(),mode='wire')

context=json.loads((ROOT/'engine-context-example.json').read_text())
context['model']=dict(binding,model_id=policy['request_model'],features=['text','function-tools'],codec_settings=dict(max_tokens=2048,
    tools_digest='sha256:'+hashlib.sha256(json.dumps(policy['tools'],sort_keys=True,separators=(',',':')).encode()).hexdigest()))
context['prompt']['provenance']['effective']=dict(size_bytes=len(policy['prompt'].encode()),digest='sha256:'+hashlib.sha256(policy['prompt'].encode()).hexdigest())
context['prompt']['provenance']['base']=deepcopy(context['prompt']['provenance']['effective'])
if 'engine.model_generate' not in context['operations']:context['operations'].append('engine.model_generate');context['operations'].sort()
item=case('startup policy',mode='context');item.update(context=context,tools=policy['tools'],prompt=policy['prompt'])
(ROOT/'bedrock-model-codec.json').write_text(json.dumps(cases,indent=2)+'\n')
print(len(cases),'Bedrock model fixtures')
