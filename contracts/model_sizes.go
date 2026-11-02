package contracts

import "encoding/json"

// ModelRequestSizes measures JSON values in the native request before transport
// adaptation. Other includes field names, delimiters and unclassified settings.
// It never estimates tokens or retains any request content.
type ModelRequestSizes struct {
	Total        int64  `json:"total_bytes"`
	Instructions int64  `json:"instruction_bytes"`
	Tools        int64  `json:"tool_definition_bytes"`
	Conversation int64  `json:"conversation_bytes"`
	Other        int64  `json:"other_bytes"`
	Basis        string `json:"basis"`
}

func MeasureModelRequest(codec string, raw []byte) (ModelRequestSizes, error) {
	v, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		return ModelRequestSizes{}, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ModelRequestSizes{}, ErrProtocol
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
			return s, ErrProtocol
		}
		s.Instructions = size(messages[0])
		for _, item := range messages[1:] {
			s.Conversation += size(item)
		}
		s.Tools = size(m["tools"])
	case responsesCodec:
		s.Instructions = size(m["instructions"])
		s.Conversation = size(m["input"])
		s.Tools = size(m["tools"])
	case anthropicCodec:
		s.Instructions = size(m["system"])
		s.Conversation = size(m["messages"])
		s.Tools = size(m["tools"])
	case bedrockCodec:
		s.Instructions = size(m["system"])
		s.Conversation = size(m["messages"])
		s.Tools = size(m["toolConfig"])
	case geminiCodec:
		s.Instructions = size(m["systemInstruction"])
		s.Conversation = size(m["contents"])
		s.Tools = size(m["tools"])
	default:
		return s, ErrProtocol
	}
	s.Other = s.Total - s.Instructions - s.Conversation - s.Tools
	if s.Other < 0 {
		return s, ErrProtocol
	}
	return s, nil
}
func DecodeModelRequestSizes(raw []byte) *ModelRequestSizes {
	v, err := Decode(raw, 4096)
	if err != nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 6 {
		return nil
	}
	for _, key := range []string{"total_bytes", "instruction_bytes", "tool_definition_bytes", "conversation_bytes", "other_bytes"} {
		n, ok := m[key].(json.Number)
		if !ok {
			return nil
		}
		i, e := n.Int64()
		if e != nil || i < 0 || i > OrdinaryLimit {
			return nil
		}
	}
	var s ModelRequestSizes
	if json.Unmarshal(raw, &s) != nil || s.Basis != "native-json-values-with-framing-remainder" || s.Total != s.Instructions+s.Tools+s.Conversation+s.Other {
		return nil
	}
	return &s
}
