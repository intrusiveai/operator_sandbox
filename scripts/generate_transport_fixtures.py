#!/usr/bin/env python3
"""Author shared transport bytes and state traces without importing validators."""
import base64
from copy import deepcopy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
FIX = ROOT / 'schemas/fixtures'
enc = lambda value: json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode()
b64 = lambda raw: base64.b64encode(raw).decode()
frame = lambda raw: len(raw).to_bytes(4, 'big') + raw
MAX = 2**53 - 1


def main():
    ordinary = json.loads((FIX / 'ordinary-protocol.json').read_text())
    request = next(c['request'] for c in ordinary if c.get('valid') and c.get('request', {}).get('operation') == 'engine.artifact_begin')
    response = next(c['response'] for c in ordinary if c.get('valid') and c.get('response', {}).get('operation') == 'engine.artifact_begin')
    startup = json.loads((FIX / 'startup-example.json').read_text())
    messages = {'ordinary-out': request, 'ordinary-in': response, 'control-in': startup[0], 'control-out': startup[1]}
    for message in messages.values(): message['seq'] = 0
    cases = []

    def frames(name, lane, chunks, expected=(), valid=True, eof=False):
        cases.append({'name': name, 'mode':'frames', 'lane':lane, 'chunks_base64':list(map(b64,chunks)),
                      'frames_base64':list(map(b64,expected)), 'valid':valid, 'eof':eof})

    for lane, message in messages.items():
        raw = enc(message);wire = frame(raw)
        frames(lane+' complete frame', lane, [wire], [raw])
        frames(lane+' fragmented header and body', lane, [wire[:1],wire[1:3],b'',wire[3:9],wire[9:]], [raw])
        frames(lane+' coalesced frames', lane, [wire+wire], [raw,raw])
        frames(lane+' every byte fragmented', lane, [bytes([b]) for b in wire], [raw])
    raw = enc(request);wire = frame(raw)
    frames('empty read is not EOF', 'ordinary-out', [b''], [])
    frames('zero length header', 'ordinary-out', [b'\0'*4], valid=False)
    frames('oversize ordinary header', 'ordinary-out', [(4194305).to_bytes(4,'big')], valid=False)
    frames('oversize control header', 'control-in', [(65537).to_bytes(4,'big')], valid=False)
    frames('maximum unsigned length', 'ordinary-out', [b'\xff'*4], valid=False)
    frames('little endian prefix rejected', 'ordinary-out', [len(raw).to_bytes(4,'little')+raw], valid=False)
    for name, bad in [('duplicate JSON fields',raw[:-1]+b',"seq":0}'),('JSON scalar',b'0'),('invalid UTF8',b'"\xff"'),('trailing JSON',raw+b'{}')]:
        frames(name,'ordinary-out',[frame(bad)],valid=False)
    frames('request in response lane','ordinary-in',[wire],valid=False)
    frames('host control in guest lane','control-out',[frame(enc(startup[0]))],valid=False)
    frames('EOF between frames','ordinary-out',[wire],[raw],valid=False,eof=True)
    frames('EOF mid header','ordinary-out',[wire[:2]],valid=False,eof=True)
    frames('EOF mid body','ordinary-out',[wire[:-1]],valid=False,eof=True)
    max_message=deepcopy(request);max_message['seq']=MAX
    frames('maximum stateless sequence','ordinary-out',[frame(enc(max_message))],[enc(max_message)])
    for seq in [0,1,12,MAX]:
        for temporary in [False,True]:
            name=f'.{seq:020d}.tmp' if temporary else f'{seq:020d}.json'
            cases.append({'name':'spool '+name,'mode':'name','filename':name,'sequence':seq,'temporary':temporary,'valid':True})
    for index, name in enumerate(['1.json','000000000000000000000.json','0000000000000000000a.json','00000000000000000001.JSON',
        '/00000000000000000001.json','../00000000000000000001.json','00000000000000000001.json\n',
        'consumed.json','.consumed.tmp',f'{MAX+1:020d}.json','99999999999999999999.json','.'+'0'*20+'.json']):
        cases.append({'name':f'invalid spool filename {index}','mode':'name','filename':name,'valid':False})
    for name,filename,valid in [('ready file',f'{0:020d}.json',True),('filename sequence mismatch',f'{1:020d}.json',False),('temporary file not dispatchable',f'.{0:020d}.tmp',False)]:
        cases.append({'name':name,'mode':'spool','lane':'ordinary-out','filename':filename,'raw_base64':b64(raw),'valid':valid})

    def event(action,lane=None,seq=0,revision=None,valid=True,**changes):
        msg=deepcopy(messages[lane]) if lane else {'api_version':'operator.dev/engine-spool-ack/v1alpha1','launch_id':'launch-1','ordinary_seq':None,'control_seq':None}
        if lane:
            msg['seq']=seq
            if revision is not None:msg['run_revision']=revision
        msg.update(changes)
        return {'action':action, 'lane':lane or '', 'raw_base64':b64(enc(msg)), 'valid':valid}
    def check(ordinary=None,control=None,action='acknowledged',valid=True):
        return {'action':action,'expected':{'ordinary_seq':ordinary,'control_seq':control},'valid':valid}
    def state(name,steps,role='host'):
        cases.append({'name':name,'mode':'state','role':role,'campaign_id':'campaign-1','launch_id':'launch-1','steps':steps})
    state('independent receive and send counters',[
        event('publish','control-in'),event('accept','control-out'),event('accept','ordinary-out'),event('publish','ordinary-in'),
        check(0,0,action='consumed'),event('ack',ordinary_seq=0,control_seq=0),check(0,0)])
    state('guest direction and cumulative ACK',[
        event('accept','control-in'),event('publish','control-out'),event('publish','ordinary-out'),event('accept','ordinary-in'),
        check(0,0,action='consumed'),event('ack',ordinary_seq=0,control_seq=0),check(0,0)],role='guest')
    state('revision change keeps ordinary sequence',[
        event('accept','ordinary-out'),event('accept','ordinary-out',seq=1,revision=4),check(1,action='consumed')])
    state('older revision control accepted by transport',[
        event('publish','ordinary-in',revision=4),event('publish','control-in',revision=3)])
    state('stale and null ACKs cannot rewind',[
        event('publish','ordinary-in'),event('publish','ordinary-in',seq=1),event('ack',ordinary_seq=1),
        event('ack',ordinary_seq=0),event('ack'),event('ack',ordinary_seq=1),check(1)])
    state('ACK before publication is future',[
        event('ack',ordinary_seq=0,valid=False),event('publish','ordinary-in',valid=False),check(action='consumed',valid=False)])
    state('future ACK updates are atomic',[
        event('publish','ordinary-in'),event('ack',ordinary_seq=0,control_seq=0,valid=False),check(),event('ack',valid=False)])
    state('wrong launch ACK closes transport',[event('ack',launch_id='other',valid=False),event('publish','ordinary-in',valid=False)])
    for name, step in [
        ('receive gap',event('accept','ordinary-out',seq=1,valid=False)),
        ('wrong campaign',event('accept','ordinary-out',campaign_id='other',valid=False)),
        ('wrong launch',event('accept','ordinary-out',launch_id='other',valid=False)),
        ('host cannot receive own lane',event('accept','ordinary-in',valid=False)),
        ('host cannot publish guest lane',event('publish','ordinary-out',valid=False)),
        ('publication gap',event('publish','ordinary-in',seq=1,valid=False)),
    ]:state(name,[step,event('accept','control-out',valid=False)])
    state('receive replay closes every lane',[event('accept','ordinary-out'),event('accept','ordinary-out',valid=False),event('publish','control-in',valid=False)])
    state('publication replay closes every lane',[event('publish','ordinary-in'),event('publish','ordinary-in',valid=False),event('accept','control-out',valid=False)])
    state('healthy restore cannot reset sequence',[event('accept','ordinary-out'),event('accept','ordinary-out',revision=4,valid=False)])
    state('explicit close is terminal',[{'action':'close','valid':True},event('accept','ordinary-out',valid=False),event('publish','control-in',valid=False),event('ack',valid=False),check(action='consumed',valid=False)])
    (FIX/'transport-codec.json').write_text(json.dumps(cases,indent=2)+'\n')
    print(f'{len(cases)} transport cases')


if __name__=='__main__':main()
