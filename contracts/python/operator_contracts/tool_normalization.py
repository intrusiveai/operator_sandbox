"""Model presentation -> canonical requests. Trusted resources are supplied by
Harness, never decoded from model arguments; host authorization remains separate.
"""
from copy import deepcopy
import hashlib
import json
from .canonical import _canonical_value
from .validation import ContractError, ORDINARY_LIMIT
from .simple import resolve_direct_input, validate_attempt_origin


def issue(reason, path, correction, facts=None):
    if reason=='invalid_resolution_context':correction='Internal resource resolution failed. Stop this operation and report the product error; do not supply internal identities or replay completed actions.'
    raise ContractError(reason+': '+correction, reason=reason, field_path=path, correction=correction, facts=facts)


def content(value, path=''):
    choices=[k for k in ('json','text','base64','artifact') if k in value]
    if len(choices)!=1:issue('content_choice',path,'Supply exactly one of json, text, base64 or a returned artifact handle.')
    k=choices[0]
    if k=='artifact':
        if 'media_type' in value:issue('artifact_media_override',path+'/media_type','Artifact media type comes from its committed descriptor; omit media_type.')
        return {'artifact_receipt':value[k]}
    if k=='json':
        if 'media_type' in value and value['media_type']!='application/json':issue('invalid_media_type',path+'/media_type','JSON requires application/json; omit media_type or use that value.')
        return dict(encoding='json',value=value[k])
    if k=='base64' and 'media_type' not in value:issue('media_type_required',path+'/media_type','Supply the media type of the decoded base64 bytes.')
    return dict(encoding='utf8' if k=='text' else 'base64',**{'text' if k=='text' else 'data':value[k]},media_type=value.get('media_type','text/plain'))


def check_model_content(protocol, name, value):
    if name=='finish' and 'assessment' in value:
        issue('obsolete_wrapper','/assessment','Put summary, objectives and optional assessment collections beside finish_reason; omit the assessment wrapper.')
    if name not in ('attempt_execute','artifact_publish'):return
    if name=='artifact_publish':
        content(value)
        if isinstance(value.get('base64'),str):
            import base64,binascii
            try:
                b=base64.b64decode(value['base64'],validate=True)
                if base64.b64encode(b).decode()!=value['base64']:raise ValueError()
            except (ValueError,binascii.Error):issue('invalid_base64','/base64','Supply canonical padded base64 or use JSON/UTF-8 input.')
        return
    for path, obj in [('',value)]+([('/injection',value['injection'])] if isinstance(value.get('injection'),dict) else []):
        body=content(obj,path)
        if 'artifact_receipt' not in body:
            try:resolve_direct_input(protocol,body)
            except ContractError as e:
                field={'/value':'/json','/data':'/base64'}.get(e.field_path,e.field_path)
                issue(e.reason or 'invalid_content',path+field,e.correction or 'Supply valid bounded content.')


def remap_tool_path(paths, path):
    """Longest boundary-prefix match; unknown host paths stay unchanged."""
    for key in sorted(paths,key=lambda k:(-len(k),k)):
        if path==key or path.startswith(key+'/'):return paths[key]+path[len(key):]
    return path


def normalize_tool_arguments(protocol,name,raw,context):
    """context: campaign_id, call_id, resources, database_keys (optional).

    Each resource: kind, campaign_id, value, available, inspected, live.
    Only resources selected by this call need be supplied. Result includes
    arguments, host->model paths, and observation_selections for local evidence.
    """
    args=protocol.validate_tool_arguments(name,raw)
    n=_Normalizer(protocol,context)
    result=n.normalize(name,args)
    if name=='finish':n.paths.update({k.removeprefix('/assessment'):v for k,v in list(n.paths.items()) if k.startswith('/assessment/')})
    schema=protocol._operations['engine.'+name]['request_schema'] if name not in ('artifact_publish','reference_read') else None
    if schema:
        if len(_canonical_value(result,ORDINARY_LIMIT))>protocol._operations['engine.'+name]['max_request_bytes']:issue('request_too_large','','Expanded request exceeds its operation limit; reduce content/references or use a committed artifact.')
        try:protocol._catalog.validate_value(schema,result)
        except ContractError as e:
            path=remap_tool_path(n.paths,e.field_path)
            if path==e.field_path and ('receipt' in path or 'binding' in path or '_id' in path and not any(k in path for k in ('objective_id','scenario_id','operation_id'))):
                issue('invalid_resolution_context','','Internal resource resolution failed.')
            raise ContractError(str(e),reason=e.reason,field_path=path,correction=e.correction,constraint=e.constraint,unsupported_fields=e.unsupported_fields,facts=e.facts) from None
    _canonical_value(result,ORDINARY_LIMIT)
    return dict(arguments=result,paths=n.paths,observation_selections=n.selections)


class _Normalizer:
    def __init__(self,p,ctx):
        self.p=p;self.ctx=deepcopy(ctx);self.paths={};self.selections=[];self.locals={}
        import re
        if not isinstance(ctx,dict) or any(not isinstance(ctx.get(k),str) or not re.fullmatch('[A-Za-z0-9][A-Za-z0-9._:-]{0,127}',ctx[k]) for k in ('campaign_id','call_id')):
            issue('invalid_resolution_context','','Supply the verified campaign and stable native call identity.')
        if not isinstance(ctx.get('resources',{}),dict) or len(ctx.get('resources',{}))>4096:issue('invalid_resolution_context','','Supply a bounded verified resource map.')
    def inspection_correction(self, resource):
        import re
        value = resource.get('value', {})
        candidates = sorted(h for h, r in self.ctx.get('resources', {}).items()
            if re.fullmatch('[A-Za-z0-9][A-Za-z0-9._:-]{0,63}', h)
            and r.get('kind') == 'observation' and r.get('campaign_id') == self.ctx['campaign_id']
            and r.get('available') and r.get('inspected') and r.get('value', {}).get('entry_id')
            and r['value'].get('entry_id') == value.get('entry_id')
            and r['value'].get('attempt_receipt_id') == value.get('attempt_receipt_id'))[:3]
        if candidates:
            return 'Cite inspected ranges from this entry: ' + json.dumps(candidates, separators=(',', ':')) + '. These do not establish inspection of unread bytes.'
        return 'No inspected citation for this entry is available. Call observation_read with the original readable handle, then cite the returned citation handle; follow continuation.arguments for additional pages.'

    def identity(self,path):
        return 'generated-'+hashlib.sha256((self.ctx['campaign_id']+'\n'+self.ctx['call_id']+'\n'+path).encode()).hexdigest()[:32]
    def resource(self,h,kind,path,observed=False,live=False):
        r=self.ctx.get('resources',{}).get(h)
        if not isinstance(r,dict):issue('unknown_handle',path,'Use a handle returned by a successful tool or verified campaign input; never invent one.')
        if r.get('kind')!=kind:issue('wrong_handle_kind',path,'Use a returned '+kind+' handle; other resource categories cannot substitute for it.',dict(expected_kind=kind,actual_kind=r.get('kind','unknown')))
        if r.get('campaign_id')!=self.ctx['campaign_id']:issue('foreign_handle',path,'Select a resource from this campaign.')
        if live and r.get('live') is not True:issue('stale_handle',path,'Refresh the resource in the current target binding; do not replay prior effects.')
        if observed and (r.get('available') is not True or r.get('inspected') is not True):issue('evidence_not_inspected',path,self.inspection_correction(r))
        if kind=='artifact' and r.get('available') is not True:issue('unavailable_artifact',path,'Use an available committed artifact handle.')
        if not isinstance(r.get('value'),dict):issue('invalid_resolution_context',path,'Supply the verified canonical resource fields.')
        import re
        required={'execution':['attempt_receipt_id'],'observation':['attempt_receipt_id','entry_id'],'artifact':['artifact_receipt','purpose'],'record':['record_receipt_id'],'hypothesis':['hypothesis_id'],'uncertainty':['gap_id'],'reference':['reference_id','entry_id'],'injection':['attempt_receipt_id','action_id'],'checkpoint':['source_session','checkpoint_id'],'session':['source_session']}
        for key in required[kind]:
            v=r['value'].get(key)
            if not isinstance(v,str) or not re.fullmatch('[A-Za-z0-9][A-Za-z0-9._:-]{0,127}',v):issue('invalid_resolution_context',path,'Supply valid canonical resource identifiers.')
        if 'range' in r['value']:
            extent=r['value']['range']
            if not isinstance(extent,dict) or set(extent)!= {'offset','length'} or any(type(extent[k]) is not int for k in ('offset','length')) or extent['offset']<0 or extent['length']<1 or extent['offset']+extent['length']>2**53-1:issue('invalid_resolution_context',path,'Supply a verified bounded byte range.')
        return deepcopy(r['value'])
    def ref(self,h,kind,path):
        if kind in ('hypothesis','uncertainty') and h in self.locals:
            k,v=self.locals[h]
            if k!=kind:issue('wrong_handle_kind',path,'Select a '+kind+' label.')
            return v
        if kind=='uncertainty':issue('unknown_uncertainty_label',path,"Reference a label declared in top-level uncertainties. Put explanatory prose in that entry's description; do not replay completed attempts.")
        r=self.resource(h,kind,path)
        key={'execution':'attempt_receipt_id','record':'record_receipt_id','hypothesis':'hypothesis_id','uncertainty':'gap_id'}[kind]
        if not isinstance(r.get(key),str):issue('invalid_resolution_context',path,'Supply the canonical resource identifier.')
        return r[key]
    def refs(self,a,field,kind,mp,hp,outfield):
        out=[]
        for i,h in enumerate(a.get(field,[])):
            path=mp+'/'+field+'/'+str(i);v=self.ref(h,kind,path)
            if v not in out:
                self.paths[hp+'/'+outfield+'/'+str(len(out))]=path;out.append(v)
        return out
    def observations(self,a,mp,hp,claim=True,has_field=True):
        refs=[];receipts=[]
        for i,h in enumerate(a.get('observations',[])):
            path=mp+'/observations/'+str(i);r=self.resource(h,'observation',path,observed=claim)
            ref={k:r[k] for k in ('attempt_receipt_id','entry_id','range') if k in r}
            if 'attempt_receipt_id' not in ref or 'entry_id' not in ref:issue('invalid_resolution_context',path,'Supply the observation receipt and entry.')
            if ref not in refs:
                self.paths[hp+'/observation_refs/'+str(len(refs))]=path;refs.append(ref)
            if r['attempt_receipt_id'] not in receipts:
                self.paths[hp+'/attempt_receipt_refs/'+str(len(receipts))]=path;receipts.append(r['attempt_receipt_id'])
            if not has_field:self.selections.append(dict(field_path=path,observation_ref=ref))
        for i,h in enumerate(a.get('attempts',[])):
            path=mp+'/attempts/'+str(i);v=self.ref(h,'execution',path)
            if v not in receipts:self.paths[hp+'/attempt_receipt_refs/'+str(len(receipts))]=path;receipts.append(v)
        if len(refs)>32 or len(receipts)>32:issue('reference_limit',mp,'Select at most 32 distinct observations and execution receipts per item.')
        return refs,receipts
    def expand_content(self,a,mp,hp):
        self.paths[hp]=mp+'/'+next(k for k in ('json','text','base64','artifact') if k in a)
        body=content(a,mp)
        if 'artifact' in a:
            r=self.resource(a['artifact'],'artifact',mp+'/artifact')
            allowed=('payload','carrier') if hp=='/input' else ('payload',)
            if r.get('purpose') not in allowed:issue('artifact_purpose',mp+'/artifact','Select an artifact with the required input purpose: '+', '.join(allowed)+'.')
            body={'artifact_receipt':r.get('artifact_receipt')}
            self.paths[hp+'/artifact_receipt']=mp+'/artifact'
        else:
            for model,host in [('json','value'),('text','text'),('base64','data'),('media_type','media_type')]:
                if model in a:self.paths[hp+'/'+host]=mp+'/'+model
        return body
    def provenance(self,a,mp,hp):
        validate_attempt_origin(a)
        out={k:a[k] for k in ('origin','scenario_id') if k in a}
        for k in out:self.paths[hp+'/'+k]=mp+'/'+k
        if 'parent' in a:out['parent_hypothesis_id']=self.ref(a['parent'],'hypothesis',mp+'/parent');self.paths[hp+'/parent_hypothesis_id']=mp+'/parent'
        return out
    def entry(self,kind,a,mp,hp):
        fields={'objectives':('objective_id','outcome','summary'),'claims':('statement','interpretation','assurance','confidence'),'hypotheses':('outcome','summary','objective_refs'),'coverage':('objective_id','status','reason','alternative'),'uncertainties':('kind','description')}
        out={k:deepcopy(a[k]) for k in fields[kind] if k in a}
        self.paths[hp]=mp
        for key in out:self.paths[hp+'/'+key]=mp+'/'+key
        obs,receipts=self.observations(a,mp,hp,claim=kind!='uncertainties',has_field=kind in ('claims','uncertainties'))
        if kind!='uncertainties':out['attempt_receipt_refs']=receipts
        if kind in ('claims','uncertainties'):out['observation_refs']=obs
        if kind in ('objectives','hypotheses','uncertainties'):out['record_refs']=self.refs(a,'records','record',mp,hp,'record_refs')
        if kind=='claims':
            out['claim_id']=self.identity(mp);out['uncertainty_refs']=self.refs(a,'uncertainties','uncertainty',mp,hp,'uncertainty_refs')
            if a['interpretation']=='supported' and not obs:issue('evidence_required',mp+'/observations','Supported claims require inspected observation handles.')
        if kind=='hypotheses':
            out['hypothesis_id']=self.identity(mp);out['objective_refs']=a.get('objective_refs',[]);out['provenance']=self.provenance(a,mp,hp+'/provenance')
        if kind in ('objectives','hypotheses') and a['outcome']=='supported' and not receipts:issue('evidence_required',mp+'/attempts','Supported outcomes require execution evidence; otherwise report untested or inconclusive.')
        if kind=='coverage':
            if 'hypothesis' in a:out['hypothesis_id']=self.ref(a['hypothesis'],'hypothesis',mp+'/hypothesis');self.paths[hp+'/hypothesis_id']=mp+'/hypothesis'
            if a['status']=='tested' and not receipts:issue('evidence_required',mp+'/attempts','Tested coverage requires executed attempts.')
        if kind=='uncertainties':
            out['gap_id']=self.identity(mp);out['input_refs']=[]
            for i,h in enumerate(a.get('inputs',[])):
                path=mp+'/inputs/'+str(i);r=self.resource(h,'reference',path)
                out['input_refs'].append({k:r[k] for k in ('entry_id','range') if k in r});self.paths[hp+'/input_refs/'+str(i)]=path
        return out
    def normalize(self,name,a):
        if name=='attempt_execute':
            out={k:a[k] for k in ('origin','operation_id','scenario_id','caller_principal_id','rationale','strategy_provenance_ref','technique_refs') if k in a}
            out['input']=self.expand_content(a,'','/input')
            if 'parent' in a:out['parent_attempt_receipt_id']=self.ref(a['parent'],'execution','/parent');self.paths['/parent_attempt_receipt_id']='/parent'
            out['observation_selection']=dict(mode='selected',kinds=a['observations']) if 'observations' in a else dict(mode='all-permitted')
            self.paths['/observation_selection']='/observations';self.paths['/observation_selection/kinds']='/observations'
            out['pre_actions']=[]
            for i,action in enumerate(a.get('setup',[])):
                mp='/setup/'+str(i);hp='/pre_actions/'+str(i)+'/parameters';action=deepcopy(action);self.paths[hp]=mp
                selector=action['selector']
                if action['surface']=='environment_state' and 'key' in selector:
                    key=selector['key'];cols=self.ctx.get('database_keys',{}).get(selector['service']+'/'+selector['collection'])
                    if cols is None:
                        if not isinstance(key,str):issue('invalid_state_key',mp+'/selector/key','Use an existing record key string from the selected state collection. Key-column objects require an advertised database table policy.')
                    else:
                        if not isinstance(cols,list) or not isinstance(key,dict) or sorted(key)!=sorted(cols) or any(v is None or isinstance(v,(dict,list)) for v in key.values()):issue('invalid_database_key',mp+'/selector/key','Supply exactly the selected database policy key columns with non-null scalar values.')
                        selector['key']=_canonical_value(key,ORDINARY_LIMIT).decode()
                out['pre_actions'].append(dict(action_id=self.identity(mp),action_type='interceptor.injection/v1alpha1',parameters=action))
            if out['pre_actions']:
                out['injection_input']=self.expand_content(a['injection'],'/injection','/injection_input');out['retain_injections']=a.get('retain_injections',False)
                if 'artifact_receipt' not in out['injection_input'] and out['injection_input'].get('encoding')!='json':issue('injection_json_required','/injection','Use canonical JSON injection content or a committed JSON artifact.')
            return out
        if name=='finish':
            out={'finish_reason':a['finish_reason']}
            if 'artifact' in a:
                r=self.resource(a['artifact'],'artifact','/artifact')
                if r.get('purpose')!='conclusion':issue('artifact_purpose','/artifact','Select a committed conclusion artifact.')
                out['artifact_receipt']=r.get('artifact_receipt');self.paths['/artifact_receipt']='/artifact';return out
            if 'unavailable_reason' in a:out['unavailable']=dict(reason=a['unavailable_reason']);self.paths['/unavailable/reason']='/unavailable_reason';return out
            for coll,kind in [('hypotheses','hypothesis'),('uncertainties','uncertainty')]:
                for i,item in enumerate(a.get(coll,[])):
                    if 'label' not in item:continue
                    label=item['label'];path='/'+coll+'/'+str(i)
                    if label in self.locals or label in self.ctx.get('resources',{}):issue('ambiguous_label',path+'/label','Use a unique local label distinct from all existing resource handles.')
                    self.locals[label]=(kind,self.identity(path))
            assessment=dict(summary=a['summary'],status=a.get('status','partial'),record_refs=self.refs(a,'records','record','','/assessment','record_refs'))
            self.paths['/assessment']='';self.paths['/assessment/record_refs']='/records'
            for coll in ('objectives','hypotheses','claims','coverage','uncertainties'):
                assessment[coll]=[self.entry(coll,item,'/'+coll+'/'+str(i),'/assessment/'+coll+'/'+str(i)) for i,item in enumerate(a.get(coll,[]))]
            parents={h['hypothesis_id']:h['provenance'].get('parent_hypothesis_id') for h in assessment['hypotheses']}
            for i,h in enumerate(assessment['hypotheses']):
                seen=set();key=h['hypothesis_id']
                while key in parents:
                    if key in seen:issue('hypothesis_cycle','/hypotheses/'+str(i)+'/parent','Remove self-parenting or the hypothesis parent cycle.')
                    seen.add(key);key=parents[key]
            out['assessment']=assessment;return out
        if name=='record_append':
            kind=a['kind'];r={};self.paths['/record']='';self.paths['/record_kind']='/kind'
            r['record_refs']=self.refs(a,'records','record','','/record','record_refs')
            if kind=='hypothesis':
                r.update(hypothesis_id=self.identity('/hypothesis'),provenance=self.provenance(a,'','/record/provenance'),statement=a['statement'])
                for k in ('objective_refs','assumptions','predicted_observations'):r[k]=a.get(k,[])
                if 'surface_ref' in a:r['surface_ref']=a['surface_ref']
            if kind=='progress':
                r.update(summary=a['summary'],objective_refs=a.get('objective_refs',[]),hypothesis_refs=self.refs(a,'hypotheses','hypothesis','','/record','hypothesis_refs'))
                r['observation_refs'],r['attempt_receipt_refs']=self.observations(a,'','/record')
                if 'decision' in a:r['decision']=a['decision']
                if 'interpretation' in a:
                    r['assessment']={k:a[k] for k in ('interpretation','confidence','assurance')};r['assessment'].update(summary=a['assessment_summary'],gaps=a.get('gaps',[]))
                    for k in r['assessment']:self.paths['/record/assessment/'+k]='/'+('assessment_summary' if k=='summary' else k)
                    if a['interpretation']=='supported' and not r['observation_refs']:issue('evidence_required','/observations','Supported progress requires inspected observation handles.')
            if kind=='lineage':
                r.update(hypothesis_id=self.ref(a['hypothesis'],'hypothesis','/hypothesis'),attempt_receipt_id=self.ref(a['attempt'],'execution','/attempt'),mutation_summary=a['mutation_summary'])
                self.paths['/record/hypothesis_id']='/hypothesis';self.paths['/record/attempt_receipt_id']='/attempt'
                if 'parent' in a:r['parent_attempt_receipt_id']=self.ref(a['parent'],'execution','/parent');self.paths['/record/parent_attempt_receipt_id']='/parent'
            if kind=='coverage':r['entries']=[self.entry('coverage',e,'/entries/'+str(i),'/record/entries/'+str(i)) for i,e in enumerate(a['entries'])]
            return dict(record_kind=kind,record=r)
        if name=='artifact_publish':
            body=self.expand_content(a,'','/content');body.pop('media_type',None)
            return dict(purpose=a['purpose'],media_type=a['media_type'],content=body)
        if name in ('observation_read','reference_read'):
            observation=name=='observation_read';key='observation' if observation else 'reference';r=self.resource(a[key],'observation' if observation else 'reference','/'+key)
            out=dict(offset=a['offset'],max_bytes=a['max_bytes'])
            if observation:out.update(receipt_id=r.get('attempt_receipt_id'),entry_id=r.get('entry_id'));self.paths['/receipt_id']='/observation';self.paths['/entry_id']='/observation'
            else:out['reference_id']=r.get('reference_id');self.paths['/reference_id']='/reference'
            if 'range' in r:
                extent=r['range'];remaining=extent['offset']+extent['length']-out['offset']
                if out['offset']<extent['offset'] or remaining<=0:issue('range_exceeded','/offset',f"This handle permits offsets [{extent['offset']},{extent['offset']+extent['length']}). Read within that range; for further pages use continuation.arguments with the original readable handle.")
                out['max_bytes']=min(out['max_bytes'],remaining)
            return out
        if name=='injection_delete':
            r=self.resource(a['injection'],'injection','/injection',live=True)
            self.paths.update({'/attempt_receipt_id':'/injection','/action_id':'/injection'})
            return {k:r.get(k) for k in ('attempt_receipt_id','action_id')}
        if name in ('snapshot_inspect','restore_request'):
            r=self.resource(a['snapshot'],'checkpoint','/snapshot');self.paths.update({'/source_session':'/snapshot','/checkpoint_id':'/snapshot'})
            return {k:r.get(k) for k in ('source_session','checkpoint_id')}
        if name=='snapshot_list':
            out={k:a[k] for k in ('offset','limit') if k in a}
            if 'session' in a:out['source_session']=self.resource(a['session'],'session','/session').get('source_session');self.paths['/source_session']='/session'
            return out
        return a


def check_model_choices(name,a):
    """Give cross-field failures a useful path before schema union diagnostics."""
    if name=='finish':
        if 'assessment' in a:issue('obsolete_wrapper','/assessment','Put summary, objectives and optional assessment collections beside finish_reason; omit the assessment wrapper.')
        inline=any(k in a for k in ('summary','objectives','status','hypotheses','claims','coverage','uncertainties','records'))
        if sum((inline,'artifact' in a,'unavailable_reason' in a))!=1:issue('finish_choice','','Supply inline summary and objectives, or artifact, or unavailable_reason; never combine these alternatives.')
        if inline:
            for k in ('summary','objectives'):
                if k not in a:issue('finish_field_required','/'+k,'Inline finish requires both summary and objectives.')
    if name=='attempt_execute' and isinstance(a.get('setup',[]),list):
        if a.get('setup') and 'injection' not in a:issue('injection_required','/injection','Nonempty setup requires injection content.')
        if not a.get('setup'):
            for k in ('injection','retain_injections'):
                if k in a:issue('setup_required','/'+k,'Injection content and retention require nonempty setup; omit them when no setup is requested.')
    if name=='record_append' and a.get('kind')=='progress':
        fields=('interpretation','confidence','assurance','assessment_summary')
        if any(k in a for k in fields):
            for k in fields:
                if k not in a:issue('assessment_fields_required','/'+k,'Supply interpretation, confidence, assurance and assessment_summary together, or omit all four.')
        elif 'gaps' in a:issue('assessment_fields_required','/gaps','gaps is only allowed with the complete progress assessment.')
