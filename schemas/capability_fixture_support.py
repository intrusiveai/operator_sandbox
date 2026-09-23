"""Conformance helpers for the committed capability chain, not a runtime adapter.

Digest serialization is intentionally restricted to the fixture's ASCII keys and
safe integers. Production must use full jcs-v1 and native typed digest validators.
"""
from copy import deepcopy
import hashlib
import json

PROFILE_KINDS = {
    'black-box': ['target_output'],
    'diagnostic': ['target_output', 'operation_error', 'injection_delivery'],
    'oracle-assisted': ['target_output', 'operation_error', 'injection_delivery', 'oracle_outcome'],
}


def raw_digest(raw):
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def fixture_jcs(value):
    def check(item):
        if isinstance(item, dict):
            if any(not k.isascii() for k in item):
                raise ValueError('fixture canonicalizer only supports ASCII property names')
            for child in item.values(): check(child)
        elif isinstance(item, list):
            for child in item: check(child)
        elif isinstance(item, float) or isinstance(item, int) and abs(item) > 2**53 - 1:
            raise ValueError('fixture canonicalizer only supports safe integer numbers')
    check(value)
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False).encode()


def projection_digest(capabilities):
    return raw_digest(fixture_jcs(capabilities))


def project(source, source_raw, target_id):
    if source['api_version'] not in ['interceptor.dev/capability-manifest/v1alpha1', 'interceptor.dev/capability-manifest/v1alpha2']:
        raise ValueError('unsupported native capability version')
    if source.get('delivery_schema_profile') not in [None, 'interceptor.delivery-schema/v1']:
        raise ValueError('unsupported delivery profile')
    public = {'target_id': target_id, 'adapter': 'interceptor/v1', 'operations': [], 'actions': [], 'services': [], 'file_namespaces': [], 'evidence_classes': [], 'features': [], 'feedback_profiles': sorted(source['feedback_profiles'])}
    for op in source.get('operations') or []:
        item = {'ref': 'operation:'+op['id'], 'operation_id': op['id'], 'delivery_status': 'described' if op.get('delivery') else 'missing-input-contract'}
        for key in ['maximum_input_bytes', 'delivery']:
            if key in op: item[key] = deepcopy(op[key])
        public['operations'].append(item)
    native_ops = set(source['cross_vm_operations'])
    if {'injection.arm', 'injection.delete'} <= native_ops:
        for profile in source.get('injection_profiles') or []:
            if profile['surface'] not in ['model_tool_result','service_response','mcp_tool_result','environment_state']:
                raise ValueError('unsupported injection surface')
            item = {'ref':'action:injection:'+profile['surface'], 'action_type':'interceptor.injection/v1alpha1', 'surface':profile['surface']}
            for key in ['scopes', 'placements', 'selector_fields', 'carriers']: item[key] = sorted(set(profile[key]))
            public['actions'].append(item)
    for service in source.get('services') or []:
        item = {'ref':'service:'+service['id'], 'service_id': service['id'], 'injection_surfaces': sorted(set(service.get('injection_surfaces') or []))}
        for key in ['kind','role','transports','implementation','response_mode','mcp','endpoints','creatable_collections']:
            if key in service: item[key] = deepcopy(service[key])
        item['transports'] = sorted(set(item['transports']))
        if 'endpoints' in item: item['endpoints'].sort(key=lambda x:x['id'])
        if 'creatable_collections' in item: item['creatable_collections'].sort(key=lambda x:x['collection'])
        public['services'].append(item)
    for namespace in source.get('file_namespaces') or []:
        public['file_namespaces'].append({'ref':'file_namespace:'+namespace['id'], 'namespace_id':namespace['id'], **{k:namespace[k] for k in ['allow_create','max_files','max_file_bytes'] if k in namespace}})
    feedback = source['feedback']
    if {'observation.read','observation.content.read'} <= native_ops and feedback['view_version'] == 'interceptor.dev/observation-view/v1alpha2':
        for kind in PROFILE_KINDS['oracle-assisted']:
            profiles = [p for p in public['feedback_profiles'] if kind in PROFILE_KINDS[p]]
            if kind in feedback['kinds'] and profiles:
                public['evidence_classes'].append({'ref':'evidence:'+kind, 'feedback_kind':kind, 'profiles':profiles})
    if source['snapshot_capable'] and 'snapshot.create' in native_ops: public['features'] = ['feature:snapshots']
    for key in ['operations','actions','services','file_namespaces','evidence_classes']:
        public[key].sort(key=lambda x:x['ref'])
        refs = [x['ref'] for x in public[key]]
        if len(refs) != len(set(refs)): raise ValueError('duplicate public ID')
    return {'schema_version':'operator.dev/target-capability-manifest/v1alpha1', 'source':{'adapter':'interceptor/v1','schema_version':source['api_version'],'capability_source_digest':source['digest'],'raw_digest':raw_digest(source_raw)}, 'capabilities':public, 'capability_projection_digest':projection_digest(public)}


def reference_index(export):
    result = {}
    for key in ['operations','actions','services','file_namespaces','evidence_classes']:
        for item in export['capabilities'][key]:
            ref = item['ref']
            if ref in result: raise ValueError('duplicate capability reference')
            result[ref] = item
    for ref in export['capabilities']['features']: result[ref] = {}
    return result


def check_admission(bundle, authoring, live, native_profile='black-box', denied_refs=()):
    """Check fixture dependency semantics after schema/native validation.

    denied_refs stands in for installed policy/route checks. This does not
    implement runtime readiness, artifact staging or native request authorization.
    """
    for export in [authoring, live]:
        if export['capability_projection_digest'] != projection_digest(export['capabilities']):
            raise ValueError('projection integrity')
    target = bundle['target_requirements']
    if target['target_id'] != authoring['capabilities']['target_id'] or target['target_id'] != live['capabilities']['target_id']:
        raise ValueError('logical target mismatch')
    if target['capability_source_digest'] != authoring['source']['capability_source_digest']:
        raise ValueError('authoring source mismatch')
    pin = target.get('capability_projection_digest')
    if pin and (pin != authoring['capability_projection_digest'] or pin != live['capability_projection_digest']):
        raise ValueError('exact projection mismatch')
    old, current = reference_index(authoring), reference_index(live)
    permitted = set(PROFILE_KINDS[native_profile]) & set(PROFILE_KINDS[bundle['feedback']['requested_profile']])
    gaps = []
    def require(ref, mandatory):
        item = current.get(ref)
        usable = ref in old and item is not None and ref not in denied_refs
        if usable and ref.startswith('operation:'):
            usable = item['delivery_status'] == 'described' and 'delivery' in item
        if usable and ref.startswith('evidence:'):
            usable = item['feedback_kind'] in permitted
        if not usable:
            if mandatory: raise ValueError('required capability unavailable: '+ref)
            gaps.append(ref)
    for ref in target.get('required_capability_refs', []): require(ref, True)
    for item in bundle['objectives'] + bundle['scenarios']:
        for ref in item.get('required_capability_refs', []): require(ref, item['required'])
    for scenario in bundle['scenarios']:
        for ref in scenario['guidance']['action_refs']: require(ref, False)
    for ref in bundle['evidence']['requested_classes']: require(ref, False)
    return sorted(set(gaps))
