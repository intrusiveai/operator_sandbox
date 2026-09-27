package contracts

// ModelMetrics is derived from validated native bytes. Known=false retains the
// host's reservation; its zero-valued fields MUST NOT be treated as actual usage.
type ModelMetrics struct {
	Known        bool  `json:"known"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	ToolCalls    int64 `json:"tool_calls"`
}

func nativeRequestOK(body map[string]any) bool {
	request := body["request"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return anthropicRequestOK(request)
	}
	if body["codec_id"] == bedrockCodec {
		return bedrockRequestOK(request)
	}
	if body["codec_id"] == geminiCodec {
		return geminiRequestOK(request)
	}
	if body["codec_id"] == responsesCodec {
		return responsesRequestOK(request)
	}
	return chatRequestOK(request)
}
func nativeResponseOK(body map[string]any) bool {
	response := body["response"].(map[string]any)
	if body["codec_id"] == anthropicCodec {
		return anthropicResponseOK(response)
	}
	if body["codec_id"] == bedrockCodec {
		return bedrockResponseOK(response)
	}
	if body["codec_id"] == geminiCodec {
		return geminiResponseOK(response)
	}
	if body["codec_id"] == responsesCodec {
		return responsesResponseOK(response)
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
	if body["codec_id"] == bedrockCodec {
		return number(request["inferenceConfig"].(map[string]any)["maxTokens"]), nil
	}
	if body["codec_id"] == geminiCodec {
		return number(request["generationConfig"].(map[string]any)["maxOutputTokens"]), nil
	}
	if body["codec_id"] == responsesCodec {
		return number(request["max_output_tokens"]), nil
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
	if body["codec_id"] == bedrockCodec {
		return bedrockMetrics(response), nil
	}
	if body["codec_id"] == geminiCodec {
		return geminiMetrics(response), nil
	}
	if body["codec_id"] == responsesCodec {
		return responsesMetrics(response), nil
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
