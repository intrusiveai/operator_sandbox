"""Fixed model tools; declarations are not dispatch authorization."""
from copy import deepcopy
from .startup import require
from .validation import decode,ORDINARY_LIMIT
from .canonical import _canonical_value
from .schema_ids import MODEL_TOOL_ARGUMENTS_SCHEMA


def model_tools(protocol,codec,operations,surfaces=None):
    if surfaces is not None:
        require(type(surfaces) is list and surfaces==sorted(set(surfaces)) and all(s in ('model_tool_result','mcp_tool_result','service_response','environment_state','database_response') for s in surfaces))
    require(type(operations) is list and len(operations)<=64 and all(type(op) is str for op in operations)
            and operations==sorted(set(operations)) and all(op in protocol._operations for op in operations))
    document=protocol._catalog._validators[MODEL_TOOL_ARGUMENTS_SCHEMA].schema
    tools=[];declarations=[]
    for branch in document['oneOf']:
        if not all(op in operations for op in branch['x-operator-required-operations']):continue
        name=branch['properties']['name']['const'];parameters=deepcopy(branch.get('x-operator-provider-parameters',branch['properties']['arguments']));description=branch['description']
        if codec in ('openai-chat-text-tools-v1','openai-responses-text-tools-v1'):
            parameters=deepcopy(branch.get('x-operator-openai-parameters',parameters))
        if name=='attempt_execute' and ('engine.injection_delete' not in operations or surfaces==[]):
            for field in ('setup','injection','retain_injections'):parameters['properties'].pop(field,None)
        if name=='attempt_execute' and 'engine.injection_delete' in operations and surfaces:
            injection=parameters['properties']['setup']['items']
            injection['properties']['surface']={'enum':surfaces}
            injection['oneOf']=[branch for branch in injection['oneOf'] if branch['properties']['surface']['const'] in surfaces]
        if codec=='openai-chat-text-tools-v1':tools.append(dict(type='function',function=dict(name=name,description=description,parameters=parameters,strict=False)))
        elif codec=='openai-responses-text-tools-v1':tools.append(dict(type='function',name=name,description=description,parameters=parameters,strict=False))
        elif codec=='anthropic-messages-text-tools-v1':tools.append(dict(name=name,description=description,input_schema=parameters))
        elif codec=='bedrock-converse-text-tools-v1':tools.append(dict(toolSpec=dict(name=name,description=description,inputSchema=dict(json=parameters))))
        elif codec=='gemini-text-tools-v1':declarations.append(dict(name=name,description=description,parametersJsonSchema=parameters))
        else:require(False)
    if declarations:tools.append(dict(functionDeclarations=declarations))
    return _canonical_value(tools,ORDINARY_LIMIT)


def validate_tool_arguments(protocol,name,raw):
    from .validation import ContractError, schema_error_path
    try: value=decode(raw,ORDINARY_LIMIT)
    except ContractError as error:
        if error.correction:raise
        correction='Supply one valid UTF-8 JSON object with unique keys, finite JSON-safe numbers and bounded nesting; do not replay completed actions.'
        raise ContractError('invalid_json: '+correction,reason='invalid_json',correction=correction) from None
    if type(value) is not dict:
        raise ContractError('invalid_structure: Expected object.',reason='invalid_structure',correction='Expected object. Supply named tool arguments in one JSON object.')
    from jsonschema import ValidationError
    validator=protocol._catalog._validators[MODEL_TOOL_ARGUMENTS_SCHEMA]
    branch=next((b for b in validator.schema['oneOf'] if b['properties']['name']['const']==name),None)
    if branch is None: raise ContractError('unsupported_tool: Use a tool from the advertised catalog.',reason='unsupported_tool',field_path='/name')
    if name == "attempt_execute":
        from .simple import validate_attempt_origin
        validate_attempt_origin(value)
    from .tool_normalization import check_model_choices
    check_model_choices(name,value)
    if name == 'finish' and 'assessment' in value:
        from .tool_normalization import check_model_content
        check_model_content(protocol,name,value)
    if name in ('attempt_execute','artifact_publish'):
        from .tool_normalization import content
        content(value)
        from .tool_normalization import check_model_content
        try: check_model_content(protocol,name,value)
        except ContractError as error:
            if error.reason in ('input_depth_exceeded','direct_input_too_large','invalid_base64','content_choice','invalid_media_type','media_type_required','artifact_media_override'):raise
    try: validator.evolve(schema=branch['properties']['arguments']).validate(value)
    except ValidationError as error:
        raise schema_argument_issue(error, protocol._catalog) from None
    from .tool_normalization import check_model_content
    check_model_content(protocol, name, value)
    if name == 'snapshot_request':
        from .protocol import _description
        _description(value)
    if name in ('reference_read','observation_read'):
        value.setdefault('max_bytes',4096);value.setdefault('offset',0)
    return value


def tool_guidance(protocol,name):
    document=protocol._catalog._validators[MODEL_TOOL_ARGUMENTS_SCHEMA].schema
    for branch in document['oneOf']:
        if branch['properties']['name']['const']==name:
            guide = dict(tool=name,description=branch['description'],arguments=branch['properties']['arguments'],guidance="Use the selected resource's details_reference for valid selectors and content. Use typed handles from corresponding verified inputs or tool results; never manufacture them. Runtime rejects invalid values before dispatch.")
            if 'x-operator-guidance' in branch: guide['examples'] = deepcopy(branch['x-operator-guidance'])
            return _canonical_value(guide,ORDINARY_LIMIT)
    require(False)


from .schema_diagnostic import schema_argument_issue
