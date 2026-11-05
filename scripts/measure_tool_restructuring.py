"""Deterministic catalog-only comparison against the locked 0.5.0 source.
Whole Harness prompt/history/compaction measurements belong to its integration.
"""
import json,hashlib,subprocess,sys,math
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'contracts/python'))
from operator_contracts import Protocol
from operator_contracts.canonical import _canonical_value
from copy import deepcopy
p=Protocol(ROOT/'schemas');ops=sorted(o['name'] for o in p.operations())
baseline='485042787cc75e1776acaac11023ba9d210cc1f3'
old=json.loads(subprocess.check_output(['git','show',baseline+':schemas/model-tool-arguments.schema.json'],cwd=ROOT))
codecs=['openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1','bedrock-converse-text-tools-v1','gemini-text-tools-v1']
profiles=[[],['mcp_tool_result'],['database_response','environment_state'],['database_response','environment_state','mcp_tool_result','model_tool_result','service_response']]
def declarations(doc,codec,enabled,surfaces):
 tools=[];decs=[]
 for b in doc['oneOf']:
  if not set(b['x-operator-required-operations'])<=set(enabled):continue
  name=b['properties']['name']['const'];v=deepcopy(b.get('x-operator-provider-parameters',b['properties']['arguments']))
  if codec in codecs[:2]:v=deepcopy(b.get('x-operator-openai-parameters',v))
  if name=='attempt_execute':
   if not surfaces or 'engine.injection_delete' not in enabled:
    for k in ('pre_actions','injection_input','retain_injections'):v['properties'].pop(k,None)
   else:
    setup=v['properties']['pre_actions']['items']['properties']['parameters'];setup['properties']['surface']={'enum':surfaces};setup['oneOf']=[x for x in setup['oneOf'] if x['properties']['surface']['const'] in surfaces]
  d=b['description']
  if codec==codecs[0]:tools.append(dict(type='function',function=dict(name=name,description=d,parameters=v,strict=False)))
  elif codec==codecs[1]:tools.append(dict(type='function',name=name,description=d,parameters=v,strict=False))
  elif codec==codecs[2]:tools.append(dict(name=name,description=d,input_schema=v))
  elif codec==codecs[3]:tools.append(dict(toolSpec=dict(name=name,description=d,inputSchema=dict(json=v))))
  else:decs.append(dict(name=name,description=d,parametersJsonSchema=v))
 return [dict(functionDeclarations=decs)] if decs else tools

def stats(tools,codec):
 if codec==codecs[-1]:tools=tools[0]['functionDeclarations']
 out={}
 for t in tools:
  t=t.get('function',t.get('toolSpec',t));schema=t.get('parameters',t.get('parametersJsonSchema',t.get('input_schema',t.get('inputSchema',{}).get('json',{}))))
  raw=_canonical_value(t,4<<20)
  out[t['name']]=dict(declaration_bytes=len(raw),description_bytes=len(t['description'].encode()),required_fields=schema.get('required',[]))
 return out
cases=[]
for codec in codecs:
 for mode,enabled,selected in [('ordinary', ['engine.attempt_execute','engine.finish','engine.observation_read'],[])]+[('full',ops,s) for s in profiles]:
  before=declarations(old,codec,enabled,selected);after=json.loads(p.model_tools(codec,enabled,selected))
  b=len(_canonical_value(before,4<<20));a=len(_canonical_value(after,4<<20))
  if a>b:raise SystemExit('catalog grew '+codec+str(selected))
  cases.append(dict(codec=codec,mode=mode,operations=enabled,surfaces=selected,baseline_bytes=b,revised_bytes=a,baseline_tokens_estimated=math.ceil(b/4),revised_tokens_estimated=math.ceil(a/4),baseline_tools=stats(before,codec),revised_tools=stats(after,codec)))
report=dict(baseline_source=baseline,estimate_method='ceil(canonical UTF-8 bytes / 4), v1; estimate, not a model tokenizer',scope='Catalog only; matching Harness session measures trusted prompt, workload history, retries and compaction.',cases=cases)
(ROOT/'schemas/fixtures/tool-context-cost.json').write_text(json.dumps(report,indent=2)+'\n')
for c in cases:
 if c['codec']==codecs[0]:print(c['mode'],c['surfaces'],c['baseline_bytes'],c['revised_bytes'],round(100*(1-c['revised_bytes']/c['baseline_bytes']),1),'percent reduction')
