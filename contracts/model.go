package contracts

// ValidateModelRequest validates only the relay envelope. Conversation JSON is
// opaque; request configuration is constructed by the host adapter.
func (p *Protocol) ValidateModelRequest(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(EngineModelGenerateRequestSchema, raw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	return v.(map[string]any), nil
}

// ModelMetrics is host-validated accounting. Unknown usage is never zero usage.
type ModelMetrics struct {
	Known        bool  `json:"known"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	ToolCalls    int64 `json:"tool_calls"`
}

const anthropicCodec = "anthropic-messages-text-tools-v1"
const bedrockCodec = "bedrock-converse-text-tools-v1"
const geminiCodec = "gemini-text-tools-v1"
const responsesCodec = "openai-responses-text-tools-v1"
