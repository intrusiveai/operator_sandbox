package contracts

import "unicode/utf8"

const geminiCodec = "gemini-text-tools-v1"

func geminiCalls(parts []any) []any {
	calls := []any{}
	for _, v := range parts {
		if call, ok := v.(map[string]any)["functionCall"].(map[string]any); ok {
			calls = append(calls, call)
		}
	}
	return calls
}
func geminiToolsOK(tools []any) bool {
	seen := map[string]bool{}
	for _, v := range tools {
		for _, d := range v.(map[string]any)["functionDeclarations"].([]any) {
			name := d.(map[string]any)["name"].(string)
			if seen[name] {
				return false
			}
			seen[name] = true
		}
	}
	return true
}
func geminiPartsOK(parts []any) bool {
	for _, v := range parts {
		part := v.(map[string]any)
		if part["functionCall"] != nil && part["thought"] == true {
			return false
		}
	}
	return true
}
func geminiRequestOK(request map[string]any) bool {
	tools := request["tools"].([]any)
	if !geminiToolsOK(tools) || (len(tools) == 0 && geminiMode(request) == "ANY") {
		return false
	}
	pending := []any{}
	seen := map[string]bool{}
	messages := request["contents"].([]any)
	for i, v := range messages {
		message := v.(map[string]any)
		parts := message["parts"].([]any)
		if (i == 0 && message["role"] != "user") || !geminiPartsOK(parts) {
			return false
		}
		if message["role"] == "model" {
			if len(pending) > 0 {
				return false
			}
			for _, v := range geminiCalls(parts) {
				call := v.(map[string]any)
				if id, ok := call["id"].(string); ok {
					if seen[id] {
						return false
					}
					seen[id] = true
				}
				pending = append(pending, call)
			}
			continue
		}
		for _, v := range parts {
			if result, ok := v.(map[string]any)["functionResponse"].(map[string]any); ok {
				if len(pending) == 0 {
					return false
				}
				call := pending[0].(map[string]any)
				if result["name"] != call["name"] || result["id"] != call["id"] {
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
func geminiMode(request map[string]any) any {
	return request["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"]
}
func geminiCandidate(response map[string]any) map[string]any {
	if candidates, ok := response["candidates"].([]any); ok && len(candidates) > 0 {
		return candidates[0].(map[string]any)
	}
	return nil
}
func geminiParts(response map[string]any) []any {
	if content, ok := geminiCandidate(response)["content"].(map[string]any); ok {
		return content["parts"].([]any)
	}
	return nil
}
func geminiBlocked(response map[string]any) bool {
	feedback, ok := response["promptFeedback"].(map[string]any)
	return ok && feedback["blockReason"] != nil && feedback["blockReason"] != "BLOCK_REASON_UNSPECIFIED"
}
func optionalModelNumber(object map[string]any, key string) int64 {
	if value, ok := object[key]; ok {
		return number(value)
	}
	return 0
}
func geminiMetrics(response map[string]any) ModelMetrics {
	m := ModelMetrics{ToolCalls: int64(len(geminiCalls(geminiParts(response))))}
	if usage, ok := response["usageMetadata"].(map[string]any); ok {
		m.Known = true
		m.InputTokens = number(usage["promptTokenCount"])
		m.OutputTokens = optionalModelNumber(usage, "candidatesTokenCount") + optionalModelNumber(usage, "thoughtsTokenCount")
		m.TotalTokens = m.InputTokens + m.OutputTokens
	}
	return m
}
func geminiResponseOK(response map[string]any) bool {
	candidate := geminiCandidate(response)
	if geminiBlocked(response) {
		if candidate != nil {
			return false
		}
	} else if candidate == nil || response["modelVersion"] == nil {
		return false
	}
	parts := geminiParts(response)
	if !geminiPartsOK(parts) {
		return false
	}
	if candidate != nil && candidate["finishReason"] == "STOP" && candidate["content"] == nil {
		return false
	}
	seen := map[string]bool{}
	for _, v := range geminiCalls(parts) {
		if id, ok := v.(map[string]any)["id"].(string); ok {
			if seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	m := geminiMetrics(response)
	if m.TotalTokens > MaxSafeInteger {
		return false
	}
	if usage, ok := response["usageMetadata"].(map[string]any); ok {
		if number(usage["totalTokenCount"]) != m.TotalTokens || optionalModelNumber(usage, "cachedContentTokenCount") > m.InputTokens {
			return false
		}
		for _, pair := range [][2]string{{"promptTokensDetails", "promptTokenCount"}, {"cacheTokensDetails", "cachedContentTokenCount"}, {"candidatesTokensDetails", "candidatesTokenCount"}} {
			if details, ok := usage[pair[0]].([]any); ok {
				var sum int64
				for _, d := range details {
					sum += number(d.(map[string]any)["tokenCount"])
				}
				if sum != optionalModelNumber(usage, pair[1]) {
					return false
				}
			}
		}
	}
	return true
}
func geminiPolicyOK(policy, request map[string]any) bool {
	config := request["generationConfig"].(map[string]any)
	return geminiRequestOK(request) && geminiToolsOK(policy["tools"].([]any)) && len(policy["prompt"].(string)) <= 128<<10 &&
		request["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] == policy["prompt"] &&
		number(config["maxOutputTokens"]) <= number(policy["max_output_tokens"]) && modelToolsEqual(config["thinkingConfig"], policy["thinking_config"]) &&
		modelToolsEqual(request["tools"], policy["tools"])
}
func geminiCorrelationOK(request, response map[string]any) bool {
	if !geminiRequestOK(request) || !geminiResponseOK(response) {
		return false
	}
	m := geminiMetrics(response)
	if m.Known && m.OutputTokens > number(request["generationConfig"].(map[string]any)["maxOutputTokens"]) {
		return false
	}
	if (geminiMode(request) == "NONE" || len(request["tools"].([]any)) == 0) && m.ToolCalls > 0 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range request["contents"].([]any) {
		for _, c := range geminiCalls(v.(map[string]any)["parts"].([]any)) {
			if id, ok := c.(map[string]any)["id"].(string); ok {
				seen[id] = true
			}
		}
	}
	for _, c := range geminiCalls(geminiParts(response)) {
		if id, ok := c.(map[string]any)["id"].(string); ok && seen[id] {
			return false
		}
	}
	return true
}
func geminiDisposition(response map[string]any) string {
	if response["usageMetadata"] == nil {
		return "usage-unknown"
	}
	if geminiBlocked(response) {
		return "filtered"
	}
	switch geminiCandidate(response)["finishReason"] {
	case "STOP":
		if len(geminiCalls(geminiParts(response))) > 0 {
			return "tool-calls"
		}
		return "text"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION", "ESCALATION", "PUP_LIMITED_DISABLED":
		return "filtered"
	default:
		return "truncated"
	}
}

// GeminiToolResult selects a function-call part in this response. Native IDs may
// be absent; part positions remain unambiguous even for identical function names.
type GeminiToolResult struct {
	PartIndex int    `json:"part_index"`
	Content   string `json:"content"`
}

func (p *Protocol) GeminiContinuation(resultRaw []byte, results []GeminiToolResult) ([]byte, error) {
	v, err := p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	body := v.(map[string]any)
	if body["codec_id"] != geminiCodec {
		return nil, ErrProtocol
	}
	response := body["response"].(map[string]any)
	if !geminiResponseOK(response) {
		return nil, ErrProtocol
	}
	disposition := geminiDisposition(response)
	if disposition != "tool-calls" && disposition != "text" {
		return nil, ErrProtocol
	}
	parts := geminiParts(response)
	if len(geminiCalls(parts)) != len(results) {
		return nil, ErrProtocol
	}
	blocks := []any{}
	index := 0
	for partIndex, v := range parts {
		call, ok := v.(map[string]any)["functionCall"].(map[string]any)
		if !ok {
			continue
		}
		result := results[index]
		index++
		if result.PartIndex != partIndex || !utf8.ValidString(result.Content) || utf8.RuneCountInString(result.Content) > 1<<20 {
			return nil, ErrProtocol
		}
		reply := map[string]any{"name": call["name"], "response": map[string]any{"output": result.Content}}
		if id, ok := call["id"]; ok {
			reply["id"] = id
		}
		blocks = append(blocks, map[string]any{"functionResponse": reply})
	}
	segment := []any{geminiCandidate(response)["content"]}
	if len(blocks) > 0 {
		segment = append(segment, map[string]any{"role": "user", "parts": blocks})
	}
	return canonicalValue(segment, OrdinaryLimit)
}
