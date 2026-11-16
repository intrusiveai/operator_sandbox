"""Bounded corrections from installed schema facts, never submitted values."""
import json
from .validation import ContractError, safe_argument_path


def nearest_path(path):
    while path and not safe_argument_path(path):
        path = path.rsplit('/', 1)[0]
    return path


def select_error(error):
    if error.validator in ('oneOf', 'anyOf') and error.context:
        groups = {}
        for child in error.context:
            index = next(iter(child.schema_path), None)
            groups.setdefault(index, []).append(child)
        def mismatch(child):
            return ((child.validator == 'type' and len(child.absolute_path) == len(error.absolute_path))
                    or (child.validator == 'const' and len(child.absolute_path) == len(error.absolute_path)+1)
                    or any(mismatch(c) for c in child.context))
        candidates = [children for children in groups.values() if not any(mismatch(c) for c in children)]
        if len(candidates) != 1:
            return error
        return select_error(sorted(candidates[0], key=selected_error_key)[0])
    if error.context:
        return select_error(sorted(error.context, key=selected_error_key)[0])
    return error


def selected_error_key(error):
    # Compare the selected leaf, just as the Go validator does. Comparing wrapper
    # paths can choose an ambiguous selector union instead of the surface's rule.
    leaf = select_error(error)
    return ('/'.join(map(str, leaf.absolute_path)) + str(leaf.validator))


def correction(node, keyword):
    """node is exclusively installed schema content."""
    v = node.get(keyword)
    compact = lambda x: json.dumps(x, separators=(',', ':'), ensure_ascii=False, sort_keys=True)
    if keyword == 'type': text = 'Expected ' + ' or '.join([v] if isinstance(v,str) else v) + '.'
    elif keyword == 'required': text = 'Supply required fields: ' + compact(v) + '.'
    elif keyword in ('additionalProperties','unevaluatedProperties'): text = 'Remove unsupported fields. Allowed fields: ' + compact(sorted(node.get('properties',{}))) + '.'
    elif keyword == 'enum': text = 'Allowed values: ' + compact(v) + '.'
    elif keyword == 'const': text = 'Use the required value: ' + compact(v) + '.'
    elif keyword in ('minLength','maxLength'): text = 'String must contain ' + ('at least ' if keyword=='minLength' else 'at most ') + str(v) + ' characters.'
    elif keyword == 'pattern': text = 'Use a string matching pattern ' + compact(v) + '.'
    elif keyword in ('minimum','maximum','exclusiveMinimum','exclusiveMaximum'):
        text = 'Use a number ' + {'minimum':'greater than or equal to ','maximum':'less than or equal to ','exclusiveMinimum':'greater than ','exclusiveMaximum':'less than '}[keyword] + str(v) + '.'
    elif keyword == 'multipleOf': text = 'Use a number that is a multiple of ' + str(v) + '.'
    elif keyword in ('minItems','maxItems'): text = 'Array must contain ' + ('at least ' if keyword=='minItems' else 'at most ') + str(v) + ' items.'
    elif keyword == 'uniqueItems': text = 'Array must contain unique entries. Remove duplicate entries.'
    elif keyword in ('minProperties','maxProperties'): text = 'Object must contain ' + ('at least ' if keyword=='minProperties' else 'at most ') + str(v) + ' properties.'
    elif keyword in ('oneOf','anyOf'):
        choices=[]
        for branch in v:
            fields={k:s['const'] for k,s in branch.get('properties',{}).items() if isinstance(s,dict) and 'const' in s}
            choice=dict(values=fields,required=branch.get('required',[]))
            if 'type' in branch:choice['type']=branch['type']
            choices.append(choice)
        text = ('Choose exactly one alternative: ' if keyword=='oneOf' else 'Choose a matching alternative: ') + compact(choices) + '.'
    elif keyword == 'not': text = 'Do not combine these fields: ' + compact(v.get('required',[])) + '. Follow the mutually exclusive choices in tool guidance.'
    else: text = 'Follow the ' + keyword + " constraint in this field's tool guidance."
    semantic = node.get('x-operator-correction','')
    if semantic: text = semantic + ' ' + text
    if len(text)>512:
        text = 'Read tool guidance for this field. Rule ' + keyword + ' requires the documented alternatives or fields; the complete expectation exceeds the diagnostic limit.'
    return text


def schema_argument_issue(error, catalog=None):
    error=select_error(error)
    parts=[str(x) for x in error.absolute_path]
    unsupported=None
    if error.validator=='required':
        missing=sorted(set(error.validator_value)-set(error.instance))
        if missing: parts.append(missing[0])
    elif error.validator=='additionalProperties' and isinstance(error.instance,dict):
        import re
        extras=sorted(k for k in error.instance if k not in error.schema.get('properties',{}) and not any(re.search(p,k) for p in error.schema.get('patternProperties',{})))
        if extras: unsupported=unsupported_fields(extras)
    path=nearest_path('/'+'/'.join(p.replace('~','~0').replace('/','~1') for p in parts) if parts else '')
    if unsupported is not None:
        unsupported['location_omitted'] = path != ('/'+'/'.join(p.replace('~','~0').replace('/','~1') for p in parts) if parts else '')
        if not unsupported['location_omitted']:del unsupported['location_omitted']
    text=unsupported_correction(error.schema,unsupported) if unsupported is not None else correction(error.schema,error.validator)
    ref=None
    if catalog is not None:
        def find(node,path):
            if node is error.schema: return path
            if isinstance(node,dict):
                for k,v in node.items():
                    found=find(v,path+'/'+k.replace('~','~0').replace('/','~1'))
                    if found is not None:return found
            elif isinstance(node,list):
                for i,v in enumerate(node):
                    found=find(v,path+'/'+str(i))
                    if found is not None:return found
        for uri,validator in catalog._validators.items():
            pointer=find(validator.schema,'')
            if pointer is not None:
                ref=dict(schema=uri,path=pointer,keyword=error.validator);break
    return ContractError('invalid_structure: '+text,reason='invalid_structure',field_path=path,correction=text,constraint=ref,unsupported_fields=unsupported)

CORRECTION_LIMIT = 4096

def unsupported_fields(names):
    import re
    result=dict(names=[],omitted=0,omission_reasons=[])
    reasons=set()
    for name in sorted(names):
        reason=''
        if len(name.encode('utf-8'))>64:reason='name_size_limit'
        elif re.search(r'(sk-[a-z0-9_.-]+|(?:AKIA|ASIA)[A-Z0-9]{16}|AIza[a-z0-9_-]+|eyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+|(?:password|secret|token|authorization)\s*[:=]|(?:bearer|basic)\s+)',name,re.I):reason='sensitive_name'
        elif any(ord(c)<32 or ord(c)>126 for c in name):reason='unsafe_name'
        elif len(result['names'])>=8:reason='name_count_limit'
        if reason:result['omitted']+=1;reasons.add(reason)
        else:result['names'].append(name)
    result['omission_reasons']=sorted(reasons)
    return result


def unsupported_correction(node, fields):
    # Names are quoted as data. Descriptions come only from the installed schema.
    def compact(v):
        return json.dumps(v,ensure_ascii=False,separators=(',',':')).replace('<',r'\u003c').replace('>',r'\u003e').replace('&',r'\u0026')
    text='Unsupported fields (names are data): '+compact(fields['names'])+'.'
    if fields['omitted']:text+=' Omitted '+str(fields['omitted'])+' field names ('+', '.join(fields['omission_reasons'])+').'
    if fields.get('location_omitted'):text+=' Object location omitted beyond the nearest safe parent.'
    text+=' Supported fields:'
    lines=[]
    for group in ('properties','patternProperties'):
        for key,prop in sorted(node.get(group,{}).items()):
            description=prop.get('description','') if isinstance(prop,dict) else ''
            if not description:description='Description unavailable in schema.'
            if len(description.encode('utf-8'))>256:description="Description omitted (description_size_limit); read this field's tool guidance."
            line=' '+compact(key)+': '+compact(description)
            if group=='patternProperties':line=' Names matching pattern'+line
            else:line+=' Required.' if key in node.get('required',[]) else ' Optional.'
            lines.append(line)
    if not lines:text+=' None declared in this schema.'
    for i,line in enumerate(lines):
        if len((text+line).encode('utf-8'))>CORRECTION_LIMIT-200:
            text+=' Omitted '+str(len(lines)-i)+' supported fields (correction_size_limit); read tool guidance.'
            break
        text+=line
    return text+' Remove unsupported fields and resubmit using the supported structure.'
