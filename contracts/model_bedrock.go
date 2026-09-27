package contracts

import (
	"encoding/base64"
	"unicode/utf8"
)

const bedrockCodec = "bedrock-converse-text-tools-v1"

func bedrockCalls(content []any) []any {
	calls := []any{}
	for _, v := range content {
		if call, ok := v.(map[string]any)["toolUse"].(map[string]any); ok {
			calls = append(calls, call)
		}
	}
	return calls
}
func bedrockToolsOK(tools []any) bool {
	seen := map[string]bool{}
	for _, v := range tools {
		name := v.(map[string]any)["toolSpec"].(map[string]any)["name"].(string)
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}
func bedrockContentOK(content []any) bool {
	for _, v := range content {
		if reasoning, ok := v.(map[string]any)["reasoningContent"].(map[string]any); ok {
			if s, ok := reasoning["redactedContent"].(string); ok {
				raw, err := base64.StdEncoding.Strict().DecodeString(s)
				if err != nil || base64.StdEncoding.EncodeToString(raw) != s {
					return false
				}
			}
		}
	}
	return true
}
func bedrockRequestOK(request map[string]any) bool {
	config, toolsEnabled := request["toolConfig"].(map[string]any)
	if toolsEnabled && !bedrockToolsOK(config["tools"].([]any)) {
		return false
	}
	seen := map[string]bool{}
	pending := []string{}
	messages := request["messages"].([]any)
	for i, v := range messages {
		message := v.(map[string]any)
		role := message["role"]
		content := message["content"].([]any)
		if (i == 0 && role != "user") || !bedrockContentOK(content) {
			return false
		}
		if role == "assistant" {
			if len(pending) > 0 {
				return false
			}
			for _, v := range bedrockCalls(content) {
				id := v.(map[string]any)["toolUseId"].(string)
				if seen[id] || !toolsEnabled {
					return false
				}
				seen[id] = true
				pending = append(pending, id)
			}
			continue
		}
		for _, v := range content {
			if result, ok := v.(map[string]any)["toolResult"].(map[string]any); ok {
				if len(pending) == 0 || result["toolUseId"] != pending[0] {
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
	return len(pending) == 0 && messages[len(messages)-1].(map[string]any)["role"] == "user"
}
func bedrockMessage(response map[string]any) map[string]any {
	return response["output"].(map[string]any)["message"].(map[string]any)
}
func bedrockMetrics(response map[string]any) ModelMetrics {
	m := ModelMetrics{ToolCalls: int64(len(bedrockCalls(bedrockMessage(response)["content"].([]any))))}
	usage, ok := response["usage"].(map[string]any)
	if !ok {
		return m
	}
	m.Known = true
	m.InputTokens = number(usage["inputTokens"])
	m.OutputTokens = number(usage["outputTokens"])
	for _, key := range []string{"cacheReadInputTokens", "cacheWriteInputTokens"} {
		if value, ok := usage[key]; ok {
			m.InputTokens += number(value)
		}
	}
	m.TotalTokens = m.InputTokens + m.OutputTokens
	return m
}
func bedrockResponseOK(response map[string]any) bool {
	content := bedrockMessage(response)["content"].([]any)
	if !bedrockContentOK(content) {
		return false
	}
	calls := bedrockCalls(content)
	seen := map[string]bool{}
	for _, v := range calls {
		id := v.(map[string]any)["toolUseId"].(string)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	finish := response["stopReason"]
	if finish == "tool_use" && len(calls) == 0 {
		return false
	}
	if (finish == "end_turn" || finish == "stop_sequence") && len(calls) > 0 {
		return false
	}
	m := bedrockMetrics(response)
	if m.TotalTokens > MaxSafeInteger {
		return false
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		// Cache-inclusive accounting is authoritative even when native totalTokens
		// summarizes only uncached input plus output. Never charge both totals.
		total := number(usage["totalTokens"])
		if total != m.TotalTokens && total != number(usage["inputTokens"])+m.OutputTokens {
			return false
		}
		if details, ok := usage["cacheDetails"].([]any); ok {
			var sum int64
			last := ""
			for _, v := range details {
				detail := v.(map[string]any)
				ttl := detail["ttl"].(string)
				if ttl <= last {
					return false
				}
				last = ttl
				sum += number(detail["inputTokens"])
			}
			write := int64(0)
			if v, ok := usage["cacheWriteInputTokens"]; ok {
				write = number(v)
			}
			if sum != write {
				return false
			}
		}
	}
	return true
}
func bedrockPolicyOK(policy, request map[string]any) bool {
	if !bedrockRequestOK(request) || !bedrockToolsOK(policy["tools"].([]any)) || len(policy["prompt"].(string)) > 128<<10 ||
		request["system"].([]any)[0].(map[string]any)["text"] != policy["prompt"] || number(request["inferenceConfig"].(map[string]any)["maxTokens"]) > number(policy["max_tokens"]) {
		return false
	}
	if config, ok := request["toolConfig"].(map[string]any); ok {
		return modelToolsEqual(config["tools"], policy["tools"])
	}
	return true
}
func bedrockCorrelationOK(request, response map[string]any) bool {
	if !bedrockRequestOK(request) || !bedrockResponseOK(response) {
		return false
	}
	m := bedrockMetrics(response)
	if m.Known && m.OutputTokens > number(request["inferenceConfig"].(map[string]any)["maxTokens"]) {
		return false
	}
	if request["toolConfig"] == nil && m.ToolCalls > 0 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range request["messages"].([]any) {
		for _, c := range bedrockCalls(v.(map[string]any)["content"].([]any)) {
			seen[c.(map[string]any)["toolUseId"].(string)] = true
		}
	}
	for _, c := range bedrockCalls(bedrockMessage(response)["content"].([]any)) {
		if seen[c.(map[string]any)["toolUseId"].(string)] {
			return false
		}
	}
	return true
}
func bedrockDisposition(response map[string]any) string {
	if response["usage"] == nil {
		return "usage-unknown"
	}
	switch response["stopReason"] {
	case "tool_use":
		return "tool-calls"
	case "guardrail_intervened", "content_filtered":
		return "filtered"
	case "max_tokens", "malformed_model_output", "malformed_tool_use", "model_context_window_exceeded":
		return "truncated"
	}
	return "text"
}
func (p *Protocol) BedrockContinuation(resultRaw []byte, results []ChatToolResult) ([]byte, error) {
	v, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	body := v.(map[string]any)
	if body["codec_id"] != bedrockCodec {
		return nil, ErrProtocol
	}
	response := body["response"].(map[string]any)
	if !bedrockResponseOK(response) {
		return nil, ErrProtocol
	}
	disposition := bedrockDisposition(response)
	if disposition != "tool-calls" && disposition != "text" {
		return nil, ErrProtocol
	}
	message := bedrockMessage(response)
	calls := bedrockCalls(message["content"].([]any))
	if len(calls) != len(results) {
		return nil, ErrProtocol
	}
	segment := []any{message}
	blocks := []any{}
	for i, result := range results {
		if calls[i].(map[string]any)["toolUseId"] != result.ToolCallID || !utf8.ValidString(result.Content) || utf8.RuneCountInString(result.Content) > 1<<20 {
			return nil, ErrProtocol
		}
		blocks = append(blocks, map[string]any{"toolResult": map[string]any{"toolUseId": result.ToolCallID, "content": []any{map[string]any{"text": result.Content}}}})
	}
	if len(blocks) > 0 {
		segment = append(segment, map[string]any{"role": "user", "content": blocks})
	}
	return canonicalValue(segment, OrdinaryLimit)
}
