"""Build the fixed local model-tool schema from Operator-owned wire schemas."""
import argparse
from copy import deepcopy
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas'
ID='urn:operator:schema:model-tool-arguments:v1alpha1'


def object_schema(properties,required=None):
    return dict(type='object',additionalProperties=False,properties=properties,required=list(properties) if required is None else required)


def build():
    catalog=json.loads((ROOT/'catalog.json').read_text())
    documents={uri:json.loads((ROOT/file).read_text()) for uri,file in catalog.items() if uri!=ID}
    def inline(value,base,trail=()):
        if isinstance(value,list):return [inline(v,base,trail) for v in value]
        if not isinstance(value,dict):return value
        if '$ref' in value:
            uri,_,fragment=value['$ref'].partition('#');uri=uri or base
            key=(uri,fragment)
            if key in trail or uri not in documents:raise ValueError('unresolvable/cyclic tool schema')
            target=documents[uri]
            if fragment:
                if not fragment.startswith('/'):raise ValueError('unsupported schema anchor')
                for token in fragment[1:].split('/'):target=target[token.replace('~1','/').replace('~0','~')]
            target=inline(target,uri,trail+(key,))
            siblings=inline({k:v for k,v in value.items() if k!='$ref'},base,trail)
            return dict(allOf=[target,siblings]) if siblings else target
        return {k:inline(v,base,trail) for k,v in value.items() if k not in ('$schema','$id','$defs','$comment','title')}
    def load(stem,version='v1alpha1'):
        uri='urn:operator:schema:'+stem+':'+version
        return inline(documents[uri],uri)
    wire=documents['urn:operator:schema:wire-common:v1alpha1']['$defs']
    identifier=inline(wire['id'],'urn:operator:schema:wire-common:v1alpha1')
    artifact=load('engine-artifact-begin-request')
    media=artifact['properties']['artifact']['properties']['media_type']
    attempt=deepcopy(documents['urn:operator:schema:engine-attempt-request:v1alpha2'])
    reserved=['api_version','kind','generator','request_id','attempt_id','attempt_index']
    for name in reserved:attempt['properties'].pop(name);attempt['required'].remove(name)
    attempt=inline(attempt,'urn:operator:schema:engine-attempt-request:v1alpha2')
    conclusion=load('engine-conclusion','v1alpha2')
    for name in ('api_version','kind','binding'):conclusion['properties'].pop(name);conclusion['required'].remove(name)
    records=load('engine-record-append-request')
    records['oneOf']=[b for b in records['oneOf'] if b['properties']['record_kind']['const']!='conclusion']
    records['type']='object'
    content={'oneOf':[
        object_schema(dict(encoding={'const':'utf8'},text=dict(type='string',maxLength=1048576))),
        object_schema(dict(encoding={'const':'base64'},data=dict(type='string',maxLength=1398104))),
        object_schema(dict(encoding={'const':'json'},value={}))]}
    reference=object_schema(dict(reference_id=identifier,offset=dict(type='integer',minimum=0,maximum=2**53-1),max_bytes=dict(type='integer',minimum=1,maximum=262144)))
    publish=object_schema(dict(purpose={'enum':['payload','carrier','supporting-data']},media_type=media,content=content))
    upload=['engine.artifact_begin','engine.artifact_put_part','engine.artifact_commit']
    entries=[
      ('reference_read','Read a bounded range from a verified reference handle in the campaign index. Never accepts a filesystem path.',[],reference),
      ('artifact_publish','Publish supplied data and return a committed artifact receipt. JSON encoding uses canonical JSON; strings remain JSON strings.',upload,publish),
      ('attempt_execute','Execute one explicit experiment with committed artifacts. The harness assigns request/attempt IDs, submission index and release identity.',['engine.attempt_execute'],attempt),
      ('injection_delete','Delete a retained injection using its known attempt receipt and action ID.',['engine.injection_delete'],load('engine-injection-delete-request')),
      ('observation_read','Read permitted feedback bytes using the host attempt receipt and observation entry ID.',['engine.observation_read'],load('engine-observation-read-request')),
      ('record_append','Append a typed hypothesis, progress, lineage or coverage assertion. These records do not establish observed effects.',['engine.record_append'],records),
      ('request_stop','Conclude the campaign with structured claims, coverage, uncertainty and a finish reason. The harness supplies verified bindings and performs finalization.',upload+['engine.record_append','engine.request_stop'],conclusion),
      ('restore_request','Restore a known checkpoint while retaining this conversation. Success skips later calls from the current model response.',['engine.restore_request'],load('engine-restore-request')),
      ('snapshot_request','Create a checkpoint with optional label and description; the host supplies campaign metadata.',['engine.snapshot_request'],load('engine-snapshot-request')),
      ('snapshot_list','List checkpoints available to this campaign, with bounded pagination.',['engine.snapshot_list'],load('engine-snapshot-list-request')),
      ('snapshot_inspect','Inspect a checkpoint and its metadata by source-session/checkpoint identity.',['engine.snapshot_inspect'],load('engine-snapshot-inspect-request')),
    ]
    branches=[]
    for name,description,operations,arguments in sorted(entries):
        branch=object_schema(dict(name={'const':name},arguments=arguments))
        branch['description']=description;branch['x-operator-required-operations']=sorted(operations);branches.append(branch)
    return {'$schema':'https://json-schema.org/draft/2020-12/schema','$id':ID,
        '$comment':'Generated by scripts/generate_tool_catalog.py; see MODEL_TOOL_CATALOG.md.','oneOf':branches}


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--check',action='store_true');args=parser.parse_args()
    document=build();rendered=json.dumps(document,indent=2)+'\n';name='model-tool-arguments.schema.json';path=ROOT/name
    catalog=json.loads((ROOT/'catalog.json').read_text())
    if args.check:
        if not path.exists() or path.read_text()!=rendered or catalog.get(ID)!=name:raise SystemExit('Stale model tool schema')
    else:
        path.write_text(rendered);catalog[ID]=name;(ROOT/'catalog.json').write_text(json.dumps(dict(sorted(catalog.items())),indent=2)+'\n')
    print('Fixed model tool schema:',len(rendered.encode()),'bytes')


if __name__=='__main__':main()
