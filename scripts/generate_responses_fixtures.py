"""Independent stateless Responses fixtures; no production validator imports."""
from copy import deepcopy
import hashlib
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas/fixtures'
binding=dict(codec_id='openai-responses-text-tools-v1',profile_id='fixture-responses',profile_digest='sha256:'+'1'*64)
tools=[dict(type='function',name='snapshot_list',description='List snapshots.',parameters=dict(type='object',properties={},additionalProperties=False),strict=False)]
policy=dict(binding,request_model='fixture-responses',response_models=['fixture-responses-001'],prompt='Frozen instructions.',max_output_tokens=2048,reasoning=dict(effort='medium'),tools=tools)
request=dict(binding,request=dict(model=policy['request_model'],instructions=policy['prompt'],input=[dict(role='user',content='Inspect the target.')],
    max_output_tokens=2048,stream=False,store=False,parallel_tool_calls=True,include=['reasoning.encrypted_content'],truncation='disabled',
    reasoning=policy['reasoning'],tools=tools,tool_choice='auto',text=dict(format=dict(type='text'))))
message=dict(type='message',id='msg-1',role='assistant',status='completed',phase='final_answer',content=[dict(type='output_text',text='Ready.',annotations=[])])
response=dict(binding,receipt_id='receipt-1',response=dict(id='response-1',object='response',created_at=1800000000.25,model='fixture-responses-001',status='completed',
    output=[message],usage=dict(input_tokens=100,output_tokens=60,total_tokens=160,input_tokens_details=dict(cached_tokens=30,cache_write_tokens=10),output_tokens_details=dict(reasoning_tokens=40)),
    error=None,incomplete_details=None,store=False,background=False,previous_response_id=None,conversation=None,metadata={},
    instructions=policy['prompt'],parallel_tool_calls=True,max_output_tokens=2048,tools=tools,tool_choice='auto',reasoning=dict(effort='medium',summary=None,context='all_turns',mode='standard'),
    text=dict(format=dict(type='text'),verbosity='medium'),truncation='disabled',temperature=1,top_p=1))


def call(identifier='call-1',name='snapshot_list',arguments='{}'):
    return dict(type='function_call',call_id=identifier,id='item-'+identifier,name=name,arguments=arguments,status='completed',caller=dict(type='direct'))


def with_calls(values=None):
    r=deepcopy(response);r['response']['output']=values or [call()];return r


def metrics(known=True,count=0):return dict(known=known,input_tokens=100 if known else 0,output_tokens=60 if known else 0,total_tokens=160 if known else 0,tool_calls=count)


cases=[]
def case(name,mutate=lambda p,q,r:None,valid=True,disposition='text',base=None,mode='bound',counts=None):
    p,q,r=deepcopy(policy),deepcopy(request),deepcopy(base or response);mutate(p,q,r)
    item=dict(name='responses: '+name,mode=mode,policy=p,request=q,result=r,valid=valid)
    if valid:item.update(disposition=disposition,output_limit=2048)
    if counts is not None:item['metrics']=counts
    cases.append(item);return item


case('native text with usage details',counts=metrics())
case('parallel call batch',base=with_calls([call(),call('call-2')]),disposition='tool-calls',counts=metrics(count=2))
case('missing whole usage',lambda p,q,r:r['response'].pop('usage'),disposition='usage-unknown',counts=metrics(False))
case('null whole usage',lambda p,q,r:r['response'].update(usage=None),disposition='usage-unknown',counts=metrics(False))
case('unparsed malformed arguments',base=with_calls([call(arguments='{bad')]),disposition='tool-calls')
case('unknown local function',base=with_calls([call(name='unknown')]),disposition='tool-calls')
case('reserved local argument',base=with_calls([call(arguments='{"attempt_index":4}')]),disposition='tool-calls')
case('tool-free generation',lambda p,q,r:(q['request'].update(tool_choice='none'),r['response'].update(tool_choice='none')))
case('changed prompt',lambda p,q,r:q['request'].update(instructions='Changed'),False)
case('changed model',lambda p,q,r:q['request'].update(model='other'),False)
case('changed model version',lambda p,q,r:r['response'].update(model='other'),False)
case('changed profile',lambda p,q,r:(q.update(profile_id='other'),r.update(profile_id='other')),False)
case('changed reasoning',lambda p,q,r:q['request']['reasoning'].update(effort='low'),False)
case('changed tools',lambda p,q,r:q['request']['tools'][0].update(description='Changed'),False)
case('duplicate definitions',lambda p,q,r:q['request']['tools'].append(deepcopy(tools[0])),False)
case('remote state',lambda p,q,r:q['request'].update(previous_response_id='previous'),False)
case('remote conversation',lambda p,q,r:q['request'].update(conversation='previous'),False)
case('background generation',lambda p,q,r:q['request'].update(background=True),False)
case('persisted response',lambda p,q,r:q['request'].update(store=True),False)
case('hosted tools',lambda p,q,r:q['request']['tools'].append(dict(type='web_search')),False)
case('hosted output item',lambda p,q,r:r['response']['output'].append(dict(type='web_search_call',id='ws-1',status='completed')),False)
case('caller program',lambda p,q,r:r['response']['output'][0].update(caller=dict(type='program',caller_id='p1')),False,base=with_calls())
case('async tool call',lambda p,q,r:r['response']['output'][0].update(**{'async':True}),False,base=with_calls())
case('namespaced tool call',lambda p,q,r:r['response']['output'][0].update(namespace='remote'),False,base=with_calls())
case('missing encryption request',lambda p,q,r:q['request'].update(include=[]),False)
case('automatic truncation',lambda p,q,r:q['request'].update(truncation='auto'),False)
case('privileged history',lambda p,q,r:q['request']['input'][0].update(role='developer'),False)
case('empty history',lambda p,q,r:q['request'].update(input=[]),False)
case('orphan result',lambda p,q,r:q['request']['input'].append(dict(type='function_call_output',call_id='missing',output='x')),False)
case('unfinished calls',lambda p,q,r:q['request']['input'].append(call()),False)
case('suppressed call response',lambda p,q,r:(q['request'].update(tool_choice='none'),r['response'].update(tool_choice='none')),False,base=with_calls())
case('duplicate call IDs',base=with_calls([call(),dict(call(),id='other')]),valid=False)
case('duplicate item IDs',base=with_calls([call(),dict(call('call-2'),id='item-call-1')]),valid=False)
case('incomplete item under completed response',lambda p,q,r:r['response']['output'][0].update(status='incomplete'),False,base=with_calls())
for reason in ('max_output_tokens','max_messages','steered','content_filter'):
    case('incomplete '+reason,lambda p,q,r,reason=reason:r['response'].update(status='incomplete',incomplete_details=dict(reason=reason)),
        base=with_calls([dict(call(arguments='{partial'),status='incomplete')]),disposition='filtered' if reason=='content_filter' else 'truncated')
case('failed native result',lambda p,q,r:r['response'].update(status='failed',error=dict(code='server_error',message='Generation failed.')),disposition='truncated')
case('cancelled native result',lambda p,q,r:r['response'].update(status='cancelled'),disposition='truncated')
case('failed without error',lambda p,q,r:r['response'].update(status='failed'),False)
case('completed with error',lambda p,q,r:r['response'].update(error=dict(code='server_error',message='Failure.')),False)
case('incomplete without details',lambda p,q,r:r['response'].update(status='incomplete'),False)
case('changed instructions echo',lambda p,q,r:r['response'].update(instructions='Changed'),False)
case('changed tools echo',lambda p,q,r:r['response']['tools'][0].update(description='Changed'),False)
case('changed reasoning echo',lambda p,q,r:r['response']['reasoning'].update(effort='high'),False)
case('inconsistent usage',lambda p,q,r:r['response']['usage'].update(total_tokens=161),False)
case('cache detail exceeds input',lambda p,q,r:r['response']['usage']['input_tokens_details'].update(cached_tokens=101),False)
case('reasoning detail exceeds output',lambda p,q,r:r['response']['usage']['output_tokens_details'].update(reasoning_tokens=61),False)
case('output above cap',lambda p,q,r:r['response']['usage'].update(output_tokens=2049,total_tokens=2149),False)
case('partial usage',lambda p,q,r:r['response']['usage'].pop('input_tokens'),False)
case('minimal native metadata',lambda p,q,r:r['response'].clear() or r['response'].update({k:deepcopy(response['response'][k]) for k in ['id','object','created_at','model','status','output','usage']}))
refusal=deepcopy(message);refusal['content']=[dict(type='refusal',refusal='Declined.')]
case('refusal',base=with_calls([refusal]),disposition='refusal')
case('refusal alongside call',base=with_calls([refusal,call()]),valid=False)
case('historical refusal does not prohibit future tools',lambda p,q,r:q['request']['input'].extend([deepcopy(refusal),dict(role='user',content='List available snapshots.')]),base=with_calls(),disposition='tool-calls')
reasoning=dict(type='reasoning',id='reasoning-1',summary=[dict(type='summary_text',text='Inspecting results.')],encrypted_content='opaque-encrypted-data',status='completed')
base=with_calls([reasoning,dict(deepcopy(message),phase='commentary'),call(arguments='{malformed'),call('call-2')])
item=case('lossless reasoning and phase continuation',base=base,mode='responses-continuation')
item['tool_results']=[dict(tool_call_id='call-1',content='{"code":"INVALID_ARGUMENT"}'),dict(tool_call_id='call-2',content='{"code":"TARGET_REVISION_CHANGED"}')]
segment=base['response']['output']+[dict(type='function_call_output',call_id=r['tool_call_id'],output=r['content'],caller=dict(type='direct')) for r in item['tool_results']]
item['segment_json']=json.dumps(segment,sort_keys=True,separators=(',',':'))
for label,results in [('missing',item['tool_results'][:1]),('reordered',list(reversed(item['tool_results'])))]:
    bad=deepcopy(item);bad.update(name='responses: '+label+' continuation',valid=False,tool_results=results);cases.append(bad)
case('missing encrypted reasoning',lambda p,q,r:r['response']['output'][0].pop('encrypted_content'),False,base=base)
case('partial reasoning is retained without dispatch',lambda p,q,r:(r['response']['output'][0].pop('encrypted_content'),r['response'].update(status='incomplete',incomplete_details=dict(reason='max_output_tokens'))),base=base,disposition='truncated')
case('reused historical call ID',lambda p,q,r:q['request']['input'].extend(deepcopy(segment)),False,base=with_calls())
case('reused historical message ID',lambda p,q,r:q['request']['input'].extend(deepcopy(segment)),False)
case('historical reordered results',lambda p,q,r:q['request']['input'].extend(deepcopy(segment[:-2]+list(reversed(segment[-2:])))),False,base=with_calls([call('new')]))
case('native wire exchange',mode='wire')
case('wire suppressed calls',lambda p,q,r:(q['request'].update(tool_choice='none'),r['response'].update(tool_choice='none')),False,base=with_calls(),mode='wire')
context=json.loads((ROOT/'engine-context-example.json').read_text())
context['model']=dict(binding,model_id=policy['request_model'],features=['text','function-tools'],codec_settings=dict(max_output_tokens=2048,reasoning=policy['reasoning'],response_models=policy['response_models'],
    tools_digest='sha256:'+hashlib.sha256(json.dumps(policy['tools'],sort_keys=True,separators=(',',':')).encode()).hexdigest()))
context['prompt']['provenance']['effective']=dict(size_bytes=len(policy['prompt'].encode()),digest='sha256:'+hashlib.sha256(policy['prompt'].encode()).hexdigest())
context['prompt']['provenance']['base']=deepcopy(context['prompt']['provenance']['effective'])
if 'engine.model_generate' not in context['operations']:context['operations'].append('engine.model_generate');context['operations'].sort()
item=case('startup policy',mode='context');item.update(context=context,tools=policy['tools'],prompt=policy['prompt'])
(ROOT/'responses-model-codec.json').write_text(json.dumps(cases,indent=2)+'\n')
print(len(cases),'Responses model fixtures')
