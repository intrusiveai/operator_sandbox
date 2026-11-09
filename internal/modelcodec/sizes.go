package modelcodec

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
)

// ModelRequestSizes measures JSON values in the native request before transport
// adaptation. Other includes field names, delimiters and unclassified settings.
// It never estimates tokens or retains any request content.
type ModelRequestSizes = contracts.ModelRequestSizes

func MeasureModelRequest(codec string, raw []byte) (ModelRequestSizes, error) {
	v, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	if err != nil {
		return ModelRequestSizes{}, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ModelRequestSizes{}, contracts.ErrProtocol
	}
	size := func(v any) int64 {
		if v == nil {
			return 0
		}
		b, _ := json.Marshal(v)
		return int64(len(b))
	}
	s := ModelRequestSizes{Total: int64(len(raw)), Basis: "native-json-values-with-framing-remainder"}
	switch codec {
	case "openai-chat-text-tools-v1":
		messages, ok := m["messages"].([]any)
		if !ok || len(messages) < 1 {
			return s, contracts.ErrProtocol
		}
		s.Instructions = size(messages[0])
		for _, item := range messages[1:] {
			s.Conversation += size(item)
		}
		s.Tools = size(m["tools"])
	case Responses:
		s.Instructions = size(m["instructions"])
		s.Conversation = size(m["input"])
		s.Tools = size(m["tools"])
	case Anthropic:
		s.Instructions = size(m["system"])
		s.Conversation = size(m["messages"])
		s.Tools = size(m["tools"])
	case Bedrock:
		s.Instructions = size(m["system"])
		s.Conversation = size(m["messages"])
		s.Tools = size(m["toolConfig"])
	case Gemini:
		s.Instructions = size(m["systemInstruction"])
		s.Conversation = size(m["contents"])
		s.Tools = size(m["tools"])
	default:
		return s, contracts.ErrProtocol
	}
	s.Other = s.Total - s.Instructions - s.Conversation - s.Tools
	if s.Other < 0 {
		return s, contracts.ErrProtocol
	}
	return s, nil
}
