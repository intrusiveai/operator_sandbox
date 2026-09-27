"""Fixed model tools; declarations are not dispatch authorization."""
from copy import deepcopy
from .startup import require
from .validation import decode,ORDINARY_LIMIT
from .canonical import _canonical_value
from .schema_ids import MODEL_TOOL_ARGUMENTS_SCHEMA


def model_tools(protocol,codec,operations):
    require(type(operations) is list and len(operations)<=64 and all(type(op) is str for op in operations)
            and operations==sorted(set(operations)) and all(op in protocol._operations for op in operations))
    document=protocol._catalog._validators[MODEL_TOOL_ARGUMENTS_SCHEMA].schema
    tools=[];declarations=[]
    for branch in document['oneOf']:
        if not all(op in operations for op in branch['x-operator-required-operations']):continue
        name=branch['properties']['name']['const'];parameters=deepcopy(branch['properties']['arguments']);description=branch['description']
        if codec=='openai-chat-text-tools-v1':tools.append(dict(type='function',function=dict(name=name,description=description,parameters=parameters,strict=False)))
        elif codec=='openai-responses-text-tools-v1':tools.append(dict(type='function',name=name,description=description,parameters=parameters,strict=False))
        elif codec=='anthropic-messages-text-tools-v1':tools.append(dict(name=name,description=description,input_schema=parameters))
        elif codec=='bedrock-converse-text-tools-v1':tools.append(dict(toolSpec=dict(name=name,description=description,inputSchema=dict(json=parameters))))
        elif codec=='gemini-text-tools-v1':declarations.append(dict(name=name,description=description,parametersJsonSchema=parameters))
        else:require(False)
    if declarations:tools.append(dict(functionDeclarations=declarations))
    return _canonical_value(tools,ORDINARY_LIMIT)


def validate_tool_arguments(protocol,name,raw):
    value=decode(raw,ORDINARY_LIMIT);require(type(value) is dict)
    protocol._catalog.validate(MODEL_TOOL_ARGUMENTS_SCHEMA,_canonical_value(dict(name=name,arguments=value),ORDINARY_LIMIT))
    return value
