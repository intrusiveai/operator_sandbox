"""Author shared compact-context and selected-tool golden vectors."""
import json,sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'contracts/python'))
from operator_contracts import Protocol
from operator_contracts.canonical import canonical_digest,raw_digest
from operator_contracts.target_context import compact_target_context
p=Protocol(ROOT/'schemas');cases=[]
for name in ('capability-chain','postgresql-capability-chain'):
    raw=(ROOT/'schemas/fixtures'/name/'public-capabilities.json').read_bytes()
    view,files=compact_target_context(p,raw)
    cases.append(dict(name=name,target=json.loads(raw),view=json.loads(view),references={key:json.loads(value) for key,value in files.items()}))
# A many-column target makes deferred table details materially useful.
from copy import deepcopy
large=deepcopy(cases[-1]['target'])
large['capabilities']['services'][0]['database_tables'][0]['fields']=['column_'+str(i) for i in range(64)]
view,files=compact_target_context(p,json.dumps(large).encode())
cases.append(dict(name='wide-table-context',target=large,view=json.loads(view),references={key:json.loads(value) for key,value in files.items()}))
(ROOT/'schemas/fixtures/compact-target-context.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2)+'\n')
ops=sorted(op['name'] for op in p.operations());cases=[]
for codec in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1','anthropic-messages-text-tools-v1','bedrock-converse-text-tools-v1','gemini-text-tools-v1'):
    for surfaces in ([],['mcp_tool_result'],['database_response','environment_state'],['database_response','environment_state','mcp_tool_result','model_tool_result','service_response']):
        raw=p.model_tools(codec,ops,surfaces)
        cases.append(dict(codec=codec,operations=ops,surfaces=surfaces,digest=raw_digest(raw),size_bytes=len(raw)))
(ROOT/'schemas/fixtures/selected-model-tools.json').write_text(json.dumps(cases,indent=2)+'\n')
