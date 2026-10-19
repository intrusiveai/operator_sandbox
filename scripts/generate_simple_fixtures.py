"""Reviewed simple workflow cases shared by Go and Python validators."""
from copy import deepcopy
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas'
cases=[]
def add(name,mode,value,valid=True,**extra):cases.append(dict(name=name,mode=mode,value=deepcopy(value),valid=valid,**extra))
add('direct JSON','input',dict(encoding='json',value=dict(z=1,a='x')),bytes_utf8='{"a":"x","z":1}',media_type='application/json',canonicalization='jcs-v1')
add('direct UTF8','input',dict(encoding='utf8',text='hello'),bytes_utf8='hello',media_type='text/plain',canonicalization='raw')
add('direct binary','input',dict(encoding='base64',data='aGk=',media_type='application/octet-stream'),bytes_utf8='hi',media_type='application/octet-stream',canonicalization='raw')
add('artifact reference','input',dict(artifact_receipt='artifact-returned'),artifact_receipt='artifact-returned')
add('input alternatives exclusive','input',dict(encoding='json',value={},artifact_receipt='artifact-returned'),False)
add('empty input rejected','input',{},False)
add('noncanonical base64','input',dict(encoding='base64',data='aGm=',media_type='application/octet-stream'),False)
add('UTF8 decoded byte bound','input',dict(encoding='utf8',text='é'*16385),False)
nested=0
for i in range(16):nested=[nested]
add('depth sixteen','input',dict(encoding='json',value=nested),bytes_utf8='['*16+'0'+']'*16,media_type='application/json',canonicalization='jcs-v1')
add('depth seventeen','input',dict(encoding='json',value=[nested]),False)
base=dict(summary='No target attempt ran.',objectives=[dict(objective_id='objective-1',outcome='not-tested',summary='Not tested.')])
add('minimal zero attempt assessment','assessment',base,normalized=dict(status='partial',hypotheses=[],claims=[],coverage=[],uncertainties=[],record_refs=[]))
for key,val in [('status','invalid'),('binding',{}),('objectives',None)]:add('invalid '+key,'assessment',dict(base,**{key:val}),False)
success=deepcopy(base);success['objectives'][0]['outcome']='supported';add('success cannot have empty evidence','assessment',success,False)
success['objectives'][0]['attempt_receipt_refs']=['receipt-1'];add('supported with execution reference','assessment',success)
hyp=dict(hypothesis_id='hyp-1',provenance=dict(origin='exploratory'),outcome='not-tested',summary='Not tested.')
a=deepcopy(base);a['hypotheses']=[dict(hyp,provenance=dict(origin='exploratory',parent_hypothesis_id='hyp-2')),dict(hyp,hypothesis_id='hyp-2')];add('forward local parent','assessment',a)
a=deepcopy(base);a['hypotheses']=[dict(hyp,provenance=dict(origin='exploratory',parent_hypothesis_id='historical-parent'))];add('historical parent requires host lookup','assessment',a)
for name,hs in [('duplicate',[hyp,hyp]),('self-parent',[dict(hyp,provenance=dict(origin='exploratory',parent_hypothesis_id='hyp-1'))]),('cycle',[dict(hyp,provenance=dict(origin='exploratory',parent_hypothesis_id='hyp-2')),dict(hyp,hypothesis_id='hyp-2',provenance=dict(origin='exploratory',parent_hypothesis_id='hyp-1'))])]:add(name,'assessment',dict(base,hypotheses=hs),False)
(ROOT/'fixtures/simple-workflow.json').write_text(json.dumps(dict(binding=json.loads((ROOT/'fixtures/conclusion-example.json').read_text())['binding'],finish_reason='budget-limit',cases=cases),indent=2,ensure_ascii=False)+'\n')
