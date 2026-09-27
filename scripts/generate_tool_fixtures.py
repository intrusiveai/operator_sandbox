"""Independent fixed-tool projection and local argument conformance vectors."""
from copy import deepcopy
import hashlib
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas'
branches=json.loads((ROOT/'model-tool-arguments.schema.json').read_text())['oneOf']
operations=[o['name'] for o in json.loads((ROOT/'operations.json').read_text())['operations']]
codecs=['openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1','bedrock-converse-text-tools-v1','gemini-text-tools-v1']
cases=[]
for codec in codecs:
 for label,enabled in [('all',operations),('local-only',[]),('incomplete-upload',[o for o in operations if o!='engine.artifact_commit'])]:
    tools=[];names=[];functions=[]
    for b in branches:
        if not set(b['x-operator-required-operations']).issubset(enabled):continue
        name=b['properties']['name']['const'];names.append(name);parameters=b['properties']['arguments'];description=b['description']
        if codec=='openai-chat-text-tools-v1':tools.append({'type':'function','function':{'name':name,'description':description,'parameters':parameters,'strict':False}})
        elif codec=='openai-responses-text-tools-v1':tools.append({'type':'function','name':name,'description':description,'parameters':parameters,'strict':False})
        elif codec=='anthropic-messages-text-tools-v1':tools.append({'name':name,'description':description,'input_schema':parameters})
        elif codec=='bedrock-converse-text-tools-v1':tools.append({'toolSpec':{'name':name,'description':description,'inputSchema':{'json':parameters}}})
        else:functions.append({'name':name,'description':description,'parametersJsonSchema':parameters})
    if functions:tools=[{'functionDeclarations':functions}]
    raw=json.dumps(tools,sort_keys=True,separators=(',',':'),ensure_ascii=False).encode()
    cases.append(dict(name=codec+'/'+label,mode='projection',codec=codec,operations=enabled,names=names,digest='sha256:'+hashlib.sha256(raw).hexdigest(),size_bytes=len(raw),valid=True))
for name,codec,ops in [('unknown-codec','unsupported',operations),('unknown-operation',codecs[0],['engine.shell']),('duplicate-operation',codecs[0],operations+operations),('unsorted-operations',codecs[0],list(reversed(operations)))]:
 cases.append(dict(name=name,mode='projection',codec=codec,operations=ops,valid=False))
ordinary=json.loads((ROOT/'fixtures/ordinary-protocol.json').read_text())
def body(op):
    return deepcopy(next(c['request']['body'] for c in ordinary if isinstance(c.get('request'),dict) and c['request'].get('operation')==op and c['valid']))
def arg(name,tool,value,valid=True):cases.append(dict(name=name,mode='arguments',tool=tool,arguments=value,valid=valid))
arg('bounded reference','reference_read',dict(reference_id='ref-1',offset=0,max_bytes=262144))
arg('no arbitrary path','reference_read',dict(path='/etc/passwd',offset=0,max_bytes=1),False)
arg('bounded reference ceiling','reference_read',dict(reference_id='ref-1',offset=0,max_bytes=262145),False)
for content in [dict(encoding='utf8',text='payload'),dict(encoding='json',value={'payload':'data'}),dict(encoding='json',value='JSON string'),dict(encoding='base64',data='eA==')]:
 arg('data artifact '+str(content),'artifact_publish',dict(purpose='payload',media_type='application/json',content=content))
arg('model cannot publish conclusion directly','artifact_publish',dict(purpose='conclusion',media_type='application/json',content=dict(encoding='json',value={})),False)
arg('no artifact source file','artifact_publish',dict(purpose='payload',media_type='application/json',source_file='/tmp/file'),False)
attempt=body('engine.attempt_execute')
for key in ('api_version','kind','generator','request_id','attempt_id','attempt_index'):attempt.pop(key)
arg('complete tactical arguments','attempt_execute',attempt)
for key,value in [('api_version','other'),('generator',{}),('attempt_index',1),('request_id','r1'),('attempt_id','a1')]:
 arg('reserved '+key,'attempt_execute',dict(attempt,**{key:value}),False)
for name in ('injection_delete','observation_read','record_append','restore_request','snapshot_request','snapshot_list','snapshot_inspect'):
 arg(name+' arguments',name,body('engine.'+name))
draft=json.loads((ROOT/'fixtures/conclusion-example.json').read_text())
for key in ('api_version','kind','binding'):draft.pop(key)
arg('structured conclusion draft','request_stop',draft)
arg('model cannot supply conclusion binding','request_stop',dict(draft,binding={}),False)
arg('record wrapper cannot fabricate finalization','record_append',dict(record_kind='conclusion',record={}),False)
arg('internal model operation hidden','model_generate',{},False)
arg('artifact chunk hidden','artifact_put_part',{},False)
arg('shell hidden','exec',dict(command='true'),False)
arg('non-object arguments','snapshot_request',[],False)
(ROOT/'fixtures/model-tools.json').write_text(json.dumps(cases,indent=2,ensure_ascii=False)+'\n')
print(len(cases),'fixed tool fixtures')
