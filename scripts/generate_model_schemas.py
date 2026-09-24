"""Generate the closed initial Chat Completions codec and model relay schemas."""
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
    return {stem:dict({'$schema':'https://json-schema.org/draft/2020-12/schema',
                      '$id':PREFIX+stem+':v1alpha1',
                      '$comment':'Generated by scripts/generate_model_schemas.py; see MODEL_CODEC_CONTRACT.md.'},**body)
            for stem,body in result.items()}


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
