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
