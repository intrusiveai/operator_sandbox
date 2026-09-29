"""Generate closed native model codecs and correlated model relay schemas."""
import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]/'schemas'
PREFIX = 'urn:operator:schema:'
COMMON = PREFIX+'openai-chat-common:v1alpha1#/$defs/'
WIRE = PREFIX+'wire-common:v1alpha1#/$defs/'
MAX = 2**53-1


def obj(properties, required=None):
    return dict(type='object', additionalProperties=False, properties=properties,
                required=list(properties) if required is None else required)


def text(maximum=1048576, minimum=0):
    return dict(type='string', minLength=minimum, maxLength=maximum)


def ref(name): return {'$ref': COMMON+name}
def nullable(schema): return {'anyOf':[schema, {'type':'null'}]}
def integer(minimum=0): return dict(type='integer', minimum=minimum, maximum=MAX)
def array(items, minimum=0, maximum=1024):
    return dict(type='array',items=items,minItems=minimum,maxItems=maximum)


def documents():
    name = dict(type='string',minLength=1,maxLength=64,pattern='^[A-Za-z0-9_-]+$',**{'not':{'pattern':'[\\r\\n]'}})
    opaque = {'$ref':WIRE+'id'}
    model = dict(type='string',minLength=1,maxLength=256,pattern='^[^\\x00-\\x20\\x7f]+$',**{'not':{'pattern':'[\\r\\n]'}})
    call = obj(dict(id=opaque,type={'const':'function'},function=obj(dict(name=name,arguments=text()))))
    function = obj(dict(name=name,description=text(8192),parameters={'type':'object'},strict={'const':False}),['name','parameters'])
    tool = obj(dict(type={'const':'function'},function=function))
    assistant = obj(dict(role={'const':'assistant'},content=nullable(text()),refusal=nullable(text()),
                         tool_calls=array(ref('call'))),['role','content'])
    response_assistant = obj(dict(assistant['properties'], annotations=dict(type='array',maxItems=0),
                                  audio={'type':'null'},function_call={'type':'null'},
                                  tool_calls=nullable(array(ref('call')))),['role','content'])
    message = {'oneOf':[
        obj(dict(role={'enum':['system','developer','user']},content=text())),
        assistant,
        obj(dict(role={'const':'tool'},tool_call_id=opaque,content=text()))]}
    usage = obj(dict(prompt_tokens=integer(),completion_tokens=integer(),total_tokens=integer(),
                     prompt_tokens_details=nullable(obj(dict(cached_tokens=integer(),audio_tokens={'const':0}),[])),
                     completion_tokens_details=nullable(obj(dict(reasoning_tokens=integer(),audio_tokens={'const':0},
                         accepted_prediction_tokens={'const':0},rejected_prediction_tokens={'const':0}),[]))),
                ['prompt_tokens','completion_tokens','total_tokens'])
    tools = array(ref('tool'),0,128)
    request = obj(dict(model=ref('model'),messages=array(ref('message'),2,4096),
                       max_completion_tokens=integer(1),stream={'const':False},store={'const':False},
                       n={'const':1},parallel_tool_calls={'const':True},tools=tools,
                       tool_choice={'enum':['auto','none','required']}))
    response = obj(dict(id=opaque,object={'const':'chat.completion'},created=integer(),model=ref('model'),
                        choices=array(obj(dict(index={'const':0},message=ref('response_assistant'),
                            finish_reason={'enum':['stop','tool_calls','length','content_filter']},logprobs={'type':'null'}),
                            ['index','message','finish_reason']),1,1),
                        usage=nullable(ref('usage')),system_fingerprint=nullable(text(256)),
                        service_tier=nullable({'enum':['auto','default','flex','scale','priority']})),
                   ['id','object','created','model','choices'])
    binding = dict(codec_id={'const':'openai-chat-text-tools-v1'},profile_id={'$ref':WIRE+'id'},profile_digest={'$ref':WIRE+'digest'})
    settings = obj(dict(instruction_role={'enum':['system','developer']},max_completion_tokens=integer(1),
                        response_models=dict(array(ref('model'),1,32),uniqueItems=True),tools_digest={'$ref':WIRE+'digest'}))
    result = {
        'openai-chat-common':{'$defs':dict(call=call,tool=tool,message=message,response_assistant=response_assistant,usage=usage,model=model,settings=settings)},
        'openai-chat-request':request,
        'openai-chat-response':response,
        'engine-model-generate-request':obj(dict(binding,request={'$ref':PREFIX+'openai-chat-request:v1alpha1'})),
        'engine-model-generate-result':obj(dict(binding,receipt_id={'$ref':WIRE+'id'},response={'$ref':PREFIX+'openai-chat-response:v1alpha1'})),
        'model-codec-policy':obj(dict(binding,request_model=ref('model'),response_models=dict(array(ref('model'),1,32),uniqueItems=True),
                                      instruction_role={'enum':['system','developer']},prompt=text(131072),
                                      max_completion_tokens=integer(1),tools=tools)),
    }
    # Each native family retains its own request/response shape. The common
    # envelope selects a closed branch by codec ID, never by a guest URL.
    for key in ('engine-model-generate-request','engine-model-generate-result','model-codec-policy'):
        result[key]={'oneOf':[result[key]]}
    for stem,codec,builder in [('anthropic-messages','anthropic-messages-text-tools-v1',anthropic_documents),
                               ('bedrock-converse','bedrock-converse-text-tools-v1',bedrock_documents),
                               ('gemini','gemini-text-tools-v1',gemini_documents),
                               ('openai-responses','openai-responses-text-tools-v1',responses_documents)]:
        extra,native_policy=builder(name,model)
        result.update(extra)
        native_binding=dict(binding,codec_id={'const':codec})
        for kind,field in [('request','request'),('result','response')]:
            properties=dict(native_binding,**{field:{'$ref':PREFIX+stem+'-'+field+':v1alpha1'}})
            if kind=='result': properties['receipt_id']={'$ref':WIRE+'id'}
            result['engine-model-generate-'+kind]['oneOf'].append(obj(properties))
        result['model-codec-policy']['oneOf'].append(native_policy)
    return {stem:dict({'$schema':'https://json-schema.org/draft/2020-12/schema',
                      '$id':PREFIX+stem+':v1alpha1',
                      '$comment':'Generated by scripts/generate_model_schemas.py; see MODEL_CODEC_CONTRACT.md.'},**body)
            for stem,body in result.items()}


def anthropic_documents(name, model):
    common=PREFIX+'anthropic-messages-common:v1alpha1#/$defs/'
    r=lambda name:{'$ref':common+name}
    text_block=obj(dict(type={'const':'text'},text=text(),citations=nullable(dict(type='array',maxItems=0))),['type','text'])
    thinking_block=obj(dict(type={'const':'thinking'},thinking=text(),signature=text(1048576,1)))
    redacted=obj(dict(type={'const':'redacted_thinking'},data=text(1048576,1)))
    tool_use=obj(dict(type={'const':'tool_use'},id={'$ref':WIRE+'id'},name=name,input={'type':'object'},
                      caller=obj(dict(type={'const':'direct'}))),['type','id','name','input'])
    tool_result=obj(dict(type={'const':'tool_result'},tool_use_id={'$ref':WIRE+'id'},content=text(),is_error={'type':'boolean'}),
                    ['type','tool_use_id','content'])
    assistant_blocks=array({'oneOf':[r('text'),r('thinking_block'),r('redacted_thinking'),r('tool_use')]},0,1024)
    user_blocks=array({'oneOf':[r('text'),r('tool_result')]},1,1024)
    message={'oneOf':[
        obj(dict(role={'const':'user'},content={'oneOf':[text(),user_blocks]})),
        obj(dict(role={'const':'assistant'},content=assistant_blocks))]}
    tool=obj(dict(name=name,description=text(8192),input_schema={'type':'object'},type={'const':'custom'}),['name','input_schema'])
    tools=array(r('tool'),0,128)
    thinking={'oneOf':[obj(dict(type={'const':'disabled'})),obj(dict(type={'const':'adaptive'})),
                       obj(dict(type={'const':'enabled'},budget_tokens=integer(1024)))]}
    usage=obj(dict(input_tokens=integer(),output_tokens=integer(),cache_creation_input_tokens=nullable(integer()),
        cache_read_input_tokens=nullable(integer()),cache_creation=nullable(obj(dict(ephemeral_1h_input_tokens=integer(),ephemeral_5m_input_tokens=integer()))),
        output_tokens_details=nullable(obj(dict(thinking_tokens=integer()))),
        server_tool_use=nullable(obj(dict(web_search_requests={'const':0},web_fetch_requests={'const':0}),[])),
        service_tier=nullable(text(128)),inference_geo=nullable(text(128))),['input_tokens','output_tokens'])
    settings=obj(dict(max_tokens=integer(1),thinking=r('thinking'),response_models=dict(array(model,1,32),uniqueItems=True),tools_digest={'$ref':WIRE+'digest'}))
    request=obj(dict(model=model,system=text(131072),messages=array(r('message'),1,4096),max_tokens=integer(1),stream={'const':False},
                     thinking=r('thinking'),tools=tools,tool_choice=obj(dict(type={'enum':['auto','any','none']}))))
    response=obj(dict(id={'$ref':WIRE+'id'},type={'const':'message'},role={'const':'assistant'},model=model,content=assistant_blocks,
        stop_reason={'enum':['end_turn','tool_use','max_tokens','refusal','pause_turn','model_context_window_exceeded']},stop_sequence={'type':'null'},
        usage=nullable(r('usage')),container={'type':'null'},
        stop_details=nullable(obj(dict(type={'const':'refusal'},category=text(256),explanation=text()),['type','explanation'])),
        diagnostics=nullable(obj(dict(cache_miss_reason=nullable(obj(dict(type=text(128),cache_missed_input_tokens=integer())))),[]))),
        ['id','type','role','model','content','stop_reason','stop_sequence'])
    binding=dict(codec_id={'const':'anthropic-messages-text-tools-v1'},profile_id={'$ref':WIRE+'id'},profile_digest={'$ref':WIRE+'digest'})
    policy=obj(dict(binding,request_model=model,response_models=dict(array(model,1,32),uniqueItems=True),prompt=text(131072),
                    max_tokens=integer(1),thinking=r('thinking'),tools=tools))
    return {'anthropic-messages-common':{'$defs':dict(text=text_block,thinking_block=thinking_block,redacted_thinking=redacted,
        tool_use=tool_use,tool_result=tool_result,message=message,tool=tool,thinking=thinking,usage=usage,settings=settings)},
        'anthropic-messages-request':request,'anthropic-messages-response':response},policy


def bedrock_documents(name,model):
    common=PREFIX+'bedrock-converse-common:v1alpha1#/$defs/'
    r=lambda name:{'$ref':common+name}
    txt=obj(dict(text=text()))
    tool_use=obj(dict(toolUse=obj(dict(toolUseId={'$ref':WIRE+'id'},name=name,input={'type':'object'}))))
    tool_result=obj(dict(toolResult=obj(dict(toolUseId={'$ref':WIRE+'id'},content=array(txt,1,1),status={'enum':['success','error']}),['toolUseId','content'])))
    reasoning=obj(dict(reasoningContent={'oneOf':[
        obj(dict(reasoningText=obj(dict(text=text(),signature=text(1048576,1)),['text']))),
        obj(dict(redactedContent=text(1048576,1)))]}))
    assistant=obj(dict(role={'const':'assistant'},content=array({'oneOf':[r('text'),r('tool_use'),r('reasoning')]},0,1024)))
    message={'oneOf':[assistant,obj(dict(role={'const':'user'},content=array({'oneOf':[r('text'),r('tool_result')]},1,1024)))]}
    tool=obj(dict(toolSpec=obj(dict(name=name,description=text(8192),inputSchema=obj(dict(json={'type':'object'})),strict={'const':False}),['name','inputSchema'])))
    tools=array(r('tool'),0,128)
    tool_config=obj(dict(tools=array(r('tool'),1,128),toolChoice={'oneOf':[obj(dict(auto=obj({}))),obj(dict(any=obj({})))]}))
    usage=obj(dict(inputTokens=integer(),outputTokens=integer(),totalTokens=integer(),cacheReadInputTokens=integer(),cacheWriteInputTokens=integer(),serverToolUsage=obj({}),
        cacheDetails=array(obj(dict(inputTokens=integer(),ttl={'enum':['1h','5m']})),0,2)),['inputTokens','outputTokens','totalTokens'])
    settings=obj(dict(max_tokens=integer(1),tools_digest={'$ref':WIRE+'digest'}))
    request=obj(dict(system=array(obj(dict(text=text(131072))),1,1),messages=array(r('message'),1,4096),
                     inferenceConfig=obj(dict(maxTokens=integer(1))),toolConfig=r('tool_config')),['system','messages','inferenceConfig'])
    response=obj(dict(output=obj(dict(message=r('assistant'))),stopReason={'enum':['end_turn','tool_use','max_tokens','stop_sequence',
        'guardrail_intervened','content_filtered','malformed_model_output','malformed_tool_use','model_context_window_exceeded']},
        usage=nullable(r('usage')),metrics=obj(dict(latencyMs=integer())),performanceConfig=obj(dict(latency={'enum':['standard','optimized']})),
        serviceTier=obj(dict(type=text(128))),additionalModelResponseFields=nullable(obj({}))),['output','stopReason'])
    binding=dict(codec_id={'const':'bedrock-converse-text-tools-v1'},profile_id={'$ref':WIRE+'id'},profile_digest={'$ref':WIRE+'digest'})
    policy=obj(dict(binding,request_model=model,prompt=text(131072),max_tokens=integer(1),tools=tools))
    return {'bedrock-converse-common':{'$defs':dict(text=txt,tool_use=tool_use,tool_result=tool_result,reasoning=reasoning,
        assistant=assistant,message=message,tool=tool,tool_config=tool_config,usage=usage,settings=settings)},
        'bedrock-converse-request':request,'bedrock-converse-response':response},policy


def gemini_documents(name,model):
    common=PREFIX+'gemini-common:v1alpha1#/$defs/'
    r=lambda name:{'$ref':common+name}
    metadata=dict(thought={'type':'boolean'},thoughtSignature=text(1048576,1))
    txt=obj(dict(text=text(),**metadata),['text'])
    call=obj(dict(functionCall=obj(dict(name=name,args={'type':'object'},id={'$ref':WIRE+'id'}),['name']),**metadata),['functionCall'])
    signature=obj(metadata,['thoughtSignature'])
    result=obj(dict(functionResponse=obj(dict(name=name,id={'$ref':WIRE+'id'},response=obj(dict(output=text()))),['name','response'])))
    assistant=obj(dict(role={'const':'model'},parts=array({'oneOf':[r('text'),r('call'),r('signature')]},0,1024)))
    message={'oneOf':[assistant,obj(dict(role={'const':'user'},parts=array({'oneOf':[obj(dict(text=text())),r('result')]},1,1024)))]}
    declaration=obj(dict(name=name,description=text(8192),parametersJsonSchema={'type':'object'}),['name','parametersJsonSchema'])
    tools=array(obj(dict(functionDeclarations=array(declaration,1,128))),0,1)
    thinking=obj(dict(includeThoughts={'type':'boolean'},thinkingBudget=integer(-1),thinkingLevel={'enum':['MINIMAL','LOW','MEDIUM','HIGH']}),[])
    thinking['not']={'required':['thinkingBudget','thinkingLevel']}
    rating=obj(dict(category=text(128,1),probability={'enum':['HARM_PROBABILITY_UNSPECIFIED','NEGLIGIBLE','LOW','MEDIUM','HIGH']},
        blocked={'type':'boolean'},probabilityScore={'type':'number','minimum':0,'maximum':1},severity=text(128),severityScore={'type':'number','minimum':0,'maximum':1}),['category','probability'])
    detail=array(obj(dict(modality={'const':'TEXT'},tokenCount=integer())),0,1)
    usage=obj(dict(promptTokenCount=integer(),candidatesTokenCount=integer(),thoughtsTokenCount=integer(),totalTokenCount=integer(),
        cachedContentTokenCount=integer(),toolUsePromptTokenCount={'const':0},promptTokensDetails=detail,cacheTokensDetails=detail,
        candidatesTokensDetails=detail,toolUsePromptTokensDetails=dict(type='array',maxItems=0),serviceTier=text(128)),['promptTokenCount','totalTokenCount'])
    finish=['STOP','MAX_TOKENS','SAFETY','RECITATION','LANGUAGE','OTHER','BLOCKLIST','PROHIBITED_CONTENT','SPII',
        'MALFORMED_FUNCTION_CALL','IMAGE_SAFETY','IMAGE_PROHIBITED_CONTENT','IMAGE_OTHER','NO_IMAGE','IMAGE_RECITATION',
        'UNEXPECTED_TOOL_CALL','TOO_MANY_TOOL_CALLS','MISSING_THOUGHT_SIGNATURE','MALFORMED_RESPONSE','ESCALATION','PUP_LIMITED_DISABLED']
    candidate=obj(dict(content=r('assistant'),finishReason={'enum':finish},index={'const':0},finishMessage=text(),
        safetyRatings=array(rating,0,32),tokenCount=integer(),avgLogprobs={'type':'number'},
        citationMetadata=obj(dict(citationSources=array(obj(dict(startIndex=integer(),endIndex=integer(),uri=text(8192),license=text(8192)),[]),0,128)),[])),['finishReason'])
    feedback=obj(dict(blockReason={'enum':['BLOCK_REASON_UNSPECIFIED','SAFETY','OTHER','BLOCKLIST','PROHIBITED_CONTENT','IMAGE_SAFETY']},
        blockReasonMessage=text(),safetyRatings=array(rating,0,32)),[])
    settings=obj(dict(max_output_tokens=integer(1),thinking_config=r('thinking'),response_models=dict(array(model,1,32),uniqueItems=True),tools_digest={'$ref':WIRE+'digest'}))
    request=obj(dict(systemInstruction=obj(dict(parts=array(obj(dict(text=text(131072))),1,1))),contents=array(r('message'),1,4096),
        generationConfig=obj(dict(maxOutputTokens=integer(1),candidateCount={'const':1},thinkingConfig=r('thinking'))),tools=tools,
        toolConfig=obj(dict(functionCallingConfig=obj(dict(mode={'enum':['AUTO','ANY','NONE']}))))))
    response=obj(dict(candidates=array(candidate,0,1),promptFeedback=feedback,usageMetadata=nullable(r('usage')),modelVersion=model,
        responseId=text(512,1),createTime=text(128),modelStatus=obj(dict(modelStage=text(128),retirementTime=text(128),message=text()),[])),[])
    binding=dict(codec_id={'const':'gemini-text-tools-v1'},profile_id={'$ref':WIRE+'id'},profile_digest={'$ref':WIRE+'digest'})
    policy=obj(dict(binding,request_model=model,response_models=dict(array(model,1,32),uniqueItems=True),prompt=text(131072),
        max_output_tokens=integer(1),thinking_config=r('thinking'),tools=tools))
    return {'gemini-common':{'$defs':dict(text=txt,call=call,signature=signature,result=result,assistant=assistant,message=message,
        usage=usage,thinking=thinking,settings=settings)},'gemini-request':request,'gemini-response':response},policy


def responses_documents(name,model):
    common=PREFIX+'openai-responses-common:v1alpha1#/$defs/'
    r=lambda name:{'$ref':common+name}
    opaque={'$ref':WIRE+'id'}
    status={'enum':['in_progress','completed','incomplete']}
    direct=nullable(obj(dict(type={'const':'direct'})))
    call=obj(dict(type={'const':'function_call'},call_id=opaque,name=name,arguments=text(),id=nullable(opaque),status=nullable(status),
        caller=direct,namespace={'type':'null'},**{'async':nullable({'const':False})}),['type','call_id','name','arguments'])
    txt=obj(dict(type={'const':'output_text'},text=text(),annotations=dict(type='array',maxItems=0),logprobs=dict(type='array',maxItems=0)),['type','text','annotations'])
    refusal=obj(dict(type={'const':'refusal'},refusal=text()))
    message=obj(dict(type={'const':'message'},id=opaque,role={'const':'assistant'},status=status,
        content=array({'oneOf':[r('text'),r('refusal')]},0,1024),phase=nullable({'enum':['commentary','final_answer']})),['type','id','role','status','content'])
    reasoning_item=obj(dict(type={'const':'reasoning'},id=opaque,summary=array(obj(dict(type={'const':'summary_text'},text=text())),0,1024),
        encrypted_content=nullable(text(1048576,1)),content=nullable(array(obj(dict(type={'const':'reasoning_text'},text=text())),0,1024)),status=nullable(status)),['type','id','summary'])
    output_item={'oneOf':[r('message'),r('reasoning_item'),r('call')]}
    user=obj(dict(type={'const':'message'},role={'const':'user'},content=text()),['role','content'])
    result=obj(dict(type={'const':'function_call_output'},call_id=opaque,output=text(),caller=direct),['type','call_id','output'])
    tool=obj(dict(type={'const':'function'},name=name,parameters={'type':'object'},strict={'const':False},description=nullable(text(8192))),['type','name','parameters','strict'])
    tools=array(r('tool'),0,128)
    reasoning=obj(dict(effort=nullable({'enum':['none','minimal','low','medium','high','xhigh','max']}),
        summary=nullable({'enum':['auto','concise','detailed']}),context=nullable({'enum':['auto','current_turn','all_turns']}),
        mode=nullable({'enum':['standard','pro']})),[])
    text_config=obj(dict(format=obj(dict(type={'const':'text'})),verbosity={'enum':['low','medium','high']}),['format'])
    usage=obj(dict(input_tokens=integer(),output_tokens=integer(),total_tokens=integer(),
        input_tokens_details=nullable(obj(dict(cached_tokens=integer(),cache_write_tokens=integer()),[])),
        output_tokens_details=nullable(obj(dict(reasoning_tokens=integer()),[]))),['input_tokens','output_tokens','total_tokens'])
    settings=obj(dict(max_output_tokens=integer(1),reasoning=nullable(r('reasoning')),response_models=dict(array(model,1,32),uniqueItems=True),tools_digest={'$ref':WIRE+'digest'}))
    request=obj(dict(model=model,instructions=text(131072),input=array({'oneOf':[r('user'),r('message'),r('reasoning_item'),r('call'),r('result')]},1,4096),
        max_output_tokens=integer(1),stream={'const':False},store={'const':False},parallel_tool_calls={'const':True},
        include=array({'const':'reasoning.encrypted_content'},1,1),truncation={'const':'disabled'},reasoning=nullable(r('reasoning')),
        tools=tools,tool_choice={'enum':['auto','none','required']},text=obj(dict(format=obj(dict(type={'const':'text'}))))))
    response=obj(dict(id=opaque,object={'const':'response'},created_at={'type':'number','minimum':0,'maximum':MAX},
        completed_at=nullable({'type':'number','minimum':0,'maximum':MAX}),model=model,status={'enum':['completed','incomplete','failed','cancelled']},
        output=array(output_item,0,1024),usage=nullable(r('usage')),error=nullable(obj(dict(code=text(128,1),message=text()))),
        incomplete_details=nullable(obj(dict(reason=nullable({'enum':['max_output_tokens','max_messages','content_filter','steered']})),[])),
        instructions=nullable(text(131072)),tools=tools,tool_choice={'enum':['auto','none','required']},parallel_tool_calls={'const':True},
        max_output_tokens=nullable(integer(1)),store={'const':False},background=nullable({'const':False}),previous_response_id={'type':'null'},
        conversation={'type':'null'},prompt={'type':'null'},max_tool_calls={'type':'null'},moderation={'type':'null'},
        metadata=nullable(obj({})),reasoning=nullable(r('reasoning')),text=nullable(r('text_config')),truncation=nullable({'const':'disabled'}),
        temperature=nullable({'type':'number','minimum':0,'maximum':2}),top_p=nullable({'type':'number','minimum':0,'maximum':1}),
        top_logprobs=nullable({'const':0}),service_tier=nullable(text(128)),user=nullable(text(256)),safety_identifier=nullable(text(256)),
        prompt_cache_key=nullable(text(512)),prompt_cache_retention=nullable({'enum':['in_memory','24h']}),
        prompt_cache_options=nullable(obj(dict(mode={'const':'implicit'},ttl={'const':'30m'},comparison_response_id={'type':'null'}),['mode','ttl'])),
        prompt_cache_diagnostics={'type':'null'},access_programs=nullable(obj(dict(cyber={'enum':['standard','daybreak_blue','daybreak_red']})))),
        ['id','object','created_at','model','status','output'])
    binding=dict(codec_id={'const':'openai-responses-text-tools-v1'},profile_id={'$ref':WIRE+'id'},profile_digest={'$ref':WIRE+'digest'})
    policy=obj(dict(binding,request_model=model,response_models=dict(array(model,1,32),uniqueItems=True),prompt=text(131072),
        max_output_tokens=integer(1),reasoning=nullable(r('reasoning')),tools=tools))
    return {'openai-responses-common':{'$defs':dict(call=call,text=txt,refusal=refusal,message=message,reasoning_item=reasoning_item,user=user,result=result,
        tool=tool,reasoning=reasoning,text_config=text_config,usage=usage,settings=settings)},
        'openai-responses-request':request,'openai-responses-response':response},policy


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check',action='store_true')
    args = parser.parse_args()
    catalog = json.loads((ROOT/'catalog.json').read_text())
    for stem, document in documents().items():
        filename = stem+'.schema.json'
        rendered = json.dumps(document,indent=2)+'\n'
        path = ROOT/filename
        if args.check:
            if not path.exists() or path.read_text()!=rendered or catalog.get(document['$id'])!=filename:
                raise SystemExit('Stale model schema: '+filename)
        else:
            path.write_text(rendered)
            catalog[document['$id']] = filename
    if not args.check:
        (ROOT/'catalog.json').write_text(json.dumps(dict(sorted(catalog.items())),indent=2)+'\n')


if __name__=='__main__': main()
