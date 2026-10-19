"""Build the fixed local model-tool schema from Operator-owned wire schemas."""
import argparse
from copy import deepcopy
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas'
ID='urn:operator:schema:model-tool-arguments:v1alpha1'


def object_schema(properties,required=None):
    return dict(type='object',additionalProperties=False,properties=properties,required=list(properties) if required is None else required)


def openai_parameters(name, arguments):
    """Provider declaration only; arguments remains the authoritative validator.

    Non-strict OpenAI tools still forbid root combinators. Preserve field schemas
    here and enforce cross-field rules with validate_tool_arguments before dispatch.
    Only these reviewed projections may differ from the validation schema.
    """
    if name in ('attempt_execute', 'finish'):
        projected = deepcopy(arguments)
        projected.pop('allOf', None)
        projected.pop('oneOf', None)
        projected['description'] = (
            'Runtime validates cross-field rules. Attempt setup requires injection_input; '
            'injection_input and retain_injections require nonempty pre_actions. '
            'Finish requires exactly one of assessment, artifact_receipt or unavailable. '
            'Direct inputs are limited to 32768 decoded bytes and JSON depth 16.')
        return projected
    if name == 'record_append':
        variants = arguments['oneOf']
        kinds = [v['properties']['record_kind']['const'] for v in variants]
        records = []
        for kind, variant in zip(kinds, variants):
            record = deepcopy(variant['properties']['record'])
            record['description'] = 'Record body for record_kind '+kind+'.'
            records.append(record)
        projected = object_schema(dict(record_kind=dict(type='string',enum=kinds),record=dict(anyOf=records)))
        projected['description'] = (
            'record_kind selects the matching record body: hypothesis, progress, lineage or coverage. '
            'Runtime rejects a body for a different kind and validates every nested constraint. '
            'Conclusion records are not accepted by this tool.')
        return projected
    return None


def build():
    catalog=json.loads((ROOT/'catalog.json').read_text())
    documents={uri:json.loads((ROOT/file).read_text()) for uri,file in catalog.items() if uri!=ID}
    def inline(value,base,trail=(),compact=False):
        if isinstance(value,list):return [inline(v,base,trail,compact) for v in value]
        if not isinstance(value,dict):return value
        if '$ref' in value:
            uri,_,fragment=value['$ref'].partition('#');uri=uri or base
            if compact and fragment in ('/$defs/json0','/$defs/json1','/$defs/json2','/$defs/json3'):
                return {'description':'Bounded JSON value. Runtime validates the selector-specific nesting, property, item and string limits; invalid values are rejected.'}
            key=(uri,fragment)
            if key in trail or uri not in documents:raise ValueError('unresolvable/cyclic tool schema')
            target=documents[uri]
            if fragment:
                if not fragment.startswith('/'):raise ValueError('unsupported schema anchor')
                for token in fragment[1:].split('/'):target=target[token.replace('~1','/').replace('~0','~')]
            target=inline(target,uri,trail+(key,),compact)
            siblings=inline({k:v for k,v in value.items() if k!='$ref'},base,trail,compact)
            return dict(allOf=[target,siblings]) if siblings else target
        return {k:inline(v,base,trail,compact) for k,v in value.items() if k not in ('$schema','$id','$defs','$comment','title')}
    def load(stem,version='v1alpha1'):
        uri='urn:operator:schema:'+stem+':'+version
        return inline(documents[uri],uri)
    wire=documents['urn:operator:schema:wire-common:v1alpha1']['$defs']
    identifier=inline(wire['id'],'urn:operator:schema:wire-common:v1alpha1')
    artifact=load('engine-artifact-begin-request')
    media=artifact['properties']['artifact']['properties']['media_type']
    attempt=load('engine-attempt-request','v1alpha3')
    finish=load('engine-finish-request')
    records=load('engine-record-append-request')
    records['oneOf']=[b for b in records['oneOf'] if b['properties']['record_kind']['const']!='conclusion']
    records['type']='object'
    content={'oneOf':[
        object_schema(dict(encoding={'const':'utf8'},text=dict(type='string',maxLength=1048576))),
        object_schema(dict(encoding={'const':'base64'},data=dict(type='string',maxLength=1398104))),
        object_schema(dict(encoding={'const':'json'},value={}))]}
    reference=object_schema(dict(reference_id=identifier,offset=dict(type='integer',minimum=0,maximum=2**53-1),max_bytes=dict(type='integer',minimum=1,maximum=262144)))
    publish=object_schema(dict(purpose={'enum':['payload','carrier','supporting-data','conclusion']},media_type=media,content=content))
    upload=['engine.artifact_begin','engine.artifact_put_part','engine.artifact_commit']
    entries=[
      ('reference_read','Read a bounded range from a verified reference handle in the campaign index. Never accepts a filesystem path.',[],reference),
      ('artifact_publish','Publish supplied data and return a committed artifact receipt. JSON encoding uses canonical JSON; strings remain JSON strings.',upload,publish),
      ('attempt_execute','Invoke a permitted target operation. Use direct input normally, or an artifact receipt returned by artifact_publish for large/reusable input. Operator assigns bookkeeping; never invent descriptors or receipts.',['engine.attempt_execute'],attempt),
      ('injection_delete','Delete a retained injection using its known attempt receipt and action ID.',['engine.injection_delete'],load('engine-injection-delete-request')),
      ('observation_read','Read permitted feedback bytes using the host attempt receipt and observation entry ID.',['engine.observation_read'],load('engine-observation-read-request')),
      ('record_append','Append a typed hypothesis, progress, lineage or coverage assertion. These records do not establish observed effects.',['engine.record_append'],records),
      ('finish','Finish with a minimal honest assessment: summary and objective outcomes. Cite only returned execution receipts. Empty references are valid for untested objectives. Operator retains the assessment and starts cleanup; acceptance is not cleanup completion.',['engine.finish'],finish),
      ('restore_request','Restore a known checkpoint while retaining this conversation. Success skips later calls from the current model response.',['engine.restore_request'],load('engine-restore-request')),
      ('snapshot_request','Create a checkpoint with optional label and description; the host supplies campaign metadata.',['engine.snapshot_request'],load('engine-snapshot-request')),
      ('snapshot_list','List checkpoints available to this campaign, with bounded pagination.',['engine.snapshot_list'],load('engine-snapshot-list-request')),
      ('snapshot_inspect','Inspect a checkpoint and its metadata by source-session/checkpoint identity.',['engine.snapshot_inspect'],load('engine-snapshot-inspect-request')),
    ]
    branches=[]
    for name,description,operations,arguments in sorted(entries):
        branch=object_schema(dict(name={'const':name},arguments=arguments))
        provider=arguments
        if name=='attempt_execute':
            provider=inline(documents['urn:operator:schema:engine-attempt-request:v1alpha3'],'urn:operator:schema:engine-attempt-request:v1alpha3',compact=True)
            branch['x-operator-provider-parameters']=provider
        projected=openai_parameters(name,provider)
        if projected is not None:branch['x-operator-openai-parameters']=projected
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
