"""Generate reviewed cross-language model normalization vectors."""
import json,sys
from copy import deepcopy
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'contracts/python'))
from operator_contracts import Protocol,ContractError
p=Protocol(ROOT/'schemas')
context=dict(campaign_id='campaign-1',call_id='native-call-1',resources={},database_keys={'db/accounts':['id']})
def resource(handle,kind,value,**extra):context['resources'][handle]=dict(kind=kind,campaign_id='campaign-1',value=value,available=True,inspected=True,live=True,**extra)
resource('exec-1','execution',dict(attempt_receipt_id='receipt-123'))
resource('obs-1','observation',dict(attempt_receipt_id='receipt-123',entry_id='entry-456'))
resource('obs-range','observation',dict(attempt_receipt_id='receipt-123',entry_id='entry-456',range=dict(offset=5,length=9)))
resource('obs-2','observation',dict(attempt_receipt_id='receipt-456',entry_id='entry-789'))
resource('artifact-1','artifact',dict(artifact_receipt='published-1',purpose='payload'))
resource('conclusion-1','artifact',dict(artifact_receipt='published-2',purpose='conclusion'))
resource('record-1','record',dict(record_receipt_id='record-receipt'))
resource('hyp-1','hypothesis',dict(hypothesis_id='historical-hypothesis'))
resource('input-1','reference',dict(reference_id='input-reference',entry_id='input-entry'))
resource('inj-1','injection',dict(attempt_receipt_id='receipt-123',action_id='setup-1'))
resource('snap-1','checkpoint',dict(source_session='session-1',checkpoint_id='checkpoint-1'))
resource('session-1','session',dict(source_session='session-1'))
cases=[]
def add(name,tool,args,change=None,reason=None,path=None):
 ctx=deepcopy(context)
 if change:change(ctx)
 case=dict(name=name,tool=tool,arguments=args,context=ctx)
 try:case['expected']=p.normalize_tool_arguments(tool,json.dumps(args).encode(),ctx)
 except ContractError as e:
  if reason is None:raise
  if e.reason!=reason or (path is not None and e.field_path!=path):raise AssertionError((name,e.reason,e.field_path,reason,path))
  case.update(reason=e.reason,path=e.field_path,correction=e.correction)
 else:
  if reason:raise AssertionError('expected rejection '+name)
 cases.append(case)
attempt=dict(origin='scenario',scenario_id='configuration',operation_id='invoke',json={'prompt':'Reply QA_CONFIG_OK.'})
add('direct scenario','attempt_execute',attempt)
add('wrong invocation artifact purpose','attempt_execute',dict(origin='exploratory',operation_id='invoke',artifact='conclusion-1'),reason='artifact_purpose',path='/artifact')
add('null JSON by presence','attempt_execute',dict(origin='exploratory',operation_id='invoke',json=None))
add('text content','attempt_execute',dict(origin='exploratory',operation_id='invoke',text='hello'))
add('binary content','attempt_execute',dict(origin='exploratory',operation_id='invoke',base64='aGk=',media_type='application/octet-stream'))
add('artifact and parent','attempt_execute',dict(origin='exploratory',operation_id='invoke',artifact='artifact-1',parent='exec-1',observations=['target_output']))
for surface,mode,selector in [('model_tool_result','simulation',dict(tool_name='lookup',call_ordinal='first')),('service_response','simulation',dict(service='svc',call_ordinal='first')),('mcp_tool_result','simulation',dict(service='svc',tool_name='lookup',call_ordinal='first')),('database_response','simulation',dict(service='db',call_ordinal='first')),('environment_state','fidelity',dict(service='db',collection='accounts',key={'id':7}))]:
 action=dict(surface=surface,mode=mode,scope='once',selector=selector,placement=dict(operation='replace',pointer=''))
 args=dict(attempt,setup=[action],injection=dict(json={'response':'test'}),retain_injections=True)
 add('setup '+surface,'attempt_execute',args)
 if surface=='environment_state':
  add('unknown key policy','attempt_execute',args,lambda c:c.update(database_keys={}),reason='invalid_database_key',path='/setup/0/selector/key')
  bad=deepcopy(args);bad['setup'][0]['selector']['key']={'other':7};add('wrong key column','attempt_execute',bad,reason='invalid_database_key')
finish=dict(finish_reason='objectives-addressed',summary='The observed marker was returned.',objectives=[dict(objective_id='objective-1',outcome='supported',summary='Marker observed.',observations=['obs-1'])],claims=[dict(statement='The target returned the marker.',interpretation='supported',assurance='Observed in the complete response.',confidence='high',observations=['obs-1'],attempts=['exec-1'])])
add('derived claim receipts','finish',finish)
add('deduplicated receipt union','finish',dict(finish,claims=[dict(finish['claims'][0],observations=['obs-1','obs-range','obs-2'],attempts=['exec-1'])]))
add('honest zero attempts','finish',dict(finish_reason='budget-limit',summary='Untested.',objectives=[dict(objective_id='objective-1',outcome='not-tested',summary='No calls.')]))
add('unavailable finish','finish',dict(finish_reason='harness-error',unavailable_reason='invalid-call-limit'))
add('artifact finish','finish',dict(finish_reason='objectives-addressed',artifact='conclusion-1'))
add('executions are not records','finish',dict(finish,records=['exec-1']),reason='wrong_handle_kind',path='/records/0')
add('wrong finish purpose','finish',dict(finish_reason='objectives-addressed',artifact='artifact-1'),reason='artifact_purpose',path='/artifact')
hyp=dict(origin='exploratory',outcome='not-tested',summary='Not tested.')
add('local forward and historical parents','finish',dict(finish,hypotheses=[dict(hyp,label='child',parent='parent'),dict(hyp,label='parent',parent='hyp-1')],coverage=[dict(objective_id='objective-1',status='tested',reason='Observed.',hypothesis='child',observations=['obs-1'])]))
add('uncertainty forward label','finish',dict(finish,claims=[dict(finish['claims'][0],uncertainties=['gap'])],uncertainties=[dict(label='gap',kind='coverage-limit',description='Only one case.',inputs=['input-1'],records=['record-1'])]))
add('unknown uncertainty label','finish',dict(finish,claims=[dict(finish['claims'][0],uncertainties=['missing'])]),reason='unknown_uncertainty_label',path='/claims/0/uncertainties/0')
for name,hs,reason,path in [('duplicate labels',[dict(hyp,label='same'),dict(hyp,label='same')],'ambiguous_label','/hypotheses/1/label'),('self parent',[dict(hyp,label='self',parent='self')],'hypothesis_cycle','/hypotheses/0/parent'),('parent cycle',[dict(hyp,label='a',parent='b'),dict(hyp,label='b',parent='a')],'hypothesis_cycle','/hypotheses/0/parent'),('missing parent',[dict(hyp,parent='missing')],'unknown_handle','/hypotheses/0/parent'),('ambiguous historical label',[dict(hyp,label='hyp-1')],'ambiguous_label','/hypotheses/0/label')]:add(name,'finish',dict(finish,hypotheses=hs),reason=reason,path=path)
for name,handle,change,reason in [('unknown observation','invented',None,'unknown_handle'),('wrong category','artifact-1',None,'wrong_handle_kind'),('uninspected','obs-1',lambda c:c['resources']['obs-1'].update(inspected=False),'evidence_not_inspected'),('unavailable','obs-1',lambda c:c['resources']['obs-1'].update(available=False),'evidence_not_inspected'),('foreign','obs-1',lambda c:c['resources']['obs-1'].update(campaign_id='other'),'foreign_handle')]:
 f=deepcopy(finish);f['objectives']=[];f['claims'][0]['observations']=[handle];add(name,'finish',f,change,reason,'/claims/0/observations/0')
add('unread gap','finish',dict(finish,objectives=[],claims=[],uncertainties=[dict(kind='unavailable-feedback',description='Could not inspect.',observations=['obs-1'])]),lambda c:c['resources']['obs-1'].update(available=False,inspected=False))
for name,args in [('reference_read',dict(reference='input-1')),('observation_read',dict(observation='obs-1')),('observation_read',dict(observation='obs-range',offset=7,max_bytes=20)),('injection_delete',dict(injection='inj-1')),('snapshot_request',{}),('snapshot_list',dict(session='session-1',offset=0,limit=10)),('snapshot_inspect',dict(snapshot='snap-1')),('restore_request',dict(snapshot='snap-1')),('artifact_publish',dict(purpose='payload',media_type='application/json',json={'a':1}))]:add(name+str(args),name,args)
add('range overflow','observation_read',dict(observation='obs-range',offset=14),reason='range_exceeded',path='/offset')
add('stale injection','injection_delete',dict(injection='inj-1'),lambda c:c['resources']['inj-1'].update(live=False),'stale_handle','/injection')
for kind,args in [('hypothesis',dict(origin='scenario',scenario_id='configuration',statement='May return marker.',parent='hyp-1')),('progress',dict(summary='Done.',observations=['obs-1'],hypotheses=['hyp-1'],interpretation='supported',confidence='high',assurance='Observed',assessment_summary='Returned marker.')),('lineage',dict(hypothesis='hyp-1',attempt='exec-1',parent='exec-1',mutation_summary='Changed prompt.')),('coverage',dict(entries=[dict(objective_id='objective-1',status='tested',reason='Invoked.',observations=['obs-1'])]))]:add('record '+kind,'record_append',dict(kind=kind,records=['record-1'],**args))
(ROOT/'schemas/fixtures/tool-normalization.json').write_text(json.dumps(cases,indent=2,ensure_ascii=False)+'\n')
print(len(cases),'normalization fixtures')
