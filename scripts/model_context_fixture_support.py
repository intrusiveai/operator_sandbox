"""Independent context-budget examples for native codec fixture authors."""
def enrich(cases):
    for case in cases:
        if case.get('mode') == 'context':
            m=case['context']['model']; m.setdefault('tool_selection',{'injection_surfaces':[]}); s=m.get('codec_settings',{})
            cap=s.get('max_completion_tokens',s.get('max_tokens',s.get('max_output_tokens',1024)))
            m['context_limits']=dict(maximum_prompt_tokens=32768,maximum_completion_tokens=cap,warning_threshold_tokens=26214)
            case['policy']['context_limits']=dict(m['context_limits'])
        if case.get('mode') == 'context' or not case.get('valid',True): continue
        r=case['result']; q=case['request']['request']; codec=r['codec_id']; response=r['response']
        usage=response.get('usage',response.get('usageMetadata'))
        if not isinstance(usage,dict):
            if case.get('mode')=='wire':case['valid']=False  # Unknown usage is native-only.
            continue
        incoming=usage.get('prompt_tokens',usage.get('input_tokens',usage.get('inputTokens',usage.get('promptTokenCount',0))))
        outgoing=usage.get('completion_tokens',usage.get('output_tokens',usage.get('outputTokens',usage.get('candidatesTokenCount',0))))
        if codec.startswith('anthropic'):incoming+=usage.get('cache_creation_input_tokens',0)+usage.get('cache_read_input_tokens',0)
        if codec.startswith('bedrock'):incoming+=usage.get('cacheReadInputTokens',0)+usage.get('cacheWriteInputTokens',0)
        if codec.startswith('gemini'):outgoing+=usage.get('thoughtsTokenCount',0)
        cap=q.get('max_completion_tokens',q.get('max_output_tokens',q.get('max_tokens',q.get('inferenceConfig',{}).get('maxTokens',q.get('generationConfig',{}).get('maxOutputTokens',1024)))))
        case.get('policy',{}).setdefault('context_limits',dict(maximum_prompt_tokens=32768,maximum_completion_tokens=cap,warning_threshold_tokens=26214))
        r['context_budget']=dict(input_tokens=incoming,output_tokens=outgoing,total_tokens=incoming+outgoing,
            requested_completion_tokens=cap,reserved_tokens=32768+cap,remaining_prompt_tokens=32768-incoming,
            near_prompt_limit=incoming>=26214,remaining_model_tokens=100000-incoming-outgoing,remaining_model_turns=9)
    return cases
