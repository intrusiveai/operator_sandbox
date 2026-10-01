package contracts

import "unicode/utf8"

const responsesCodec = "openai-responses-text-tools-v1"

func responsesCalls(items []any) []any {
	calls := []any{}
	for _, v := range items {
		if v.(map[string]any)["type"] == "function_call" {
			calls = append(calls, v)
		}
	}
	return calls
}
func responsesToolsOK(tools []any) bool {
	seen := map[string]bool{}
	for _, v := range tools {
		name := v.(map[string]any)["name"].(string)
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}
func responsesItemsOK(items []any, complete bool) bool {
	ids, calls := map[string]bool{}, map[string]bool{}
	for _, v := range items {
		item := v.(map[string]any)
		if id, ok := item["id"].(string); ok {
			if ids[id] {
				return false
			}
			ids[id] = true
		}
		if complete && item["status"] != nil && item["status"] != "completed" {
			return false
		}
		if complete && item["type"] == "reasoning" && item["encrypted_content"] == nil {
			return false
		}
		if item["type"] == "function_call" {
			id := item["call_id"].(string)
			if calls[id] {
				return false
			}
			calls[id] = true
		}
	}
	return true
}
func responsesRefusal(items []any) bool {
	for _, v := range items {
		item := v.(map[string]any)
		if item["role"] == "assistant" {
			for _, c := range item["content"].([]any) {
				if c.(map[string]any)["type"] == "refusal" {
					return true
				}
			}
		}
	}
	return false
}
func responsesRequestOK(request map[string]any) bool {
	tools := request["tools"].([]any)
	items := request["input"].([]any)
	if !responsesToolsOK(tools) || (len(tools) == 0 && request["tool_choice"] == "required") || !responsesItemsOK(items, true) {
		return false
	}
	if items[0].(map[string]any)["role"] != "user" {
		return false
	}
	pending := []any{}
	consuming := false
	for _, v := range items {
		item := v.(map[string]any)
		if item["type"] == "function_call_output" {
			if len(pending) == 0 {
				return false
			}
			call := pending[0].(map[string]any)
			if item["call_id"] != call["call_id"] || !modelToolsEqual(item["caller"], call["caller"]) {
				return false
			}
			pending = pending[1:]
			consuming = true
			continue
		}
		if (consuming || item["role"] == "user") && len(pending) > 0 {
			return false
		}
		consuming = false
		if item["type"] == "function_call" {
			pending = append(pending, item)
		}
	}
	last := items[len(items)-1].(map[string]any)
	return len(pending) == 0 && (last["role"] == "user" || last["type"] == "function_call_output")
}
func responsesMetrics(response map[string]any) ModelMetrics {
	m := ModelMetrics{ToolCalls: int64(len(responsesCalls(response["output"].([]any))))}
	if usage, ok := response["usage"].(map[string]any); ok {
		m.Known = true
		m.InputTokens = number(usage["input_tokens"])
		m.OutputTokens = number(usage["output_tokens"])
		m.TotalTokens = number(usage["total_tokens"])
	}
	return m
}
func responsesResponseOK(response map[string]any) bool {
	if responsesRefusal(response["output"].([]any)) && len(responsesCalls(response["output"].([]any))) > 0 {
		return false
	}
	complete := response["status"] == "completed"
	if !responsesItemsOK(response["output"].([]any), complete) {
		return false
	}
	if complete && (response["error"] != nil || response["incomplete_details"] != nil) {
		return false
	}
	if response["status"] == "failed" && response["error"] == nil {
		return false
	}
	if response["status"] != "failed" && response["error"] != nil {
		return false
	}
	if response["status"] == "incomplete" && response["incomplete_details"] == nil {
		return false
	}
	if response["status"] != "incomplete" && response["incomplete_details"] != nil {
		return false
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		m := responsesMetrics(response)
		if m.InputTokens+m.OutputTokens != m.TotalTokens {
			return false
		}
		if detail, ok := usage["input_tokens_details"].(map[string]any); ok {
			if optionalModelNumber(detail, "cached_tokens") > m.InputTokens || optionalModelNumber(detail, "cache_write_tokens") > m.InputTokens {
				return false
			}
		}
		if detail, ok := usage["output_tokens_details"].(map[string]any); ok {
			if optionalModelNumber(detail, "reasoning_tokens") > m.OutputTokens {
				return false
			}
		}
	}
	return true
}
func responsesPolicyOK(policy, request map[string]any) bool {
	return responsesRequestOK(request) && responsesToolsOK(policy["tools"].([]any)) && len(policy["prompt"].(string)) <= 128<<10 &&
		request["model"] == policy["request_model"] && request["instructions"] == policy["prompt"] && number(request["max_output_tokens"]) <= number(policy["max_output_tokens"]) &&
		modelToolsEqual(request["reasoning"], policy["reasoning"]) && modelToolsEqual(request["tools"], policy["tools"])
}
func responsesCorrelationOK(request, response map[string]any) bool {
	if !responsesRequestOK(request) || !responsesResponseOK(response) {
		return false
	}
	m := responsesMetrics(response)
	if m.Known && m.OutputTokens > number(request["max_output_tokens"]) {
		return false
	}
	if request["tool_choice"] == "none" && m.ToolCalls > 0 {
		return false
	}
	for _, key := range []string{"instructions", "max_output_tokens", "tool_choice"} {
		if value, ok := response[key]; ok && value != nil && !modelToolsEqual(value, request[key]) {
			return false
		}
	}
	if tools, ok := response["tools"].([]any); ok {
		// A null output_schema is a provider-added echo default, not a changed
		// declaration. Compare copies so retained native response bytes stay intact.
		echo := make([]any, len(tools))
		for i, value := range tools {
			tool := map[string]any{}
			for key, field := range value.(map[string]any) {
				if key != "output_schema" || field != nil {
					tool[key] = field
				}
			}
			echo[i] = tool
		}
		if !modelToolsEqual(echo, request["tools"]) {
			return false
		}
	}
	if selected, ok := request["reasoning"].(map[string]any); ok {
		if actual, ok := response["reasoning"].(map[string]any); ok {
			for key, value := range selected {
				if value != nil && !modelToolsEqual(value, actual[key]) {
					return false
				}
			}
		}
	}
	ids, calls := map[string]bool{}, map[string]bool{}
	for _, v := range request["input"].([]any) {
		item := v.(map[string]any)
		if id, ok := item["id"].(string); ok {
			ids[id] = true
		}
		if item["type"] == "function_call" {
			calls[item["call_id"].(string)] = true
		}
	}
	for _, v := range response["output"].([]any) {
		item := v.(map[string]any)
		if id, ok := item["id"].(string); ok && ids[id] {
			return false
		}
		if item["type"] == "function_call" && calls[item["call_id"].(string)] {
			return false
		}
	}
	return true
}
func responsesDisposition(response map[string]any) string {
	if response["usage"] == nil {
		return "usage-unknown"
	}
	if azureFiltered(response) {
		return "filtered"
	}
	if response["status"] != "completed" {
		if details, ok := response["incomplete_details"].(map[string]any); ok && details["reason"] == "content_filter" {
			return "filtered"
		}
		return "truncated"
	}
	for _, v := range response["output"].([]any) {
		item := v.(map[string]any)
		if item["type"] == "message" {
			for _, c := range item["content"].([]any) {
				if c.(map[string]any)["type"] == "refusal" {
					return "refusal"
				}
			}
		}
	}
	if len(responsesCalls(response["output"].([]any))) > 0 {
		return "tool-calls"
	}
	return "text"
}
func (p *Protocol) ResponsesContinuation(resultRaw []byte, results []ChatToolResult) ([]byte, error) {
	v, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	body := v.(map[string]any)
	if body["codec_id"] != responsesCodec {
		return nil, ErrProtocol
	}
	response := body["response"].(map[string]any)
	if !responsesResponseOK(response) {
		return nil, ErrProtocol
	}
	disposition := responsesDisposition(response)
	if disposition != "text" && disposition != "tool-calls" && disposition != "refusal" {
		return nil, ErrProtocol
	}
	output := response["output"].([]any)
	calls := responsesCalls(output)
	if len(calls) != len(results) {
		return nil, ErrProtocol
	}
	segment := append([]any{}, output...)
	for i, r := range results {
		call := calls[i].(map[string]any)
		if r.ToolCallID != call["call_id"] || !utf8.ValidString(r.Content) || utf8.RuneCountInString(r.Content) > 1<<20 {
			return nil, ErrProtocol
		}
		item := map[string]any{"type": "function_call_output", "call_id": r.ToolCallID, "output": r.Content}
		if caller, ok := call["caller"]; ok {
			item["caller"] = caller
		}
		segment = append(segment, item)
	}
	return canonicalValue(segment, OrdinaryLimit)
}
