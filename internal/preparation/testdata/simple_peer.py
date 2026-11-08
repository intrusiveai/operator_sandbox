"""Controlled Python protocol peer: local targets only, no model/provider calls.

This exercises the ordinary process/spool boundary independently of the separate
Attack Harness implementation. It does not claim production Harness qualification.
"""
import json
import base64
import hashlib
import os
from pathlib import Path
import sys
import time
from copy import deepcopy

root, spool, mode = map(str, sys.argv[1:])
sys.path.insert(0, str(Path(root)/'contracts/python'))
from operator_contracts import Protocol, ContractError
contract_bytes=(Path(root)/'schemas/asyncapi.json').read_bytes()
contract_pin=json.loads((Path(root)/'release/contract-lock.json').read_bytes())['package']
p=Protocol.from_interface(contract_bytes,contract_pin)
spool=Path(spool)
seq=0
resolution=dict(campaign_id='campaign-1',call_id='native-call-1',resources={})
def normalized(tool,args,call_id):
    resolution['call_id']=call_id
    return p.normalize_tool_arguments(tool,json.dumps(args).encode(),resolution)

def atomic(path,value):
    tmp=path.with_name('.consumed.tmp')
    tmp.write_bytes(json.dumps(value,separators=(',',':')).encode())
    os.replace(tmp,path)

def call(operation,body,identity=None,invalid=False):
    global seq
    request=dict(api_version='operator.dev/engine-pipe/v1alpha1',kind='request',seq=seq,
                 campaign_id='campaign-1',launch_id='launch-1',run_revision=1,
                 call_id=f'call-{seq}',operation_id=identity or f'operation-{seq}',
                 operation=operation,timeout_ms=30000,body=body)
    raw=json.dumps(request,separators=(',',':')).encode()
    if invalid:p.validate_request_envelope(raw)
    else:p.validate_request(raw)
    name=f'{seq:020d}.json'
    # Transport publication uses its exact tmp/ready naming convention.
    temporary=spool/'ordinary-out'/f'.{seq:020d}.tmp'
    temporary.write_bytes(raw);os.replace(temporary,spool/'ordinary-out'/name)
    response=spool/'ordinary-in'/name
    deadline=time.monotonic()+15
    while not response.exists():
        if time.monotonic()>deadline:raise RuntimeError('fixture response timeout')
        time.sleep(.01)
    value=p.validate_response(raw,response.read_bytes())
    atomic(spool/'control-out'/'consumed.json',dict(api_version='operator.dev/engine-spool-ack/v1alpha1',launch_id='launch-1',ordinary_seq=seq,control_seq=2))
    (spool/'ordinary-out'/name).unlink()
    seq+=1
    return value

if mode=='invalid-limit':
    for i in range(5):
        try:p.validate_tool_arguments('attempt_execute',b'{"origin":"exploratory","operation_id":"invoke"}')
        except ContractError as e:
            diagnostic=dict(source='harness-local',stage='arguments',reason=e.reason,
                            field_path=e.field_path,message='Required input is absent.',
                            execution_occurred='no',resubmission_safe=True)
            call('engine.diagnostic_report',dict(tool_name='attempt_execute',tool_call_id=f'bad-{i+1}',diagnostic=diagnostic))
        else:raise AssertionError('invalid arguments accepted')
    diagnostic=dict(source='harness-local',stage='termination',reason='invalid_call_limit',
                    message='Invalid tool calls reached the configured limit.',
                    execution_occurred='no',resubmission_safe=False)
    call('engine.diagnostic_report',dict(diagnostic=diagnostic,
                                        stopping=dict(condition='invalid-call-limit',observed_count=5,configured_limit=5)))
    finish=normalized('finish',dict(finish_reason='harness-error',unavailable_reason='invalid-call-limit'),'finish')['arguments']
    attempts=0
else:
    if mode=='corrected':
        rejected=call('engine.attempt_execute',dict(origin='exploratory',operation_id='invoke'),invalid=True)
        assert rejected['error']['code']=='INVALID_ARGUMENTS'
        assert rejected['error']['instance_path']=='/input'
        assert 'receipt_id' not in rejected
    attempts=4 if mode=='qa-corrected' else 1
    selected=[]
    for i in range(attempts):
        args=dict(origin='scenario',operation_id='invoke',scenario_id='scenario-marker',json=dict(query='direct input'))
        execution=call('engine.attempt_execute',normalized('attempt_execute',args,f'invoke-{i}')['arguments'])['result']
        assert execution['status']=='completed'
        entry=execution['feedback']['entries'][0];handle=f'obs-{i}'
        resolution['resources'][handle]=dict(kind='observation',campaign_id='campaign-1',available=True,inspected=False,value=dict(attempt_receipt_id=execution['receipt_id'],entry_id=entry['entry_id']))
        if mode == 'pagination-corrected':
            next_request = dict(observation=handle,offset=0,max_bytes=4)
            chunks=[]
            while next_request:
                read=call('engine.observation_read',normalized('observation_read',next_request,f'read-{i}-{next_request["offset"]}')['arguments'])['result']
                chunk=base64.b64decode(read['content'],validate=True);chunks.append(chunk)
                citation=f'{handle}-range-{read["offset"]}'
                resource=deepcopy(resolution['resources'][handle]);resource['inspected']=True
                resource['value']['range']=dict(offset=read['offset'],length=read['raw_length'])
                resolution['resources'][citation]=resource
                selected.append(citation)
                # Controlled peer implements the documented presentation. Production
                # Harness must adopt the same fields in its own _present method.
                presented=dict(readable_observation=handle,citation=citation)
                if not read['eof']:
                    presented['continuation']=dict(tool='observation_read',arguments=dict(observation=handle,offset=read['offset']+read['raw_length'],max_bytes=4))
                    bad=dict(finish_reason='objectives-addressed',summary='Partial read.',objectives=[dict(objective_id='objective-marker',outcome='supported',summary='Observed.',observations=[handle])])
                    try:normalized('finish',bad,'unread-citation')
                    except ContractError as error:
                        assert error.reason=='evidence_not_inspected' and citation in error.correction,error
                    else:raise AssertionError('unread whole observation accepted')
                next_request=presented.get('continuation',{}).get('arguments')
            content=b''.join(chunks)
            assert len(content)==read['artifact']['size_bytes']
            assert 'sha256:'+hashlib.sha256(content).hexdigest()==read['artifact']['digest']
        else:
            read=call('engine.observation_read',normalized('observation_read',dict(observation=handle,max_bytes=256),f'read-{i}')['arguments'])
            assert 'result' in read,read
            observed=read['result'];content=base64.b64decode(observed['content'],validate=True)
            assert observed['availability']=='available' and observed['eof'] and not observed['truncated']
            assert len(content)==observed['raw_length']==observed['artifact']['size_bytes']
            assert 'sha256:'+hashlib.sha256(content).hexdigest()==observed['artifact']['digest']
            resolution['resources'][handle]['inspected']=True;selected.append(handle)
    if mode=='qa-corrected':
        try:p.validate_tool_arguments('finish',b'{"finish_reason":"objectives-addressed","summary":"one","summary":"two","objectives":[]}')
        except ContractError as error:assert error.reason=='duplicate_json_key' and error.field_path=='/summary'
        else:raise AssertionError('duplicate summary accepted')
        try:p.validate_tool_arguments('finish',b'{"finish_reason":"objectives-addressed","assessment":{}}')
        except ContractError as error:assert error.reason=='obsolete_wrapper' and error.field_path=='/assessment'
        else:raise AssertionError('obsolete wrapper accepted')
    if mode.startswith('budget-') or mode == 'pagination-corrected':
        malformed=dict(finish_reason='objectives-addressed',summary='Finished target.',objectives=[],uncertainties=['A limitation'])
        try: normalized('finish',malformed,'bad-finish')
        except ContractError as error:
            assert error.field_path=='/uncertainties/0' and 'kind and description' in error.correction
            report=dict(source='harness-local',stage='arguments',reason=error.reason,field_path=error.field_path,message='Malformed uncertainty.',execution_occurred='no',resubmission_safe=True)
            call('engine.diagnostic_report',dict(tool_name='finish',tool_call_id='bad-finish',diagnostic=report))
        else: raise AssertionError('malformed finish accepted')
    if mode.startswith('budget-'):
        consumed=493460 if mode=='budget-001' else 502198
        report=dict(source='harness-local',stage='termination',reason='model_reservation_unavailable',message='Cannot fund next request.',execution_occurred='no',resubmission_safe=False)
        budget=dict(kind='model-tokens',total_allowance=524288,consumed=consumed,remaining=524288-consumed,next_request_reservation=36864)
        call('engine.diagnostic_report',dict(diagnostic=report,stopping=dict(condition='budget-limit',budget=budget)))
        finish=normalized('finish',dict(finish_reason='budget-limit',unavailable_reason='budget-limit'),'finish')['arguments']
    else:
        args=dict(finish_reason='objectives-addressed',summary='Observed the local target response.',objectives=[dict(objective_id='objective-marker',outcome='supported',summary='A target response was observed.',observations=selected)],claims=[dict(statement='The response was inspected.',interpretation='supported',assurance='Controlled local response.',confidence='high',observations=selected)])
        expansion=normalized('finish',args,'finish')
        diagnostic=dict(source='harness-local',stage='references',reason='observation_selection',message='Selected observation evidence.',execution_occurred='no',resubmission_safe=True)
        call('engine.diagnostic_report',dict(tool_name='finish',tool_call_id='finish',diagnostic=diagnostic,observation_selections=expansion['observation_selections']))
        finish=expansion['arguments']
accepted=call('engine.finish',finish,identity='finish-once')['result']
assert accepted['finalization_status']=='pending' and accepted['execution_admission']=='closed'
duplicate=call('engine.finish',finish,identity='finish-once')['result']
assert duplicate==accepted
print(json.dumps(dict(mode=mode,dispatched=attempts,finish=accepted,ordinary_calls=seq)))
