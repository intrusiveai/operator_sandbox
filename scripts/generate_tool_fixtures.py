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
 for label,enabled in [('all',operations),('local-only',[]),('no-injection',[o for o in operations if o!='engine.injection_delete']),('incomplete-upload',[o for o in operations if o!='engine.artifact_commit'])]:
    tools=[];names=[];functions=[]
    for b in branches:
        if not set(b['x-operator-required-operations']).issubset(enabled):continue
        name=b['properties']['name']['const'];names.append(name);parameters=b.get('x-operator-provider-parameters',b['properties']['arguments']);description=b['description']
        if codec in codecs[:2]:parameters=b.get('x-operator-openai-parameters',parameters)
        parameters=deepcopy(parameters)
        if name=='attempt_execute' and 'engine.injection_delete' not in enabled:
            for field in ('setup','injection','retain_injections'):parameters['properties'].pop(field,None)
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
def arg(name,tool,value,valid=True,openai_valid=None):
    case=dict(name=name,mode='arguments',tool=tool,arguments=value,valid=valid)
    if openai_valid is not None:case['openai_valid']=openai_valid
    cases.append(case)
arg('bounded reference','reference_read',dict(reference='ref-1',offset=0,max_bytes=262144))
arg('read defaults','observation_read',dict(observation='obs-1'))
arg('no arbitrary path','reference_read',dict(path='/etc/passwd'),False)
arg('bounded reference ceiling','reference_read',dict(reference='ref-1',max_bytes=262145),False)
for content in [dict(text='payload'),dict(json={'payload':'data'}),dict(json='JSON string'),dict(json=None),dict(base64='eA==')]:
 arg('data artifact '+str(content),'artifact_publish',dict(purpose='payload',media_type='application/json',**content))
arg('no artifact source file','artifact_publish',dict(purpose='payload',media_type='application/json',source_file='/tmp/file'),False)
attempt=dict(origin='exploratory',operation_id='invoke',json={})
arg('direct invocation','attempt_execute',attempt)
def attempt_case(label,mutate,valid,projected=True):
 value=deepcopy(attempt);mutate(value);arg(label,'attempt_execute',value,valid,projected)
attempt_case('scenario association',lambda a:a.update(origin='scenario',scenario_id='scenario-1'),True)
attempt_case('parent handle',lambda a:a.update(parent='execution-1'),True)
action=dict(surface='model_tool_result',mode='simulation',scope='once',selector=dict(tool_name='search',call_ordinal='first'),placement=dict(operation='replace',pointer=''))
attempt_case('setup requires injection',lambda a:a.update(setup=[action]),False)
attempt_case('setup with injection',lambda a:a.update(setup=[action],injection=dict(json='replacement')),True)
database=dict(surface='database_response',mode='simulation',scope='once',selector=dict(service='database',call_ordinal='first'),placement=dict(operation='append_text',pointer='/rows/0/0'))
attempt_case('database result setup',lambda a:a.update(setup=[database],injection=dict(json='replacement')),True)
for field in ('method','tool_name','turn_id'):
 bad=deepcopy(database);bad['selector'][field]='invalid'
 attempt_case('database disallows '+field,lambda a:a.update(setup=[bad],injection=dict(json='replacement')),False,False)
attempt_case('no irrelevant injection',lambda a:a.update(injection=dict(json='replacement')),False)
attempt_case('no cleanup flags without setup',lambda a:a.update(retain_injections=False),False)
attempt_case('unknown attempt field',lambda a:a.update(unexpected=True),False,False)
attempt_case('missing scenario',lambda a:a.update(origin='scenario'),False)
attempt_case('forbidden scenario',lambda a:a.update(scenario_id='s'),False)
attempt_case('empty observations',lambda a:a.update(observations=[]),False,False)
attempt_case('conflicting input',lambda a:a.update(text='x'),False)
for key,value in [('input',{'encoding':'json','value':{}}),('generation',1),('thread_id','thread'),('attempt_index',1),('request_id','r1'),('attempt_id','a1')]:
 arg('obsolete '+key,'attempt_execute',dict(attempt,**{key:value}),False,False)
for name,args in [('injection_delete',dict(injection='inj-1')),('restore_request',dict(snapshot='snap-1')),('snapshot_request',{}),('snapshot_list',{}),('snapshot_inspect',dict(snapshot='snap-1'))]:arg(name+' arguments',name,args)
for kind,fields in [('hypothesis',dict(origin='exploratory',statement='Maybe.')),('progress',dict(summary='So far.')),('lineage',dict(hypothesis='hyp-1',attempt='exec-1',mutation_summary='Changed.')),('coverage',dict(entries=[]))]:
 record=dict(kind=kind,**fields);arg('record '+kind,'record_append',record,True,True)
 wrong=deepcopy(record);wrong['kind']='progress' if kind!='progress' else 'hypothesis';arg('wrong record '+kind,'record_append',wrong,False,True)
 extra=dict(record,unexpected=True);arg('unknown record field '+kind,'record_append',extra,False,False)
arg('partial progress assessment','record_append',dict(kind='progress',summary='So far.',confidence='low'),False,True)
arg('orphan gaps','record_append',dict(kind='progress',summary='So far.',gaps=[]),False,True)
arg('full progress assessment','record_append',dict(kind='progress',summary='So far.',interpretation='inconclusive',confidence='low',assurance='No evidence',assessment_summary='Untested'),True,True)
draft=dict(summary='No target experiment was run.',objectives=[dict(objective_id='objective-1',outcome='not-tested',summary='Not tested.')])
arg('minimal finish','finish',dict(finish_reason='budget-limit',**draft))
arg('honest unavailable finish','finish',dict(finish_reason='harness-error',unavailable_reason='invalid-call-limit'))
arg('finish requires one source','finish',dict(finish_reason='budget-limit'),False,True)
arg('finish conflicting sources','finish',dict(finish_reason='budget-limit',**draft,artifact='artifact-1'),False,True)
arg('no old assessment wrapper','finish',dict(finish_reason='budget-limit',assessment=draft),False,False)
arg('no conclusion record','record_append',dict(kind='conclusion'),False)
arg('internal model operation hidden','model_generate',{},False)
arg('artifact chunk hidden','artifact_put_part',{},False)
arg('shell hidden','exec',dict(command='true'),False)
arg('non-object arguments','snapshot_request',[],False)
(ROOT/'fixtures/model-tools.json').write_text(json.dumps(cases,indent=2,ensure_ascii=False)+'\n')
print(len(cases),'fixed tool fixtures')
