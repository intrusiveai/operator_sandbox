"""Compact public target view; immutable details remain ordinary references."""
from .canonical import _canonical_value, raw_digest
from .validation import ORDINARY_LIMIT
from .schema_ids import TARGET_CAPABILITY_MANIFEST_SCHEMA


def compact_target_context(protocol, raw):
    target=protocol._catalog.validate(TARGET_CAPABILITY_MANIFEST_SCHEMA,raw)
    caps=target['capabilities'];files={}
    def take(item,*keys):return {k:item[k] for k in keys if k in item}
    def detail(value):
        content=_canonical_value(value,ORDINARY_LIMIT)
        identifier='target-'+raw_digest(content)[7:];files[identifier]=content
        return identifier
    result=take(target,'schema_version','source','capability_projection_digest')
    out=take(caps,'target_id','adapter','features','feedback_profiles','evidence_classes','actions')
    for group in ('operations','services','file_namespaces'):
        items=[]
        for item in caps[group]:
            short=take(item,'ref','operation_id','service_id','namespace_id','kind','role','transports','injection_surfaces','delivery_status','maximum_input_bytes')
            short['details_reference']=detail(item)
            if group=='operations' and 'delivery' in item:short['delivery']=take(item['delivery'],'description','input')
            if group=='services':
                for sub in ('endpoints','database_tables','creatable_collections'):
                    if sub in item:
                        short[sub]=[{**take(entry,'id','kind','collection'),'details_reference':detail(dict(service_id=item['service_id'],resource_kind=sub,resource=entry))} for entry in item[sub]]
            items.append(short)
        out[group]=items
    result.update(capabilities=out,details_deferred=True)
    return _canonical_value(result,ORDINARY_LIMIT),files
