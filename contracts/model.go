package contracts

import "unicode/utf8"

// ModelPolicyFromContext builds the common safe policy from verified startup
// context, frozen prompt bytes and the installed native tool projection. The
// caller still verifies launch/package/release identity and live host authority.
func (p *Protocol) ModelPolicyFromContext(contextRaw, toolsRaw, promptRaw []byte) ([]byte, error) {
	c, err := p.ValidateEngineContext(contextRaw)
	if err != nil {
		return nil, err
	}
	m := c["model"].(map[string]any)
	if m["codec_id"] != "openai-chat-text-tools-v1" && m["codec_id"] != anthropicCodec && m["codec_id"] != bedrockCodec {
		return nil, ErrProtocol
	}
	admitted := false
	for _, op := range c["operations"].([]any) {
		if op == "engine.model_generate" {
			admitted = true
		}
	}
	if !admitted || len(promptRaw) > 128<<10 || !utf8.Valid(promptRaw) {
		return nil, ErrProtocol
	}
	effective := c["prompt"].(map[string]any)["provenance"].(map[string]any)["effective"].(map[string]any)
	if effective["digest"] != RawDigest(promptRaw) || number(effective["size_bytes"]) != int64(len(promptRaw)) {
		return nil, ErrProtocol
	}
	tools, err := Decode(toolsRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	settings := m["codec_settings"].(map[string]any)
	digest, err := objectDigest(tools, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	if settings["tools_digest"] != digest {
		return nil, ErrProtocol
	}
	policy := map[string]any{"codec_id": m["codec_id"], "profile_id": m["profile_id"], "profile_digest": m["profile_digest"],
		"request_model": m["model_id"], "prompt": string(promptRaw), "tools": tools}
	keys := []string{"instruction_role", "max_completion_tokens", "response_models"}
	if m["codec_id"] == anthropicCodec {
		keys = []string{"max_tokens", "thinking", "response_models"}
	}
	if m["codec_id"] == bedrockCodec {
		keys = []string{"max_tokens"}
	}
	for _, key := range keys {
		policy[key] = settings[key]
	}
	schema, ok := p.catalog.schemas[ModelCodecPolicySchema]
	if !ok {
		return nil, ErrCatalog
	}
	if schema.Validate(policy) != nil {
		return nil, ErrSchema
	}
	valid := false
	if m["codec_id"] == anthropicCodec {
		valid = anthropicToolsOK(tools.([]any))
	} else if m["codec_id"] == bedrockCodec {
		valid = bedrockToolsOK(tools.([]any))
	} else {
		valid = chatToolsOK(tools.([]any))
	}
	if !valid {
		return nil, ErrProtocol
	}
	return canonicalValue(policy, OrdinaryLimit)
}

// These helpers validate the pinned native subset, not provider credentials,
// live admission, journal durability, or trust in model assertions.
func chatCalls(message map[string]any) []any {
	if value, ok := message["tool_calls"].([]any); ok {
		return value
	}
	return nil
}
func chatToolsOK(tools []any) bool {
	seen := map[string]bool{}
	for _, item := range tools {
		name := item.(map[string]any)["function"].(map[string]any)["name"].(string)
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}
func modelToolsEqual(left, right any) bool {
	// Native parameter schemas can contain fractional bounds and boolean constants.
	l, err := objectDigest(left, OrdinaryLimit)
	if err != nil {
		return false
	}
	r, err := objectDigest(right, OrdinaryLimit)
	return err == nil && l == r
}
func chatRequestOK(request map[string]any) bool {
	tools := request["tools"].([]any)
	if !chatToolsOK(tools) || (len(tools) == 0 && request["tool_choice"] == "required") {
		return false
	}
	messages := request["messages"].([]any)
	seen := map[string]bool{}
	var pending []string
	for index, value := range messages {
		m := value.(map[string]any)
		role := m["role"].(string)
		if index == 0 {
			if role != "system" && role != "developer" {
				return false
			}
		} else if role == "system" || role == "developer" {
			return false
		}
		if index == 1 && role != "user" {
			return false
		}
		if role == "tool" {
			if len(pending) == 0 || m["tool_call_id"] != pending[0] {
				return false
			}
			pending = pending[1:]
			continue
		}
		if len(pending) > 0 {
			return false
		}
		calls := chatCalls(m)
		if len(calls) > 0 && m["refusal"] != nil {
			return false
		}
		for _, value := range calls {
			id := value.(map[string]any)["id"].(string)
			if seen[id] {
				return false
			}
			seen[id] = true
			pending = append(pending, id)
		}
	}
	return len(pending) == 0
}
func chatResponseOK(response map[string]any) bool {
	choice := response["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	calls := chatCalls(message)
	seen := map[string]bool{}
	for _, value := range calls {
		id := value.(map[string]any)["id"].(string)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	finish := choice["finish_reason"].(string)
	if finish == "tool_calls" && (len(calls) == 0 || message["refusal"] != nil) {
		return false
	}
	if finish == "stop" && len(calls) > 0 {
		return false
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		prompt, completion := number(usage["prompt_tokens"]), number(usage["completion_tokens"])
		if prompt+completion != number(usage["total_tokens"]) {
			return false
		}
		for _, field := range []struct {
			group, name string
			maximum     int64
		}{
			{"prompt_tokens_details", "cached_tokens", prompt}, {"completion_tokens_details", "reasoning_tokens", completion},
		} {
			if detail, ok := usage[field.group].(map[string]any); ok {
				if n, present := detail[field.name]; present && number(n) > field.maximum {
					return false
				}
			}
		}
	}
	return true
}
func modelCorrelationOK(body, result map[string]any) bool {
	for _, key := range []string{"codec_id", "profile_id", "profile_digest"} {
		if body[key] != result[key] {
			return false
		}
	}
	request, response := body["request"].(map[string]any), result["response"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return anthropicCorrelationOK(request, response)
	}
	if body["codec_id"] == bedrockCodec {
		return bedrockCorrelationOK(request, response)
	}
	if !chatRequestOK(request) || !chatResponseOK(response) {
		return false
	}
	if usage, ok := response["usage"].(map[string]any); ok && number(usage["completion_tokens"]) > number(request["max_completion_tokens"]) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range request["messages"].([]any) {
		for _, call := range chatCalls(value.(map[string]any)) {
			seen[call.(map[string]any)["id"].(string)] = true
		}
	}
	calls := chatCalls(response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any))
	if request["tool_choice"] == "none" && len(calls) > 0 {
		return false
	}
	for _, call := range calls {
		if seen[call.(map[string]any)["id"].(string)] {
			return false
		}
	}
	return true
}

// ValidateModelRequest binds a request BODY to trusted installed policy. Policy
// contains no endpoint/credentials and must be selected independently of the guest.
func (p *Protocol) ValidateModelRequest(policyRaw, requestRaw []byte) (map[string]any, error) {
	policyValue, err := p.catalog.Validate(ModelCodecPolicySchema, policyRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	value, err := p.catalog.Validate(EngineModelGenerateRequestSchema, requestRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	policy, body := policyValue.(map[string]any), value.(map[string]any)
	request := body["request"].(map[string]any)
	for _, key := range []string{"codec_id", "profile_id", "profile_digest"} {
		if body[key] != policy[key] {
			return nil, ErrProtocol
		}
	}
	if body["codec_id"] == anthropicCodec {
		if !anthropicPolicyOK(policy, request) {
			return nil, ErrProtocol
		}
		return body, nil
	}
	if body["codec_id"] == bedrockCodec {
		if !bedrockPolicyOK(policy, request) {
			return nil, ErrProtocol
		}
		return body, nil
	}
	first := request["messages"].([]any)[0].(map[string]any)
	if !chatRequestOK(request) || !chatToolsOK(policy["tools"].([]any)) ||
		request["model"] != policy["request_model"] || number(request["max_completion_tokens"]) > number(policy["max_completion_tokens"]) ||
		first["role"] != policy["instruction_role"] || first["content"] != policy["prompt"] || len(policy["prompt"].(string)) > 128<<10 ||
		!modelToolsEqual(request["tools"], policy["tools"]) {
		return nil, ErrProtocol
	}
	return body, nil
}

// ValidateModelExchange additionally binds a result BODY to request, profile,
// supported response-model IDs, usage and historical tool-call identities.
func (p *Protocol) ValidateModelExchange(policyRaw, requestRaw, resultRaw []byte) (map[string]any, error) {
	body, err := p.ValidateModelRequest(policyRaw, requestRaw)
	if err != nil {
		return nil, err
	}
	value, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	result := value.(map[string]any)
	if !modelCorrelationOK(body, result) {
		return nil, ErrProtocol
	}
	if body["codec_id"] == bedrockCodec {
		return result, nil
	} // Model is pinned by the host URL/profile.
	policy, _ := Decode(policyRaw, OrdinaryLimit)
	model := result["response"].(map[string]any)["model"]
	for _, candidate := range policy.(map[string]any)["response_models"].([]any) {
		if model == candidate {
			return result, nil
		}
	}
	return nil, ErrProtocol
}

// ModelDisposition does not authorize dispatch. Validate the bound exchange and
// host receipt first. Unknown usage is preserved, never replaced by zero.
func (p *Protocol) ModelDisposition(resultRaw []byte) (string, error) {
	value, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return "", err
	}
	response := value.(map[string]any)["response"].(map[string]any)
	if value.(map[string]any)["codec_id"] == bedrockCodec {
		if !bedrockResponseOK(response) {
			return "", ErrProtocol
		}
		return bedrockDisposition(response), nil
	}
	if value.(map[string]any)["codec_id"] == anthropicCodec {
		if !anthropicResponseOK(response) {
			return "", ErrProtocol
		}
		return anthropicDisposition(response), nil
	}
	if !chatResponseOK(response) {
		return "", ErrProtocol
	}
	return chatDisposition(response), nil
}
func chatDisposition(response map[string]any) string {
	if response["usage"] == nil {
		return "usage-unknown"
	}
	choice := response["choices"].([]any)[0].(map[string]any)
	switch choice["finish_reason"] {
	case "length":
		return "truncated"
	case "content_filter":
		return "filtered"
	}
	if choice["message"].(map[string]any)["refusal"] != nil {
		return "refusal"
	}
	if choice["finish_reason"] == "tool_calls" {
		return "tool-calls"
	}
	return "text"
}

type ChatToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// ChatContinuation creates a complete native assistant/tool segment from an
// already bound, host-recorded response. Caller supplies one result per call in
// original order, including explicit invalid/not-executed tool results. Argument
// strings remain opaque, so invalid local arguments can be corrected next turn.
func (p *Protocol) ChatContinuation(resultRaw []byte, results []ChatToolResult) ([]byte, error) {
	value, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	if value.(map[string]any)["codec_id"] != "openai-chat-text-tools-v1" {
		return nil, ErrProtocol
	}
	response := value.(map[string]any)["response"].(map[string]any)
	if !chatResponseOK(response) {
		return nil, ErrProtocol
	}
	disposition := chatDisposition(response)
	if disposition != "tool-calls" && disposition != "text" && disposition != "refusal" {
		return nil, ErrProtocol
	}
	message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	assistant := map[string]any{}
	for _, key := range []string{"role", "content", "refusal", "tool_calls"} {
		if v, ok := message[key]; ok && !(key == "tool_calls" && v == nil) {
			assistant[key] = v
		}
	}
	calls := chatCalls(message)
	if len(calls) != len(results) {
		return nil, ErrProtocol
	}
	segment := []any{assistant}
	for i, result := range results {
		if calls[i].(map[string]any)["id"] != result.ToolCallID || !utf8.ValidString(result.Content) || utf8.RuneCountInString(result.Content) > 1<<20 {
			return nil, ErrProtocol
		}
		segment = append(segment, map[string]any{"role": "tool", "tool_call_id": result.ToolCallID, "content": result.Content})
	}
	return canonicalValue(segment, OrdinaryLimit)
}
