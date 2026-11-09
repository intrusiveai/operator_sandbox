package modelcodec

import "github.com/intrusiveai/operator_sandbox/contracts"

// Continue is used by controlled/live provider probes; Harness maintains its own
// native conversation. Content and reasoning blocks are preserved, not re-created.
func Continue(codec string, parsed Parsed, results []string) ([]byte, error) {
	if len(results) != len(parsed.Calls) {
		return nil, contracts.ErrProtocol
	}
	r := parsed.Response
	var segment, blocks []any
	switch codec {
	case Chat:
		segment = []any{object(array(r["choices"])[0])["message"]}
		for i, c := range parsed.Calls {
			segment = append(segment, map[string]any{"role": "tool", "tool_call_id": c.ID, "content": results[i]})
		}
	case Responses:
		segment = append([]any{}, array(r["output"])...)
		for i, c := range parsed.Calls {
			segment = append(segment, map[string]any{"type": "function_call_output", "call_id": c.ID, "output": results[i]})
		}
	case Anthropic:
		segment = []any{map[string]any{"role": "assistant", "content": r["content"]}}
		for i, c := range parsed.Calls {
			blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": c.ID, "content": results[i]})
		}
		if len(blocks) > 0 {
			segment = append(segment, map[string]any{"role": "user", "content": blocks})
		}
	case Bedrock:
		segment = []any{object(r["output"])["message"]}
		for i, c := range parsed.Calls {
			blocks = append(blocks, map[string]any{"toolResult": map[string]any{"toolUseId": c.ID, "content": []any{map[string]any{"text": results[i]}}}})
		}
		if len(blocks) > 0 {
			segment = append(segment, map[string]any{"role": "user", "content": blocks})
		}
	case Gemini:
		assistant := object(array(r["candidates"])[0])["content"]
		segment = []any{assistant}
		for i, c := range parsed.Calls {
			f := map[string]any{"name": c.Name, "response": map[string]any{"output": results[i]}}
			original := object(object(array(object(assistant)["parts"])[c.PartIndex])["functionCall"])
			if id, ok := original["id"]; ok {
				f["id"] = id
			}
			blocks = append(blocks, map[string]any{"functionResponse": f})
		}
		if len(blocks) > 0 {
			segment = append(segment, map[string]any{"role": "user", "parts": blocks})
		}
	default:
		return nil, contracts.ErrProtocol
	}
	return raw(segment), nil
}
