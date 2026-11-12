"""Build the fixed local model-tool schema from Operator-owned wire schemas."""
import argparse
from copy import deepcopy
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]/'schemas'
ID='urn:operator:schema:model-tool-arguments:v1alpha1'


def field(schema, description):
    return dict(deepcopy(schema), description=description)


def object_schema(properties,required=None):
    return dict(type='object',additionalProperties=False,properties=properties,required=list(properties) if required is None else required)


def openai_parameters(name, arguments):
    # All codecs use the same concise object declaration. Cross-field choices
    # remain in the authoritative runtime schema, not duplicated in the prompt.
    projected = deepcopy(arguments)
    for key in ('allOf', 'oneOf', 'anyOf', 'not'):
        projected.pop(key, None)
    return projected


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
            if set(siblings) <= {"description"}: return dict(target,**siblings)
            return dict(allOf=[target,siblings], **({"description": siblings["description"]} if "description" in siblings else {}))
        return {k:inline(v,base,trail,compact) for k,v in value.items() if k not in ('$schema','$id','$defs','$comment','title')}
    def load(stem,version='v1alpha1'):
        uri='urn:operator:schema:'+stem+':'+version
        return inline(documents[uri],uri)
    wire=documents['urn:operator:schema:wire-common:v1alpha1']['$defs']
    identifier=inline(wire['id'],'urn:operator:schema:wire-common:v1alpha1')
    artifact=load('engine-artifact-begin-request')
    media=artifact['properties']['artifact']['properties']['media_type']
    attempt_host=load('engine-attempt-request','v1alpha4')
    assessment=load('assessment')['properties']
    records_host=load('engine-record-append-request')['oneOf']
    finish_host=load('engine-finish-request')['properties']
    handle=dict(type='string',minLength=1,maxLength=128,pattern='^[A-Za-z0-9][A-Za-z0-9._:-]*$')
    def handles(limit=32,meaning="Use exact returned handles of this field's resource category."):
        item=dict(deepcopy(handle),description=meaning,**{'x-operator-correction':meaning})
        return dict(type='array',maxItems=limit,uniqueItems=True,items=item,description=meaning)
    content=dict(json=dict(description='Direct JSON content for the target operation or artifact.'),text=dict(type='string',maxLength=1048576,description='Direct UTF-8 text content.'),base64=dict(type='string',maxLength=1398104,description='Base64-encoded content; supply its media_type.'),artifact=field(handle,'Committed artifact handle returned by artifact_publish.'),media_type=field(media,'Media type of the supplied content.'))
    def choice(keys):return dict(oneOf=[dict(required=[key]) for key in keys])
    def content_rules(artifact=True):
        keys=['json','text','base64']+(['artifact'] if artifact else [])
        return [choice(keys),
            {'if':{'required':['json','media_type']},'then':{'properties':{'media_type':{'const':'application/json'}}}},
            {'if':{'required':['base64']},'then':{'required':['media_type']}},
            *([{'if':{'required':['artifact']},'then':{'not':{'required':['media_type']}}}] if artifact else [])]
    def origin_rule():return {'if':{'properties':{'origin':{'const':'scenario'}},'required':['origin']},'then':{'required':['scenario_id']},'else':{'not':{'required':['scenario_id']}}}
    injection=deepcopy(attempt_host['properties']['pre_actions']['items']['properties']['parameters'])
    # Database row keys are native JSON objects at the model boundary. The
    # normalizer validates the selected policy then serializes the host key.
    def native_keys(v):
        if isinstance(v,list):
            for x in v:native_keys(x)
        elif isinstance(v,dict):
            props=v.get('properties',{})
            if {'service','collection','key'} <= props.keys():
                props['key']=dict(description='Use exactly the key columns from this database policy details_reference, with non-null scalar values.',type='object',minProperties=1,maxProperties=16,propertyNames=dict(type='string',minLength=1,maxLength=128),additionalProperties={'type':['string','number','boolean']})
            for x in v.values():native_keys(x)
    native_keys(injection)
    keep=['origin','operation_id','scenario_id','caller_principal_id','rationale','retain_injections','strategy_provenance_ref','technique_refs']
    props={k:attempt_host['properties'][k] for k in keep}
    props.update(content)
    props.update(parent=field(handle,'Execution handle of the parent experiment; omit for a root attempt.'),observations=load('observation-selection')['oneOf'][1]['properties']['kinds'],
        setup=dict(type='array',maxItems=16,items=injection,description='Optional injection setup before invocation; nonempty setup requires injection content.'),injection=field(object_schema(content,[]),'Content for nonempty setup; choose exactly one of json, text, base64, artifact.'))
    props['retain_injections']['description']='Default false. Requires nonempty setup; true retains injections for explicit deletion or lifecycle cleanup.'
    props['observations']['description']='Permitted observation kinds to collect; omission selects the configured feedback default.'
    props['injection']['allOf']=content_rules()
    attempt=object_schema(props,['origin','operation_id'])
    attempt['allOf']=content_rules()+[origin_rule(),{'if':{'required':['setup'],'properties':{'setup':{'minItems':1}}},'then':{'required':['injection']},'else':{'not':{'anyOf':[{'required':['injection']},{'required':['retain_injections']}]}}}]
    def item(kind):
        source=assessment[kind]['items']; props=deepcopy(source['properties'])
        for key in ('claim_id','gap_id','hypothesis_id','attempt_receipt_refs','record_refs','observation_refs','input_refs','uncertainty_refs','provenance'):props.pop(key,None)
        props['observations']=handles(meaning='Use inspected citation handles returned by observation_read; readable handles do not establish inspection.')
        if kind!='uncertainties':props['attempts']=handles(meaning='Use execution handles returned by executed attempts; rejected submissions have no execution handle.')
        if kind in ('objectives','hypotheses','uncertainties'):props['records']=handles(meaning='Use historical record handles returned by record_append, never execution or artifact handles.')
        if kind=='hypotheses':props.update(origin=attempt['properties']['origin'],scenario_id=field(identifier,'Submitted scenario associated with scenario-origin work.'),label=dict(handle,description='Optional local hypothesis label; no earlier registration required.'),parent=dict(handle,description='Local hypothesis label or returned historical hypothesis handle. Forward references are valid; self-parenting and cycles are invalid.'))
        if kind=='coverage':props['hypothesis']=field(handle,'Local hypothesis label or returned historical hypothesis handle covered by this entry.')
        if kind=='claims':props['uncertainties']=handles(meaning="Reference a label declared in top-level uncertainties. Put explanatory prose in that entry's description.")
        if kind=='uncertainties':props.update(label=dict(handle,description='Local label referenced by claims[].uncertainties; declare once.'),inputs=handles())
        if kind=='claims':
            props['statement']['description']='The claim being assessed.'
            props['attempts']['description']='Execution handles associated with this claim.'
            props['observations']['description']='Inspected observation citation handles supporting this claim.'
        required={'objectives':['objective_id','outcome','summary'],'hypotheses':['origin','outcome','summary'],'claims':['statement','interpretation','assurance','confidence'],'coverage':['objective_id','status','reason'],'uncertainties':['kind','description']}[kind]
        result=object_schema(props,required)
        if kind=='hypotheses':result['allOf']=[origin_rule()]
        if kind=='uncertainties':result['x-operator-correction']='Supply an uncertainty object with required kind and description; put explanatory prose in description.'
        return result
    inline_props={k:deepcopy(assessment[k]) for k in ('status','summary')}
    inline_props.update(
        objectives=dict(type='array',maxItems=100,items=item('objectives'),description='Outcomes for the submitted objectives.'),
        hypotheses=dict(type='array',maxItems=100,items=item('hypotheses'),description='Optional local hypothesis definitions and outcomes; no preregistration required.'),
        claims=dict(type='array',maxItems=100,items=item('claims'),description='Evidence-backed interpretations, distinct from target execution facts.'),
        coverage=dict(type='array',maxItems=100,items=item('coverage'),description='Which objectives were tested and any remaining gaps.'),
        uncertainties=dict(type='array',maxItems=100,items=item('uncertainties'),description='Declared limitations; claims may refer to their labels.'))
    inline_props['records']=handles(256, 'Historical record handles returned by record_append; not executions or artifacts.')
    finish=object_schema(dict(finish_reason=finish_host['finish_reason'],**inline_props,artifact=field(handle,'Committed assessment artifact handle; replaces inline assessment fields.'),unavailable_reason=finish_host['unavailable']['properties']['reason']),['finish_reason'])
    finish['properties']['unavailable_reason']['description']='Why an assessment cannot be supplied; replaces inline assessment fields.'
    finish['oneOf']=[{'required':['summary','objectives'],'not':{'anyOf':[{'required':['artifact']},{'required':['unavailable_reason']}]}},*[
        {'required':[key],'not':{'anyOf':[{'required':[k]} for k in [*inline_props,*[x for x in ('artifact','unavailable_reason') if x!=key]] ]}} for key in ('artifact','unavailable_reason')]]
    variants=[]
    for branch in records_host:
        kind=branch['properties']['record_kind']['const']; old=branch['properties']['record']['properties']
        props={}; required={'hypothesis':['origin','statement'],'progress':['summary'],'lineage':['hypothesis','attempt','mutation_summary'],'coverage':['entries']}[kind]
        keep={'hypothesis':['statement','objective_refs','assumptions','predicted_observations','surface_ref'],'progress':['summary','decision','objective_refs'],'lineage':['mutation_summary'],'coverage':[]}[kind]
        props.update({k:old[k] for k in keep});props['records']=handles(meaning='Use historical record handles returned by record_append, never execution or artifact handles.')
        if kind=='hypothesis':props.update(origin=attempt['properties']['origin'],scenario_id=field(identifier,'Submitted scenario associated with scenario-origin work.'),parent=field(handle,'Returned historical hypothesis handle for the parent hypothesis.'))
        if kind=='progress':
            props.update(hypotheses=handles(meaning='Returned historical hypothesis handles.'),attempts=handles(meaning='Execution handles returned after dispatch.'),observations=handles(meaning='Inspected observation citation handles returned by observation_read.'))
            props.update({('assessment_summary' if k=='summary' else k):v for k,v in old['assessment']['properties'].items()})
        if kind=='lineage':props.update(hypothesis=field(handle,'Returned historical hypothesis handle.'),attempt=field(handle,'Execution handle for this experiment.'),parent=field(handle,'Execution handle for the parent experiment.'))
        if kind=='coverage':props['entries']=dict(type='array',maxItems=100,items=item('coverage'),description='Coverage records for the submitted objectives.')
        variant=object_schema(dict(kind={'const':kind,'description':'Record kind selecting the supported fields.'},**props),['kind',*required])
        if kind=='hypothesis':variant['allOf']=[origin_rule()]
        if kind=='progress':
            fields=['interpretation','confidence','assurance','assessment_summary']
            variant['allOf']=[{'if':{'anyOf':[{'required':[k]} for k in fields]},'then':{'required':fields},'else':{'not':{'required':['gaps']}}}]
        variants.append(variant)
    record_props={'kind':{'enum':['hypothesis','progress','lineage','coverage'],'description':'Record kind selecting the supported fields.'}}
    for variant in variants:
        for k,v in variant['properties'].items():
            if k!='kind':record_props[k]=v
    records=object_schema(record_props,['kind']);records['oneOf']=variants
    read=dict(offset=dict(type='integer',minimum=0,maximum=2**53-1,description='Zero-based byte offset at which to start reading.'),max_bytes=dict(type='integer',minimum=1,maximum=262144,description='Maximum bytes to return in this read.'))
    reference=object_schema(dict(reference=field(handle,'Returned input reference or tool-guide reference to read.'),**read),['reference'])
    observation=object_schema(dict(observation=field(handle,'Readable observation handle returned by attempt execution or a read continuation.'),**deepcopy(read)),['observation'])
    observation['properties']['max_bytes']['maximum']=1048576
    publish=object_schema(dict(purpose={'enum':['payload','carrier','supporting-data','conclusion'],'description':'Intended use of the published artifact.'},**{k:v for k,v in content.items() if k!='artifact'}),['purpose','media_type']);publish['allOf']=content_rules(False)
    upload=['engine.artifact_begin','engine.artifact_put_part','engine.artifact_commit']
    snapshot_list=load('engine-snapshot-list-request');snapshot_list['properties']['session']=field(handle,'Returned source-session handle whose snapshots should be listed.');snapshot_list['properties'].pop('source_session')
    snapshot=object_schema(dict(snapshot=dict(handle,description='Snapshot identity returned by snapshot_list/request/inspect. Inspect readiness before restore; identity alone does not imply readiness.')))
    entries=[
      ('reference_read','Select a scenario reference at offset 0: complete instructions become the active scenario in the current task; the reply is a short confirmation, with no pagination. Other references use byte ranges: offset defaults to 0, max_bytes to 4096; follow returned continuation. Read tool-guide:<tool_name> for detailed arguments and examples.',[],reference),
      ('artifact_publish','Publish large or reusable content. Supply exactly one of json, text, base64. Returns a committed artifact handle; use direct content for ordinary invocations.',upload,publish),
      ('attempt_execute','Invoke a permitted target operation. Choose origin explicitly: scenario requires scenario_id; exploratory forbids it. Supply exactly one of json, text, base64, artifact. Omitted observations selects all permitted feedback.',['engine.attempt_execute'],attempt),
      ('injection_delete','Remove an injection using its returned injection handle; does not reverse committed mutations.',['engine.injection_delete'],object_schema(dict(injection=field(handle,'Returned injection handle to remove.')))),
      ('observation_read','Read with a readable observation handle; follow continuation.arguments. Cite the returned citation for inspected bytes only, not unread or unavailable evidence. offset defaults to 0, max_bytes to 4096.',['engine.observation_read'],observation),
      ('record_append','Optional historical recording; ordinary progress does not require it. kind selects the supported fields and relationships.',['engine.record_append'],records),
      ('finish','Submit the final assessment. Acceptance ends the entire campaign, not one scenario; confirmed cleanup is a separate outcome. Correct rejected assessments without replaying completed target work.',['engine.finish'],finish),
      ('restore_request','Restore a returned snapshot handle. Success skips later calls in this model response.',['engine.restore_request'],snapshot),
      ('snapshot_request','Create a checkpoint; label and description optional. Returned handle status distinguishes identity from readiness.',['engine.snapshot_request'],load('engine-snapshot-request')),
      ('snapshot_list','List campaign checkpoints; optional session handle and pagination.',['engine.snapshot_list'],snapshot_list),
      ('snapshot_inspect','Inspect a returned snapshot handle under campaign availability checks.',['engine.snapshot_inspect'],snapshot),
    ]
    def compact_schema(value):
        if isinstance(value,list): return [compact_schema(v) for v in value]
        if not isinstance(value,dict):return value
        # Regex/string constraints remain in the full shared runtime schema.
        out={k:compact_schema(v) for k,v in value.items() if k not in ('pattern','not')}
        if out.get('type')=='object' and set(out.get('properties',{}))=={'surface','mode','scope','selector','placement'}:
            out['properties']['selector']={'type':'object'}  # Each surface branch supplies its exact selector once.
            out['description']='Surface selects matching selector, mode and placement. Read the resource contract before setup; runtime rejects invalid combinations.'
        return out
    branches=[]
    for name,description,operations,arguments in sorted(entries):
        branch=object_schema(dict(name={'const':name},arguments=arguments))
        provider=compact_schema(arguments)
        # A JSON value's precise recursive bounds are checked at runtime.
        def compact_json(v):
            if isinstance(v,list):return [compact_json(x) for x in v]
            if not isinstance(v,dict):return v
            if 'oneOf' in v and any(isinstance(x,dict) and x.get('type')=='null' for x in v['oneOf']):return {'description':'JSON; runtime checks nesting and size.'}
            out={k:compact_json(x) for k,x in v.items() if k!='x-operator-correction'}
            # The full schema/guidance retains all purposes. In the recurring
            # provider projection omit prose for scalar values already described
            # by their type/enum; preserve semantic handle corrections.
            if ('enum' in v or v.get('type') in ('string','integer','boolean')) and 'x-operator-correction' not in v:
                out.pop('description', None)
            if isinstance(out.get('description'),str) and out['description'].startswith(('Use exactly the key columns','Optional local hypothesis','Local hypothesis label','Snapshot identity','Local label referenced')):
                out.pop('description')
            if out.get('type')=='array' and out.get('items',{}).get('type')=='string':
                out.pop('description',None)
                out['items'].pop('description',None)
            props=out.get('properties',{})
            for key,meaning in {'uncertainties':'Declared top-level uncertainty labels; explanations belong in description.'}.items():
                if key in props and props[key].get('items',{}).get('type')=='string':props[key]['description']=meaning
            return out
        provider=compact_json(provider)
        if name == 'attempt_execute':
            # Advanced setup descriptions remain in tool-guide and runtime
            # diagnostics. Avoid repeating them across every surface branch in
            # each ordinary model request.
            def compact_setup(node):
                if isinstance(node, list): return [compact_setup(x) for x in node]
                if isinstance(node, dict): return {k:compact_setup(v) for k,v in node.items() if k!='description'}
                return node
            provider['properties']['setup']['items']=compact_setup(provider['properties']['setup']['items'])
            for key in ('setup','injection','retain_injections'):
                provider['properties'][key]['description']=arguments['properties'][key]['description']
        provider=openai_parameters(name,provider)
        if name == 'attempt_execute':
            branch['x-operator-guidance'] = {
                'content': 'Choose exactly one of json, text, base64, artifact. JSON uses application/json; text defaults to text/plain; base64 requires media_type; artifact forbids a media override. Use a committed payload artifact handle for target input, not an invented descriptor.',
                'parent': 'Use an existing execution handle to express a follow-up experiment; omit parent for a root attempt.',
                'setup': 'Only use advertised setup fields and admitted surfaces. Nonempty setup requires injection content; injection and retain_injections require nonempty setup. Omitted setup means none; omitted retention means false. Payload/carrier purpose must match the selected route.',
                'database_setup': {
                    'discovery': 'Read the selected database policy details_reference for permitted service/table, exact key columns, writable columns, row-creation permission and limits.',
                    'query': 'Invoke the application through its permitted operation, not arbitrary SQL or database credentials. database_response uses simulation mode: select the declared service and admitted call ordinals/request-body predicates, not HTTP paths or MCP tool names. Replace eligible text/JSON cells using /rows/<zero-based-row>/<zero-based-column>; do not change row counts, column metadata, NULL semantics, binary cells or unsupported native types.',
                    'state': 'environment_state uses fidelity mode: collection is the declared table-policy ID; key is a native JSON object with exactly its non-null scalar key columns. Field pointers target authorized writable columns. Creating a row requires explicit permission, root-replace semantics and payload keys matching the selector; this is not an upsert. Never infer permission from a successful query.',
                    'lifecycle': 'Response replacement changes selected query-result cells delivered to the application, not persistent rows; state mutation changes persistent state. Deleting an injection does not reverse committed mutations. Use an admitted ready snapshot and restore when reverting persistent state is required; successful restore skips remaining calls in that response.'}}
        if name == 'record_append':
            branch['x-operator-guidance'] = {'progress': 'Historical progress recording is optional. An assessment requires interpretation, confidence, assurance and assessment_summary together; gaps is valid only with that group. Use returned typed handles for the selected record kind.'}
        if name == 'finish':
            branch['x-operator-guidance'] = {
                'scope': 'Acceptance ends the entire campaign, not only the active scenario. Report remaining unmet objectives honestly.',
                'completion': 'Assessment acceptance, Harness exit and confirmed owned cleanup are distinct outcomes.',
                'alternatives': 'Supply inline summary and objectives, or a committed conclusion-purpose artifact handle, or unavailable_reason when no assessment can be constructed. Artifact and unavailable alternatives replace inline fields; unavailable does not claim successful assessment.',
                'minimal_inline_example': {'finish_reason':'no-useful-next-experiment','summary':'Not tested.','objectives':[{'objective_id':'objective-id','outcome':'not-tested','summary':'No attempt ran.'}]},
                'example_identity_note': 'Replace objective-id with a submitted objective ID and choose outcomes from observed facts. Handles in examples illustrate returned handles; never invent them.',
                'optional_uncertainty_example': {'uncertainties':[{'kind':'evidence-gap','description':'Only part of the response was inspected.'}]},
                'uncertainty_kinds': item('uncertainties')['properties']['kind']['enum'],
                'objective_outcomes': item('objectives')['properties']['outcome']['enum'],
                'finish_reasons': finish_host['finish_reason']['enum'],
                'assessment_statuses': assessment['status']['enum'],
                'handles': {'attempts':'Execution handles returned after dispatch; rejected submissions have none.', 'observations':'Inspected citation handles returned by observation_read, inside supported assessment items; no top-level observations.', 'records':'Historical record handles from accepted record_append only, never executions or artifacts.'},
                'uncertainty_reference_example': {'uncertainties':[{'label':'limited-feedback','kind':'unavailable-feedback','description':'Internal operation-error feedback was unavailable.'}],'claims':[{'statement':'The inspected evidence supports a limited conclusion.','interpretation':'inconclusive','assurance':'refund-behavior','confidence':'low','uncertainties':['limited-feedback']}]},
                'example_scope': 'The uncertainty-reference example is an assessment fragment, not a complete finish request.',
                'correction': 'Correct a rejected finish with a new call; do not replay completed target operations. Omit optional reporting collections when unnecessary.'}
        if name == 'observation_read':
            branch['x-operator-guidance'] = {
                'pagination_example': {'request': {'observation':'observation-7','offset':0,'max_bytes':4096}, 'result_handles': {'readable_observation':'observation-7','citation':'observation-7-range-0-4096','continuation':{'tool':'observation_read','arguments':{'observation':'observation-7','offset':4096,'max_bytes':4096}}}, 'next_request':{'observation':'observation-7','offset':4096,'max_bytes':4096}, 'finish_objective_citation':{'observations':['observation-7-range-0-4096']}},
                'database_feedback': 'When oracle-assisted feedback is enabled, inspect permitted oracle_outcome observations before the next experiment. A database_query_match positive verdict records an observed condition, not injection causality. Empty means no positive verdict was captured, not proof of attack failure. Unavailable evidence remains unknown; available entries in a partial category can still be read. Receipt bytes remain unchanged after later writes or restore; old verdicts and session-wide reports are not new-attempt results.',
                'rules': 'Use the returned continuation verbatim. readable_observation remains readable, citation represents only verified inspected bytes. continuation is absent at EOF or when no readable bytes remain. Unavailable or truncated evidence must be reported as a limitation. A partial read never makes a whole-observation citation valid. Example handles must be replaced with actual returned handles.'}
        branch['x-operator-provider-parameters']=provider
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
