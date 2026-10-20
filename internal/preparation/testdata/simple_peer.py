"""Controlled Python protocol peer: local targets only, no model/provider calls.

This exercises the ordinary process/spool boundary independently of the separate
Attack Harness implementation. It does not claim production Harness qualification.
"""
import json
import os
from pathlib import Path
import sys
import time

root, spool, mode = map(str, sys.argv[1:])
sys.path.insert(0, str(Path(root)/'contracts/python'))
from operator_contracts import Protocol, ContractError
p=Protocol(Path(root)/'schemas')
spool=Path(spool)
seq=0

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
    errors=[]
    for i in range(5):
        try:p.validate_tool_arguments('attempt_execute',b'{"origin":"exploratory","operation_id":"invoke"}')
        except ContractError as e:errors.append(e)
        else:raise AssertionError('invalid arguments accepted')
    error=errors[-1]
    diagnostic=dict(source='harness-local',stage='arguments',reason=error.reason,
                    field_path=error.field_path,message='Required input is absent.',
                    execution_occurred='no',resubmission_safe=True)
    call('engine.diagnostic_report',dict(tool_name='attempt_execute',tool_call_id='last-invalid',diagnostic=diagnostic,
                                        stopping=dict(condition='invalid-call-limit',observed_count=5,configured_limit=5)))
    finish=dict(finish_reason='harness-error',unavailable=dict(reason='invalid-call-limit'))
    attempts=0
else:
    if mode=='corrected':
        rejected=call('engine.attempt_execute',dict(origin='exploratory',operation_id='invoke'),invalid=True)
        assert rejected['error']['code']=='INVALID_ARGUMENTS'
        assert rejected['error']['instance_path']=='/input'
        assert 'receipt_id' not in rejected
    execution=call('engine.attempt_execute',dict(origin='scenario',operation_id='invoke',scenario_id='scenario-marker',
                    input=dict(encoding='json',value=dict(query='direct input'))))['result']
    assert execution['status']=='completed'
    entry=execution['feedback']['entries'][0]
    read=call('engine.observation_read',dict(receipt_id=execution['receipt_id'],entry_id=entry['entry_id'],offset=0,max_bytes=256))
    assert 'result' in read, read
    observed=read['result']
    assert observed['availability']=='available' and observed['raw_length']>0
    finish=dict(finish_reason='objectives-addressed',assessment=dict(summary='Observed the local target response.',objectives=[dict(objective_id='objective-marker',outcome='supported',summary='A target response was observed.',attempt_receipt_refs=[execution['receipt_id']])]))
    attempts=1
accepted=call('engine.finish',finish,identity='finish-once')['result']
assert accepted['finalization_status']=='pending' and accepted['execution_admission']=='closed'
duplicate=call('engine.finish',finish,identity='finish-once')['result']
assert duplicate==accepted
print(json.dumps(dict(mode=mode,dispatched=attempts,finish=accepted,ordinary_calls=seq)))
