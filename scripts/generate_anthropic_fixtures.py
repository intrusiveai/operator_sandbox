"""Independent Messages codec cases; no validator implementation imports."""
from copy import deepcopy
import hashlib
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]/'schemas/fixtures'
binding=dict(codec_id='anthropic-messages-text-tools-v1',profile_id='fixture-anthropic',profile_digest='sha256:'+'1'*64)
tool=dict(name='snapshot_list',description='List snapshots.',input_schema=dict(type='object',properties={},additionalProperties=False))
policy=dict(binding,request_model='fixture-model',response_models=['fixture-model-2026'],prompt='Frozen instructions.',
            max_tokens=2048,thinking=dict(type='disabled'),tools=[tool])
request=dict(binding,request=dict(model='fixture-model',system=policy['prompt'],messages=[dict(role='user',content='Inspect the target.')],
    max_tokens=2048,stream=False,thinking=dict(type='disabled'),tools=[tool],tool_choice=dict(type='auto')))
response=dict(binding,receipt_id='receipt-1',response=dict(id='msg-1',type='message',role='assistant',model='fixture-model-2026',
    content=[dict(type='text',text='Ready.',citations=None)],stop_reason='end_turn',stop_sequence=None,
    usage=dict(input_tokens=100,output_tokens=20,cache_creation_input_tokens=10,cache_read_input_tokens=30,
               cache_creation=dict(ephemeral_1h_input_tokens=0,ephemeral_5m_input_tokens=10),
               output_tokens_details=dict(thinking_tokens=0),server_tool_use=dict(web_search_requests=0,web_fetch_requests=0),
               service_tier='standard',inference_geo='global')))


def call(identifier='tool-1',name='snapshot_list',arguments=None):
    return dict(type='tool_use',id=identifier,name=name,input={} if arguments is None else arguments)


def with_calls(values=None):
    result=deepcopy(response)
    result['response'].update(stop_reason='tool_use',content=values or [call()])
    return result


def metrics(known=True,count=0):
    return dict(known=known,input_tokens=140 if known else 0,output_tokens=20 if known else 0,total_tokens=160 if known else 0,tool_calls=count)


cases=[]
def case(name,mutate=lambda p,q,r:None,valid=True,disposition='text',base=None,mode='bound',counts=None):
    p,q,r=deepcopy(policy),deepcopy(request),deepcopy(base or response)
    mutate(p,q,r)
    item=dict(name='anthropic: '+name,mode=mode,policy=p,request=q,result=r,valid=valid)
    if valid:item.update(disposition=disposition,output_limit=2048)
    if counts is not None:item['metrics']=counts
    cases.append(item)
    return item


case('native text including cache counts',counts=metrics())
case('multiple tools',base=with_calls([call(),call('tool-2')]),disposition='tool-calls',counts=metrics(count=2))
case('missing usage',lambda p,q,r:r['response'].pop('usage'),disposition='usage-unknown',counts=metrics(False))
case('null usage',lambda p,q,r:r['response'].update(usage=None),disposition='usage-unknown',counts=metrics(False))
case('unknown function remains a correctable local call',base=with_calls([call(name='unknown')]),disposition='tool-calls')
case('reserved argument is local validation',base=with_calls([call(arguments={'attempt_index':9})]),disposition='tool-calls')
case('refusal stop reason',lambda p,q,r:r['response'].update(stop_reason='refusal'),disposition='refusal')
case('refusal details retained',lambda p,q,r:r['response'].update(stop_details=dict(type='refusal',category='example',explanation='Declined.')),disposition='refusal')
for reason in ('max_tokens','pause_turn','model_context_window_exceeded'):
    case(reason+' executes no calls',lambda p,q,r,reason=reason:r['response'].update(stop_reason=reason),base=with_calls(),disposition='truncated')
case('compaction suppresses calls',lambda p,q,r:q['request'].update(tool_choice=dict(type='none')))
for thinking in (dict(type='adaptive'),dict(type='enabled',budget_tokens=1024)):
    case('pinned thinking '+thinking['type'],lambda p,q,r,thinking=thinking:(p.update(thinking=thinking),q['request'].update(thinking=thinking)))
case('guest profile change',lambda p,q,r:(q.update(profile_id='other'),r.update(profile_id='other')),False)
case('guest model change',lambda p,q,r:q['request'].update(model='other'),False)
case('unapproved response model',lambda p,q,r:r['response'].update(model='other'),False)
case('changed system prompt',lambda p,q,r:q['request'].update(system='Changed'),False)
case('changed tool catalog',lambda p,q,r:q['request']['tools'][0].update(description='Changed'),False)
case('duplicate tool definitions',lambda p,q,r:q['request']['tools'].append(deepcopy(tool)),False)
case('changed thinking policy',lambda p,q,r:q['request'].update(thinking=dict(type='adaptive')),False)
case('forced tool with thinking',lambda p,q,r:(p.update(thinking=dict(type='adaptive')),q['request'].update(thinking=dict(type='adaptive'),tool_choice=dict(type='any'))),False)
case('thinking exceeds output allowance',lambda p,q,r:(p.update(thinking=dict(type='enabled',budget_tokens=2048)),q['request'].update(thinking=dict(type='enabled',budget_tokens=2048))),False)
case('output exceeds profile',lambda p,q,r:q['request'].update(max_tokens=2049),False)
case('output usage exceeds request',lambda p,q,r:r['response']['usage'].update(output_tokens=2049),False)
case('cache detail mismatch',lambda p,q,r:r['response']['usage']['cache_creation'].update(ephemeral_1h_input_tokens=1),False)
case('thinking usage exceeds output',lambda p,q,r:r['response']['usage']['output_tokens_details'].update(thinking_tokens=21),False)
case('aggregate usage overflow',lambda p,q,r:r['response']['usage'].update(input_tokens=2**53-1),False)
case('duplicate response calls',base=with_calls([call(),call()]),valid=False)
case('tool stop without calls',lambda p,q,r:r['response'].update(stop_reason='tool_use'),False)
case('end turn with calls',lambda p,q,r:r['response'].update(stop_reason='end_turn'),False,base=with_calls())
case('tool call while suppressed',lambda p,q,r:q['request'].update(tool_choice=dict(type='none')),False,base=with_calls())
case('server tool response rejected',lambda p,q,r:r['response']['usage']['server_tool_use'].update(web_search_requests=1),False)
case('remote container rejected',lambda p,q,r:r['response'].update(container={'id':'remote'}),False)
case('unknown native field rejected',lambda p,q,r:r['response'].update(new_behavior=True),False)
case('tool input scalar rejected',base=with_calls([call(arguments=1)]),valid=False)
case('initial assistant forbidden',lambda p,q,r:q['request'].update(messages=[dict(role='assistant',content=[])]),False)
case('orphan result forbidden',lambda p,q,r:q['request']['messages'].append(dict(role='user',content=[dict(type='tool_result',tool_use_id='missing',content='x')])),False)
case('unfinished tool batch forbidden',lambda p,q,r:q['request']['messages'].append(dict(role='assistant',content=[call()])),False)
case('assistant prefill forbidden',lambda p,q,r:q['request']['messages'].append(dict(role='assistant',content=[dict(type='text',text='Finished.')])),False)

# Preserve both opaque thinking variants and original block order across a batch.
thoughts=[dict(type='thinking',thinking='native thinking',signature='opaque-signature'),dict(type='redacted_thinking',data='opaque-data')]
base=with_calls(thoughts+[call(),call('tool-2')])
item=case('complete native continuation',base=base,mode='anthropic-continuation')
item['tool_results']=[dict(tool_call_id='tool-1',content='{"snapshots":[]}'),dict(tool_call_id='tool-2',content='{"code":"TARGET_REVISION_CHANGED"}')]
segment=[dict(role='assistant',content=base['response']['content']),dict(role='user',content=[dict(type='tool_result',tool_use_id=r['tool_call_id'],content=r['content']) for r in item['tool_results']])]
item['segment_json']=json.dumps(segment,ensure_ascii=False,sort_keys=True,separators=(',',':'))
for label,results in [('missing',item['tool_results'][:1]),('reordered',list(reversed(item['tool_results']))),('wrong',[dict(tool_call_id='wrong',content='x')]*2)]:
    bad=deepcopy(item);bad.update(name='anthropic: '+label+' correlated continuation',valid=False,tool_results=results);cases.append(bad)
case('historical call reuse rejected',lambda p,q,r:q['request']['messages'].extend(segment),False,base=with_calls())
case('out of order historical results',lambda p,q,r:(q['request']['messages'].extend(deepcopy(segment)),q['request']['messages'][-1]['content'].reverse()),False)
case('native wire shape',mode='wire')
case('wire suppression',lambda p,q,r:q['request'].update(tool_choice=dict(type='none')),False,base=with_calls(),mode='wire')

context=json.loads((ROOT/'engine-context-example.json').read_text())
context['model']=dict(binding,model_id=policy['request_model'],features=['text','function-tools'],codec_settings=dict(
    max_tokens=2048,thinking=policy['thinking'],response_models=policy['response_models'],tools_digest='sha256:'+hashlib.sha256(json.dumps(policy['tools'],sort_keys=True,separators=(',',':')).encode()).hexdigest()))
context['prompt']['provenance']['effective']=dict(size_bytes=len(policy['prompt'].encode()),digest='sha256:'+hashlib.sha256(policy['prompt'].encode()).hexdigest())
context['prompt']['provenance']['base']=deepcopy(context['prompt']['provenance']['effective'])
if 'engine.model_generate' not in context['operations']: context['operations'].append('engine.model_generate');context['operations'].sort()
item=case('startup policy',mode='context');item.update(context=context,tools=policy['tools'],prompt=policy['prompt'])
(ROOT/'anthropic-model-codec.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2)+'\n')
print(len(cases),'Anthropic model fixtures')
