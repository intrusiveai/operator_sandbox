"""Author native model exchange and continuation fixtures, without implementation imports."""
from copy import deepcopy
import json
import hashlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]/'schemas/fixtures'
binding = dict(codec_id='openai-chat-text-tools-v1',profile_id='fixture-model-profile',profile_digest='sha256:'+'1'*64)
tool = dict(type='function',function=dict(name='snapshot_list',description='List available snapshots.',
            parameters=dict(type='object',properties={},additionalProperties=False),strict=False))
policy = dict(binding,request_model='fixture-model',response_models=['fixture-model-2026'],
              instruction_role='developer',prompt='Frozen prompt. <Keep bytes> café\n',max_completion_tokens=1024,tools=[tool])
request = dict(binding,request=dict(model='fixture-model',messages=[dict(role='developer',content=policy['prompt']),
    dict(role='user',content='Inspect the target.')],max_completion_tokens=1024,stream=False,store=False,n=1,
    parallel_tool_calls=True,tools=[tool],tool_choice='auto'))
response = dict(binding,receipt_id='model-receipt-1',response=dict(id='chatcmpl-fixture',object='chat.completion',
    created=100,model='fixture-model-2026',choices=[dict(index=0,message=dict(role='assistant',content='Ready.',
    refusal=None,annotations=[]),finish_reason='stop',logprobs=None)],usage=dict(prompt_tokens=100,completion_tokens=20,
    total_tokens=120,prompt_tokens_details=dict(cached_tokens=10,audio_tokens=0),
    completion_tokens_details=dict(reasoning_tokens=5,audio_tokens=0,accepted_prediction_tokens=0,rejected_prediction_tokens=0)),
    service_tier='default',system_fingerprint=None))


def call(identifier='call-1',name='snapshot_list',arguments='{}'):
    return dict(id=identifier,type='function',function=dict(name=name,arguments=arguments))


def with_calls(values=None):
    result = deepcopy(response)
    result['response']['choices'][0].update(finish_reason='tool_calls',message=dict(role='assistant',content=None,
        refusal=None,annotations=[],tool_calls=values or [call()]))
    return result


cases = []


def case(name, mutate=lambda p,q,r:None, valid=True, disposition='text', base=None, mode='bound'):
    p,q,r = deepcopy(policy),deepcopy(request),deepcopy(base or response)
    mutate(p,q,r)
    item = dict(name=name,mode=mode,policy=p,request=q,result=r,valid=valid)
    if valid: item['disposition']=disposition
    cases.append(item)
    return item


case('native text and usage preserved')
case('multiple native tools',base=with_calls([call(),call('call-2')]),disposition='tool-calls')
case('system instruction role explicitly selected',lambda p,q,r:(p.update(instruction_role='system'),q['request']['messages'][0].update(role='system')))
case('missing usage is explicit unknown',lambda p,q,r:r['response'].pop('usage'),disposition='usage-unknown')
case('null usage is explicit unknown',lambda p,q,r:r['response'].update(usage=None),disposition='usage-unknown')
case('refusal preserved',lambda p,q,r:r['response']['choices'][0]['message'].update(content=None,refusal='Cannot comply.'),disposition='refusal')
case('empty response does not complete campaign',lambda p,q,r:r['response']['choices'][0]['message'].update(content=None))
for reason, outcome in [('length','truncated'),('content_filter','filtered')]:
    case(reason+' with incomplete arguments is not dispatched',lambda p,q,r,reason=reason:r['response']['choices'][0].update(finish_reason=reason),
         base=with_calls([call(arguments='{"unfinished":')]),disposition=outcome)
for arguments in ['{','[]','null','{"x":1,"x":2}','{"attempt_index":7}']:
    case('local argument validation remains separate '+arguments,base=with_calls([call(arguments=arguments)]),disposition='tool-calls')
case('unknown function is a correctable local call',base=with_calls([call(name='unknown_tool')]),disposition='tool-calls')
case('null optional response fields',lambda p,q,r:r['response']['choices'][0]['message'].update(tool_calls=None,audio=None,function_call=None))
case('empty optional native call array',lambda p,q,r:r['response']['choices'][0]['message'].update(tool_calls=[]))
case('compaction request suppresses tools',lambda p,q,r:q['request'].update(tool_choice='none'))
case('unadvertised codec',lambda p,q,r:q.update(codec_id='other'),False)
case('guest changes profile',lambda p,q,r:(q.update(profile_id='other'),r.update(profile_id='other')),False)
case('guest changes profile pin',lambda p,q,r:(q.update(profile_digest='sha256:'+'2'*64),r.update(profile_digest='sha256:'+'2'*64)),False)
case('result changes profile',lambda p,q,r:r.update(profile_id='other'),False)
case('guest changes model',lambda p,q,r:q['request'].update(model='other'),False)
case('unqualified response model',lambda p,q,r:r['response'].update(model='unqualified-model'),False)
case('output ceiling exceeds installed profile',lambda p,q,r:q['request'].update(max_completion_tokens=1025),False)
case('frozen prompt changed',lambda p,q,r:q['request']['messages'][0].update(content='Changed prompt'),False)
case('additional privileged instruction',lambda p,q,r:q['request']['messages'].append(dict(role='developer',content='new authority')),False)
case('first message missing instructions',lambda p,q,r:q['request']['messages'][0].update(role='user'),False)
case('tool schema mutated',lambda p,q,r:q['request']['tools'][0]['function']['parameters'].update(additionalProperties=True),False)
case('tool description mutated',lambda p,q,r:q['request']['tools'][0]['function'].update(description='Different'),False)
case('duplicate tool names',lambda p,q,r:q['request']['tools'].append(deepcopy(tool)),False)
case('required tools with empty catalog',lambda p,q,r:(p.update(tools=[]),q['request'].update(tools=[],tool_choice='required')),False)
case('boolean differs from numeric tool schema value',lambda p,q,r:(p['tools'][0]['function']['parameters'].update(default=1),q['request']['tools'][0]['function']['parameters'].update(default=True)),False)
case('fractional tool schema values cannot compare as truncated integers',lambda p,q,r:(p['tools'][0]['function']['parameters'].update(minimum=1.2),q['request']['tools'][0]['function']['parameters'].update(minimum=1.9)),False)
for key,value in [('stream',True),('store',True),('n',2),('headers',{}),('api_key','synthetic-not-secret'),
                  ('base_url','https://invalid.example'),('web_search_options',{}),('functions',[]),('response_format',{}),
                  ('max_tokens',50),('reasoning_effort','high')]:
    case('unsupported native request field '+key,lambda p,q,r,key=key,value=value:q['request'].update({key:value}),False)
case('hosted tool declaration',lambda p,q,r:q['request']['tools'][0].update(type='web_search'),False)
case('multimodal input',lambda p,q,r:q['request']['messages'][1].update(content=[dict(type='image_url',image_url=dict(url='https://invalid.example'))]),False)
case('unknown continuation semantics rejected',lambda p,q,r:r['response']['choices'][0]['message'].update(reasoning_content='unsupported'),False)
case('nonempty hosted annotations rejected',lambda p,q,r:r['response']['choices'][0]['message'].update(annotations=[dict(type='url_citation')]),False)
case('streamed chunk rejected',lambda p,q,r:r['response'].update(object='chat.completion.chunk'),False)
case('multiple choices rejected',lambda p,q,r:r['response']['choices'].append(deepcopy(r['response']['choices'][0])),False)
case('nonzero choice index',lambda p,q,r:r['response']['choices'][0].update(index=1),False)
case('usage total inconsistent',lambda p,q,r:r['response']['usage'].update(total_tokens=119),False)
case('usage detail exceeds total',lambda p,q,r:r['response']['usage']['completion_tokens_details'].update(reasoning_tokens=21),False)
case('audio usage rejected',lambda p,q,r:r['response']['usage']['prompt_tokens_details'].update(audio_tokens=1),False)
case('missing usage field within present object',lambda p,q,r:r['response']['usage'].pop('prompt_tokens'),False)
case('usage exceeds requested completion bound',lambda p,q,r:r['response']['usage'].update(completion_tokens=1025,total_tokens=1125),False)
case('duplicate tool call IDs reject entire response',base=with_calls([call(),call()]),valid=False)
case('tool finish without tool calls',lambda p,q,r:r['response']['choices'][0].update(finish_reason='tool_calls'),False)
case('stop finish with tool calls',lambda p,q,r:r['response']['choices'][0].update(finish_reason='stop'),False,base=with_calls())
case('refusal mixed with completed tool calls',lambda p,q,r:r['response']['choices'][0]['message'].update(refusal='No'),False,base=with_calls())
case('suppressed tools still returned',lambda p,q,r:q['request'].update(tool_choice='none'),False,base=with_calls())

history = [dict(role='assistant',content=None,tool_calls=[call('old-1'),call('old-2')]),
           dict(role='tool',tool_call_id='old-1',content='{"snapshots":[]}'),
           dict(role='tool',tool_call_id='old-2',content='{"status":"not_executed"}')]
case('complete ordered native tool history',lambda p,q,r:q['request']['messages'].extend(deepcopy(history)))
case('missing tool result',lambda p,q,r:q['request']['messages'].extend(deepcopy(history[:-1])),False)
case('reordered tool results',lambda p,q,r:q['request']['messages'].extend(deepcopy([history[0],history[2],history[1]])),False)
case('orphan tool result',lambda p,q,r:q['request']['messages'].append(deepcopy(history[1])),False)
case('duplicate historical call IDs',lambda p,q,r:q['request']['messages'].extend(deepcopy(history+history)),False)
case('ordinary message interrupts pending tools',lambda p,q,r:q['request']['messages'].extend(deepcopy([history[0],dict(role='user',content='continue')]+history[1:])),False)
case('new result reuses historical tool ID',lambda p,q,r:q['request']['messages'].extend(deepcopy(history)),False,base=with_calls([call('old-1')]))
# Exercise the ordinary entry points without installed policy as a separate gate.
for existing in list(cases):
    if existing['name'] in ['native text and usage preserved','multiple native tools','missing usage is explicit unknown',
                           'additional privileged instruction','usage total inconsistent','result changes profile',
                           'duplicate tool call IDs reject entire response','missing tool result','reordered tool results',
                           'new result reuses historical tool ID','streamed chunk rejected','unsupported native request field headers']:
        cloned = deepcopy(existing); cloned.update(name='ordinary envelope: '+existing['name'],mode='wire');cases.append(cloned)


def continuation(name, result=None, results=None, valid=True):
    r = deepcopy(result or with_calls([call(),call('call-2')]))
    items = deepcopy(results if results is not None else [dict(tool_call_id='call-1',content='{"snapshots":[]}'),
                     dict(tool_call_id='call-2',content='{"status":"not_executed","code":"TARGET_REVISION_CHANGED"}')])
    item = dict(name=name,mode='continuation',policy=deepcopy(policy),request=deepcopy(request),result=r,tool_results=items,valid=valid)
    if valid:
        message=r['response']['choices'][0]['message']
        assistant={k:message[k] for k in ('role','content','refusal','tool_calls') if k in message and not(k=='tool_calls' and message[k] is None)}
        segment=[assistant]+[dict(role='tool',**v) for v in items]
        # All keys in this projection are ASCII; compact sorted JSON pins JCS bytes
        # independently of either production implementation.
        item['segment_json']=json.dumps(segment,ensure_ascii=False,sort_keys=True,separators=(',',':'))
    cases.append(item)


restore_result = next(c['response']['result'] for c in json.loads((ROOT/'ordinary-protocol.json').read_text())
                      if c.get('valid') and c.get('response',{}).get('operation')=='engine.restore_request')
skipped_result = deepcopy(json.loads((ROOT/'restore-batch-not-executed.json').read_text())[0]['instance'])
skipped_result.update(transition_receipt=restore_result['transition_receipt'],
                      previous_run_revision=restore_result['previous_run_revision'],run_revision=restore_result['run_revision'])
continuation('complete restore and skipped call segment',result=with_calls([
    call(name='restore_request',arguments='{"source_session":"source-1","checkpoint_id":"checkpoint-1"}'),
    call('call-2',name='attempt_execute',arguments='{"intentionally_not_validated":true}')]),results=[
        dict(tool_call_id='call-1',content=json.dumps(restore_result,separators=(',',':'))),
        dict(tool_call_id='call-2',content=json.dumps(skipped_result,separators=(',',':')))])
for native_name in ('restore_request','attempt_execute'):
    declaration=deepcopy(tool)
    declaration['function'].update(name=native_name,description='Synthetic '+native_name+' fixture.')
    cases[-1]['policy']['tools'].append(deepcopy(declaration))
    cases[-1]['request']['request']['tools'].append(deepcopy(declaration))
continuation('wrong correlated result ID',results=[dict(tool_call_id='wrong',content='ok'),dict(tool_call_id='call-2',content='ok')],valid=False)
continuation('missing correlated result',results=[],valid=False)
continuation('reordered correlated results',results=[dict(tool_call_id='call-2',content='ok'),dict(tool_call_id='call-1',content='ok')],valid=False)
continuation('invalid local arguments remain exact in followup',result=with_calls([call(arguments='{"truncated":')]),results=[dict(tool_call_id='call-1',content='Invalid arguments: <retry> café\u2028')])
continuation('text-only continuation',result=response,results=[])
empty=deepcopy(response);empty['response']['choices'][0]['message']['tool_calls']=[]
continuation('empty native tool array continuation',result=empty,results=[])
refusal=deepcopy(response);refusal['response']['choices'][0]['message'].update(refusal='No',content=None)
continuation('refusal continuation',result=refusal,results=[])
truncated=with_calls();truncated['response']['choices'][0]['finish_reason']='length'
continuation('truncated calls cannot become continuation effects',result=truncated,results=[dict(tool_call_id='call-1',content='ok')],valid=False)
unknown=with_calls();unknown['response']['usage']=None
continuation('unknown usage cannot become continuation effects',result=unknown,results=[dict(tool_call_id='call-1',content='ok')],valid=False)

context=json.loads((ROOT/'engine-context-example.json').read_text())
context['model']=dict(binding,model_id=policy['request_model'],features=['text','function-tools'],codec_settings=dict(
    instruction_role=policy['instruction_role'],response_models=policy['response_models'],max_completion_tokens=policy['max_completion_tokens'],
    tools_digest='sha256:'+hashlib.sha256(json.dumps(policy['tools'],ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()).hexdigest()))
context['operations']=sorted(set(context['operations']+['engine.model_generate']))
prompt_bytes=policy['prompt'].encode()
descriptor=dict(size_bytes=len(prompt_bytes),digest='sha256:'+hashlib.sha256(prompt_bytes).hexdigest())
context['prompt']['provenance'].update(base=descriptor,effective=descriptor)


def context_case(name,mutate=lambda c:None,valid=True):
    item=dict(name=name,mode='context',context=deepcopy(context),tools=deepcopy(policy['tools']),prompt=policy['prompt'],
              policy=deepcopy(policy),request=deepcopy(request),valid=valid)
    mutate(item);cases.append(item)


context_case('build identical safe policy from startup context')
context_case('startup missing selected codec settings',lambda c:c['context']['model'].pop('codec_settings'),False)
context_case('startup invalid instruction role',lambda c:c['context']['model']['codec_settings'].update(instruction_role='user'),False)
context_case('startup zero output ceiling',lambda c:c['context']['model']['codec_settings'].update(max_completion_tokens=0),False)
context_case('startup missing accepted response IDs',lambda c:c['context']['model']['codec_settings'].pop('response_models'),False)
context_case('startup excludes authority fields',lambda c:c['context']['model']['codec_settings'].update(headers={}),False)
context_case('startup frozen prompt mismatched',lambda c:c.update(prompt='Wrong prompt'),False)
context_case('startup tool projection pin mismatched',lambda c:c['context']['model']['codec_settings'].update(tools_digest='sha256:'+'2'*64),False)
context_case('startup model generation not admitted',lambda c:c['context']['operations'].remove('engine.model_generate'),False)
context_case('startup unsupported codec',lambda c:(c['context']['model'].update(codec_id='unimplemented'),c['context']['model'].pop('codec_settings')),False)
context_case('startup native tools changed',lambda c:c['tools'][0]['function'].update(description='Changed'),False)

(ROOT/'model-codec.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2)+'\n')
ordinary_path=ROOT/'ordinary-protocol.json'
ordinary=json.loads(ordinary_path.read_text())
names={'model relay native exchange','model relay unknown provider outcome','model relay cannot continue unknown provider outcome'}
ordinary=[c for c in ordinary if c['name'] not in names]
envelope=dict(api_version='operator.dev/engine-pipe/v1alpha1',seq=0,campaign_id='campaign-1',
              launch_id='launch-1',run_revision=1,call_id='model-call-1',operation_id='model-operation-1',operation='engine.model_generate')
wire_request=dict(envelope,kind='request',timeout_ms=120000,body=request)
ordinary.append(dict(name='model relay native exchange',request=wire_request,response=dict(envelope,kind='response',result=response),valid=True))
error=dict(code='OUTCOME_UNKNOWN',message='Model execution outcome is unknown.',effect_state='unknown',disposition='terminate')
ordinary.append(dict(name='model relay unknown provider outcome',request=wire_request,response=dict(envelope,kind='response',error=error),valid=True))
ordinary.append(dict(name='model relay cannot continue unknown provider outcome',request=wire_request,
                     response=dict(envelope,kind='response',error=dict(error,disposition='gap-and-continue')),valid=False))
ordinary_path.write_text(json.dumps(ordinary,indent=2)+'\n')
print(f'{len(cases)} model codec cases')
