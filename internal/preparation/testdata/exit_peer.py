"""Controlled Docker/spool finish peer. No model, network, or target calls."""
import json, os, sys, time
from pathlib import Path
spool=Path('/spool'); mode=sys.argv[1]
def put(lane,n,value):
    tmp=spool/lane/f'.{n:020d}.tmp'; tmp.write_text(json.dumps(value,separators=(',',':')))
    os.replace(tmp,spool/lane/f'{n:020d}.json')
def read(lane,n):
    p=spool/lane/f'{n:020d}.json'
    until=time.monotonic()+20
    while not p.exists():
        if time.monotonic()>until: raise RuntimeError('controlled peer response deadline')
        time.sleep(.01)
    return json.loads(p.read_text())
def ack(control,ordinary=None):
    v=dict(api_version='operator.dev/engine-spool-ack/v1alpha1',launch_id='launch-1',ordinary_seq=ordinary,control_seq=control)
    tmp=spool/'control-out'/'.consumed.tmp'; tmp.write_text(json.dumps(v));os.replace(tmp,spool/'control-out'/'consumed.json')
boot=read('control-in',0); ack(0); boot['kind']='confinement_ready';boot['body'].pop('timeout_ms'); contract=boot['body']['contract'];put('control-out',0,boot)
init=read('control-in',1);ack(1);init['kind']='initialized';init['body'].pop('timeout_ms');init['body']['contract']=contract;put('control-out',1,init)
read('control-in',2);ack(2)
if mode=='no-finish': sys.exit(0)
seq=0
def finish(body):
    global seq
    q=dict(api_version='operator.dev/engine-pipe/v1alpha1',kind='request',seq=seq,campaign_id='campaign-1',launch_id='launch-1',run_revision=1,call_id=f'call-{seq}',operation_id=f'finish-{seq}',operation='engine.finish',timeout_ms=30000,body=body)
    put('ordinary-out',seq,q);r=read('ordinary-in',seq);ack(2,seq);(spool/'ordinary-out'/f'{seq:020d}.json').unlink();seq+=1;return r
assessment=dict(summary='No target invocation was required by this lifecycle test.',objectives=[])
if mode=='corrected':
    bad=dict(summary='Rejected reference',objectives=[dict(objective_id='objective-marker',outcome='inconclusive',summary='No evidence',attempt_receipt_refs=['missing'])])
    assert 'error' in finish(dict(finish_reason='objectives-addressed',assessment=bad))
r=finish(dict(finish_reason='objectives-addressed',assessment=assessment)); assert r['result']['status']=='accepted',r
if mode=='peer-first':
    (spool/'peer-closed').write_text('closed');time.sleep(.8)
if mode=='timeout': time.sleep(20)
if mode=='nonzero':
    print('RuntimeError: controlled post-finish failure; api_key=synthetic-secret-never-retain',file=sys.stderr,flush=True);sys.exit(1)
if mode=='silent-nonzero': sys.exit(2)
time.sleep(.15)
