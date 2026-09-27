package contracts

import "unicode/utf8"

const anthropicCodec = "anthropic-messages-text-tools-v1"

func anthropicToolsOK(tools []any) bool {
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
func anthropicCalls(content any) []any {
	result := []any{}
	blocks, _ := content.([]any)
	for _, v := range blocks {
		if v.(map[string]any)["type"] == "tool_use" {
			result = append(result, v)
		}
	}
	return result
}
func anthropicRequestOK(request map[string]any) bool {
	tools := request["tools"].([]any)
	choice := request["tool_choice"].(map[string]any)["type"]
	thinking := request["thinking"].(map[string]any)
	if !anthropicToolsOK(tools) || (len(tools) == 0 && choice == "any") || (thinking["type"] != "disabled" && choice == "any") {
		return false
	}
	if thinking["type"] == "enabled" && number(thinking["budget_tokens"]) >= number(request["max_tokens"]) {
		return false
	}
	seen := map[string]bool{}
	pending := []string{}
	messages := request["messages"].([]any)
	for i, v := range messages {
		message := v.(map[string]any)
		role := message["role"]
		if i == 0 && role != "user" {
			return false
		}
		if role == "assistant" {
			if len(pending) > 0 {
				return false
			}
			for _, v := range anthropicCalls(message["content"]) {
				id := v.(map[string]any)["id"].(string)
				if seen[id] {
					return false
				}
				seen[id] = true
				pending = append(pending, id)
			}
			continue
		}
		if _, ok := message["content"].(string); ok {
			if len(pending) > 0 {
				return false
			}
			continue
		}
		for _, v := range message["content"].([]any) {
			block := v.(map[string]any)
			if block["type"] == "tool_result" {
				if len(pending) == 0 || block["tool_use_id"] != pending[0] {
					return false
				}
				pending = pending[1:]
			} else if len(pending) > 0 {
				return false
			}
		}
		if len(pending) > 0 {
			return false
		}
	}
	// A new generation must not reinterpret a completed assistant response as a
	// partial assistant prefill. The harness supplies its next user turn explicitly.
	return len(pending) == 0 && messages[len(messages)-1].(map[string]any)["role"] == "user"
}

// ModelMetrics is derived from validated native bytes. Known=false retains the
// host's reservation; its zero-valued fields MUST NOT be treated as actual usage.
type ModelMetrics struct {
	Known        bool  `json:"known"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	ToolCalls    int64 `json:"tool_calls"`
}

func anthropicMetrics(response map[string]any) ModelMetrics {
	m := ModelMetrics{ToolCalls: int64(len(anthropicCalls(response["content"])))}
	usage, ok := response["usage"].(map[string]any)
	if !ok {
		return m
	}
	m.Known = true
	m.InputTokens = number(usage["input_tokens"])
	m.OutputTokens = number(usage["output_tokens"])
	for _, key := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if usage[key] != nil {
			m.InputTokens += number(usage[key])
		}
	}
	m.TotalTokens = m.InputTokens + m.OutputTokens
	return m
}
func anthropicResponseOK(response map[string]any) bool {
	seen := map[string]bool{}
	calls := anthropicCalls(response["content"])
	for _, v := range calls {
		id := v.(map[string]any)["id"].(string)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	finish := response["stop_reason"]
	refused := response["stop_details"] != nil || finish == "refusal"
	if finish == "tool_use" && (len(calls) == 0 || refused) {
		return false
	}
	if (finish == "end_turn" || refused) && len(calls) > 0 {
		return false
	}
	m := anthropicMetrics(response)
	if m.TotalTokens > MaxSafeInteger {
		return false
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		if detail, ok := usage["cache_creation"].(map[string]any); ok {
			if usage["cache_creation_input_tokens"] == nil || number(detail["ephemeral_1h_input_tokens"])+number(detail["ephemeral_5m_input_tokens"]) != number(usage["cache_creation_input_tokens"]) {
				return false
			}
		}
		if detail, ok := usage["output_tokens_details"].(map[string]any); ok && number(detail["thinking_tokens"]) > m.OutputTokens {
			return false
		}
	}
	return true
}
func anthropicCorrelationOK(request, response map[string]any) bool {
	if !anthropicRequestOK(request) || !anthropicResponseOK(response) {
		return false
	}
	m := anthropicMetrics(response)
	if m.Known && m.OutputTokens > number(request["max_tokens"]) {
		return false
	}
	if request["tool_choice"].(map[string]any)["type"] == "none" && m.ToolCalls > 0 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range request["messages"].([]any) {
		for _, c := range anthropicCalls(v.(map[string]any)["content"]) {
			seen[c.(map[string]any)["id"].(string)] = true
		}
	}
	for _, c := range anthropicCalls(response["content"]) {
		if seen[c.(map[string]any)["id"].(string)] {
			return false
		}
	}
	return true
}
func anthropicPolicyOK(policy, request map[string]any) bool {
	return anthropicRequestOK(request) && anthropicToolsOK(policy["tools"].([]any)) && request["model"] == policy["request_model"] &&
		number(request["max_tokens"]) <= number(policy["max_tokens"]) && request["system"] == policy["prompt"] && len(policy["prompt"].(string)) <= 128<<10 &&
		modelToolsEqual(request["tools"], policy["tools"]) && modelToolsEqual(request["thinking"], policy["thinking"])
}
func anthropicDisposition(response map[string]any) string {
	if response["usage"] == nil {
		return "usage-unknown"
	}
	switch response["stop_reason"] {
	case "max_tokens", "pause_turn", "model_context_window_exceeded":
		return "truncated"
	case "refusal":
		return "refusal"
	}
	if response["stop_details"] != nil {
		return "refusal"
	}
	if response["stop_reason"] == "tool_use" {
		return "tool-calls"
	}
	return "text"
}

// AnthropicContinuation preserves all assistant content blocks, including opaque
// thinking signatures, and appends one correlated native user tool-result batch.
func (p *Protocol) AnthropicContinuation(resultRaw []byte, results []ChatToolResult) ([]byte, error) {
	v, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	body := v.(map[string]any)
	if body["codec_id"] != anthropicCodec {
		return nil, ErrProtocol
	}
	response := body["response"].(map[string]any)
	if !anthropicResponseOK(response) {
		return nil, ErrProtocol
	}
	disposition := anthropicDisposition(response)
	if disposition != "tool-calls" && disposition != "text" && disposition != "refusal" {
		return nil, ErrProtocol
	}
	calls := anthropicCalls(response["content"])
	if len(calls) != len(results) {
		return nil, ErrProtocol
	}
	segment := []any{map[string]any{"role": "assistant", "content": response["content"]}}
	blocks := []any{}
	for i, result := range results {
		if calls[i].(map[string]any)["id"] != result.ToolCallID || !utf8.ValidString(result.Content) || utf8.RuneCountInString(result.Content) > 1<<20 {
			return nil, ErrProtocol
		}
		blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": result.ToolCallID, "content": result.Content})
	}
	if len(blocks) > 0 {
		segment = append(segment, map[string]any{"role": "user", "content": blocks})
	}
	return canonicalValue(segment, OrdinaryLimit)
}

func nativeRequestOK(body map[string]any) bool {
	request := body["request"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return anthropicRequestOK(request)
	}
	return chatRequestOK(request)
}
func nativeResponseOK(body map[string]any) bool {
	response := body["response"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return anthropicResponseOK(response)
	}
	return chatResponseOK(response)
}

// ModelOutputLimit validates a request body before deriving its native output cap.
func (p *Protocol) ModelOutputLimit(raw []byte) (int64, error) {
	v, err := p.catalog.Validate(EngineModelGenerateRequestSchema, raw, OrdinaryLimit)
	if err != nil {
		return 0, err
	}
	body := v.(map[string]any)
	if !nativeRequestOK(body) {
		return 0, ErrProtocol
	}
	request := body["request"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return number(request["max_tokens"]), nil
	}
	return number(request["max_completion_tokens"]), nil
}
func (p *Protocol) ModelUsage(raw []byte) (ModelMetrics, error) {
	v, err := p.catalog.Validate(EngineModelGenerateResultSchema, raw, OrdinaryLimit)
	if err != nil {
		return ModelMetrics{}, err
	}
	body := v.(map[string]any)
	if !nativeResponseOK(body) {
		return ModelMetrics{}, ErrProtocol
	}
	response := body["response"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return anthropicMetrics(response), nil
	}
	m := ModelMetrics{ToolCalls: int64(len(chatCalls(response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any))))}
	if usage, ok := response["usage"].(map[string]any); ok {
		m.Known = true
		m.InputTokens = number(usage["prompt_tokens"])
		m.OutputTokens = number(usage["completion_tokens"])
		m.TotalTokens = number(usage["total_tokens"])
	}
	return m, nil
}
