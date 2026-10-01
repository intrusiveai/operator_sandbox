"""Independent GenerateContent fixtures, including absent native call IDs."""
from copy import deepcopy
import hashlib
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas/fixtures'
binding=dict(codec_id='gemini-text-tools-v1',profile_id='fixture-gemini',profile_digest='sha256:'+'1'*64)
tools=[dict(functionDeclarations=[dict(name='snapshot_list',description='List snapshots.',parametersJsonSchema=dict(type='object',properties={},additionalProperties=False))])]
policy=dict(binding,request_model='fixture-gemini',response_models=['fixture-gemini-001'],prompt='Frozen instructions.',max_output_tokens=2048,thinking_config=dict(thinkingBudget=-1,includeThoughts=True),tools=tools)
request=dict(binding,request=dict(systemInstruction=dict(parts=[dict(text=policy['prompt'])]),contents=[dict(role='user',parts=[dict(text='Inspect the target.')])],
    generationConfig=dict(maxOutputTokens=2048,candidateCount=1,thinkingConfig=policy['thinking_config']),tools=tools,toolConfig=dict(functionCallingConfig=dict(mode='AUTO'))))
response=dict(binding,receipt_id='receipt-1',response=dict(candidates=[dict(index=0,content=dict(role='model',parts=[dict(text='Ready.')]),finishReason='STOP')],
    modelVersion='fixture-gemini-001',responseId='native-1',usageMetadata=dict(promptTokenCount=100,candidatesTokenCount=20,thoughtsTokenCount=40,totalTokenCount=160,cachedContentTokenCount=30)))


def call(name='snapshot_list',identifier=None,args=None):
    c=dict(name=name,args={} if args is None else args)
    if identifier is not None:c['id']=identifier
    return dict(functionCall=c)


def with_calls(values=None):
    r=deepcopy(response);r['response']['candidates'][0]['content']['parts']=values or [call()];return r


def metrics(known=True,count=0):return dict(known=known,input_tokens=100 if known else 0,output_tokens=60 if known else 0,total_tokens=160 if known else 0,tool_calls=count)


cases=[]
def case(name,mutate=lambda p,q,r:None,valid=True,disposition='text',base=None,mode='bound',counts=None):
    p,q,r=deepcopy(policy),deepcopy(request),deepcopy(base or response);mutate(p,q,r)
    item=dict(name='gemini: '+name,mode=mode,policy=p,request=q,result=r,valid=valid)
    if valid:item.update(disposition=disposition,output_limit=2048)
    if counts is not None:item['metrics']=counts
    cases.append(item);return item


case('native text including thinking tokens',counts=metrics())
for traffic in ('TRAFFIC_TYPE_UNSPECIFIED','ON_DEMAND','PROVISIONED_THROUGHPUT','ON_DEMAND_PRIORITY','ON_DEMAND_FLEX'):
    case('traffic metadata '+traffic,lambda p,q,r,traffic=traffic:r['response']['usageMetadata'].update(trafficType=traffic),counts=metrics())
for traffic in ('unknown',None,1):
    case('invalid traffic metadata '+str(traffic),lambda p,q,r,traffic=traffic:r['response']['usageMetadata'].update(trafficType=traffic),False)

case('multiple same-name tools without IDs',base=with_calls([call(),call()]),disposition='tool-calls',counts=metrics(count=2))
case('explicit IDs',base=with_calls([call(identifier='c1'),call(identifier='c2')]),disposition='tool-calls')
case('tool suppression',lambda p,q,r:q['request']['toolConfig']['functionCallingConfig'].update(mode='NONE'))
case('empty tools',lambda p,q,r:(q['request'].update(tools=[]),p.update(tools=[])))
case('required empty tools',lambda p,q,r:(q['request'].update(tools=[]),p.update(tools=[]),q['request']['toolConfig']['functionCallingConfig'].update(mode='ANY')),False)
case('missing whole usage',lambda p,q,r:r['response'].pop('usageMetadata'),disposition='usage-unknown',counts=metrics(False))
case('null whole usage',lambda p,q,r:r['response'].update(usageMetadata=None),disposition='usage-unknown',counts=metrics(False))
case('unknown function',base=with_calls([call('unknown')]),disposition='tool-calls')
case('reserved arguments remain local error',base=with_calls([call(args=dict(attempt_index=4))]),disposition='tool-calls')
case('omitted empty native arguments',lambda p,q,r:r['response']['candidates'][0]['content']['parts'][0]['functionCall'].pop('args'),base=with_calls(),disposition='tool-calls')
for finish in ('MAX_TOKENS','LANGUAGE','OTHER','MALFORMED_FUNCTION_CALL','UNEXPECTED_TOOL_CALL','TOO_MANY_TOOL_CALLS','MISSING_THOUGHT_SIGNATURE','MALFORMED_RESPONSE'):
    case(finish,lambda p,q,r,finish=finish:r['response']['candidates'][0].update(finishReason=finish),base=with_calls(),disposition='truncated')
for finish in ('SAFETY','RECITATION','BLOCKLIST','PROHIBITED_CONTENT','SPII','ESCALATION'):
    case(finish,lambda p,q,r,finish=finish:r['response']['candidates'][0].update(finishReason=finish),base=with_calls(),disposition='filtered')

def block(p,q,r):
    r['response'].pop('candidates');r['response'].pop('modelVersion')
    r['response']['promptFeedback']=dict(blockReason='SAFETY',safetyRatings=[dict(category='HARM_CATEGORY_DANGEROUS_CONTENT',probability='HIGH',blocked=True)])
    r['response']['usageMetadata']=dict(promptTokenCount=100,totalTokenCount=100)
case('blocked prompt without candidates or output usage',block,disposition='filtered',counts=dict(known=True,input_tokens=100,output_tokens=0,total_tokens=100,tool_calls=0))
case('empty unexplained result',lambda p,q,r:r['response'].pop('candidates'),False)
case('block with candidates',lambda p,q,r:r['response'].update(promptFeedback=dict(blockReason='SAFETY')),False)
case('missing version on completed result',lambda p,q,r:r['response'].pop('modelVersion'),False)
case('different model version',lambda p,q,r:r['response'].update(modelVersion='other'),False)
case('changed profile',lambda p,q,r:(q.update(profile_id='other'),r.update(profile_id='other')),False)
case('changed prompt',lambda p,q,r:q['request']['systemInstruction']['parts'][0].update(text='Changed'),False)
case('changed thinking',lambda p,q,r:q['request']['generationConfig']['thinkingConfig'].update(thinkingBudget=0),False)
case('conflicting thinking controls',lambda p,q,r:q['request']['generationConfig']['thinkingConfig'].update(thinkingLevel='HIGH'),False)
case('changed tool definition',lambda p,q,r:q['request']['tools'][0]['functionDeclarations'][0].update(description='Changed'),False)
case('duplicate tool definitions',lambda p,q,r:q['request']['tools'][0]['functionDeclarations'].append(deepcopy(tools[0]['functionDeclarations'][0])),False)
case('server tools',lambda p,q,r:q['request']['tools'].append(dict(googleSearch={})),False)
case('body model override',lambda p,q,r:q['request'].update(model='other'),False)
case('extra candidate',lambda p,q,r:r['response']['candidates'].append(deepcopy(r['response']['candidates'][0])),False)
case('partial usage',lambda p,q,r:r['response']['usageMetadata'].pop('promptTokenCount'),False)
case('thinking exceeds output allowance',lambda p,q,r:r['response']['usageMetadata'].update(thoughtsTokenCount=2048,totalTokenCount=2168),False)
case('inconsistent totals',lambda p,q,r:r['response']['usageMetadata'].update(totalTokenCount=120),False)
case('cached tokens exceed input',lambda p,q,r:r['response']['usageMetadata'].update(cachedContentTokenCount=101),False)
case('usage overflow',lambda p,q,r:r['response']['usageMetadata'].update(thoughtsTokenCount=2**53-1),False)
case('unexpected hosted tool usage',lambda p,q,r:r['response']['usageMetadata'].update(toolUsePromptTokenCount=1,totalTokenCount=161),False)
case('text usage details',lambda p,q,r:r['response']['usageMetadata'].update(promptTokensDetails=[dict(modality='TEXT',tokenCount=100)],cacheTokensDetails=[dict(modality='TEXT',tokenCount=30)],candidatesTokensDetails=[dict(modality='TEXT',tokenCount=20)]))
case('wrong usage details',lambda p,q,r:r['response']['usageMetadata'].update(promptTokensDetails=[dict(modality='TEXT',tokenCount=99)]),False)
case('unsupported media',lambda p,q,r:r['response']['candidates'][0]['content']['parts'].append(dict(inlineData=dict(mimeType='image/png',data='eA=='))),False)
case('no candidate content at STOP',lambda p,q,r:r['response']['candidates'][0].pop('content'),False)
case('filtered without candidate content',lambda p,q,r:(r['response']['candidates'][0].pop('content'),r['response']['candidates'][0].update(finishReason='SAFETY')),disposition='filtered')
case('calls suppressed',lambda p,q,r:q['request']['toolConfig']['functionCallingConfig'].update(mode='NONE'),False,base=with_calls())
case('call marked as thought',lambda p,q,r:r['response']['candidates'][0]['content']['parts'][0].update(thought=True),False,base=with_calls())
case('duplicate native IDs',base=with_calls([call(identifier='c1'),call(identifier='c1')]),valid=False)
case('initial model message',lambda p,q,r:q['request']['contents'][0].update(role='model'),False)
case('unfinished tool batch',lambda p,q,r:q['request']['contents'].append(dict(role='model',parts=[call()])),False)
case('orphan result',lambda p,q,r:q['request']['contents'].append(dict(role='user',parts=[dict(functionResponse=dict(name='snapshot_list',response=dict(output='x')))])),False)

base=with_calls([dict(text='Reasoning summary.',thought=True,thoughtSignature='c2ln'),dict(call(),thoughtSignature='c2lnMg=='),call(identifier='c2'),dict(thoughtSignature='c2lnMw==')])
item=case('lossless positional continuation',base=base,mode='gemini-continuation')
item['gemini_tool_results']=[dict(part_index=1,content='{"snapshots":[]}'),dict(part_index=2,content='{"code":"TARGET_REVISION_CHANGED"}')]
segment=[base['response']['candidates'][0]['content'],dict(role='user',parts=[dict(functionResponse=dict(name='snapshot_list',response=dict(output=item['gemini_tool_results'][0]['content']))),dict(functionResponse=dict(name='snapshot_list',id='c2',response=dict(output=item['gemini_tool_results'][1]['content'])))])]
item['segment_json']=json.dumps(segment,sort_keys=True,separators=(',',':'))
for name,values in [('missing',item['gemini_tool_results'][:1]),('reordered',list(reversed(item['gemini_tool_results']))),('wrong index',[dict(part_index=0,content='x'),item['gemini_tool_results'][1]])]:
    bad=deepcopy(item);bad.update(name='gemini: '+name+' continuation',valid=False,gemini_tool_results=values);cases.append(bad)
case('historical explicit ID reuse',lambda p,q,r:q['request']['contents'].extend(deepcopy(segment)),False,base=with_calls([call(identifier='c2')]))
case('historical anonymous calls are distinct turns',lambda p,q,r:q['request']['contents'].extend(deepcopy(segment)),base=with_calls(),disposition='tool-calls')
case('historical result ID mismatch',lambda p,q,r:(q['request']['contents'].extend(deepcopy(segment)),q['request']['contents'][-1]['parts'][1]['functionResponse'].update(id='wrong')),False)
case('historical result name mismatch',lambda p,q,r:(q['request']['contents'].extend(deepcopy(segment)),q['request']['contents'][-1]['parts'][0]['functionResponse'].update(name='wrong')),False)
case('ordinary wire exchange',mode='wire')
case('ordinary wire ID rejection',base=with_calls([call(identifier='c1'),call(identifier='c1')]),valid=False,mode='wire')

context=json.loads((ROOT/'engine-context-example.json').read_text())
context['model']=dict(binding,model_id=policy['request_model'],features=['text','function-tools'],codec_settings=dict(max_output_tokens=2048,thinking_config=policy['thinking_config'],response_models=policy['response_models'],
    tools_digest='sha256:'+hashlib.sha256(json.dumps(policy['tools'],sort_keys=True,separators=(',',':')).encode()).hexdigest()))
context['prompt']['provenance']['effective']=dict(size_bytes=len(policy['prompt'].encode()),digest='sha256:'+hashlib.sha256(policy['prompt'].encode()).hexdigest())
context['prompt']['provenance']['base']=deepcopy(context['prompt']['provenance']['effective'])
if 'engine.model_generate' not in context['operations']:context['operations'].append('engine.model_generate');context['operations'].sort()
item=case('startup policy',mode='context');item.update(context=context,tools=policy['tools'],prompt=policy['prompt'])
(ROOT/'gemini-model-codec.json').write_text(json.dumps(cases,indent=2)+'\n')
print(len(cases),'Gemini model fixtures')
