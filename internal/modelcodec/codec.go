// Package modelcodec implements host-owned provider request construction and
// minimal response accounting. It is not part of the shared wire contract.
package modelcodec

import (
	"encoding/json"
	"fmt"
	"github.com/intrusiveai/operator_sandbox/contracts"
)

const Chat = "openai-chat-text-tools-v1"
const Responses = "openai-responses-text-tools-v1"
const Anthropic = "anthropic-messages-text-tools-v1"
const Bedrock = "bedrock-converse-text-tools-v1"
const Gemini = "gemini-text-tools-v1"

func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func array(v any) []any           { a, _ := v.([]any); return a }
func str(v any) string            { s, _ := v.(string); return s }
func raw(v any) []byte            { b, _ := json.Marshal(v); return b }
func fail(reason, path string) error {
	return &contracts.ModelValidationError{Stage: "adapter", Reason: reason, FieldPath: path, Cause: contracts.ErrProtocol}
}
func count(v any, path string) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, fail("usage_invalid", path)
	}
	i, e := n.Int64()
	if e != nil || i < 0 || i > contracts.MaxSafeInteger {
		return 0, fail("usage_invalid", path)
	}
	return i, nil
}
func OutputLimit(p map[string]any) int64 {
	key := "max_output_tokens"
	switch p["codec_id"] {
	case Chat:
		key = "max_completion_tokens"
	case Anthropic, Bedrock:
		key = "max_tokens"
	}
	v, _ := contracts.Decode(raw(p[key]), contracts.OrdinaryLimit)
	n, _ := count(v, "")
	return n
}

// Build accepts only conversation data and purpose. Host options are never
// merged from guest data. Compaction cannot request tools.
func Build(policyRaw, relayRaw []byte) ([]byte, error) {
	pv, e := contracts.Decode(policyRaw, contracts.OrdinaryLimit)
	if e != nil {
		return nil, e
	}
	p := object(pv)
	rv, e := contracts.Decode(relayRaw, contracts.OrdinaryLimit)
	if e != nil {
		return nil, e
	}
	r := object(rv)
	history, ok := r["conversation"].([]any)
	if !ok || len(r) != 2 || (r["purpose"] != "generation" && r["purpose"] != "compaction") {
		return nil, contracts.ErrProtocol
	}
	n := OutputLimit(p)
	if n < 1 {
		return nil, contracts.ErrProtocol
	}
	tools := p["tools"]
	choice := "auto"
	if r["purpose"] == "compaction" {
		tools = []any{}
		choice = "none"
	}
	var q map[string]any
	switch p["codec_id"] {
	case Chat:
		messages := append([]any{map[string]any{"role": p["instruction_role"], "content": p["prompt"]}}, history...)
		q = map[string]any{"model": p["request_model"], "messages": messages, "max_completion_tokens": n, "stream": false, "store": false, "n": 1, "parallel_tool_calls": true, "tools": tools, "tool_choice": choice}
	case Responses:
		q = map[string]any{"model": p["request_model"], "instructions": p["prompt"], "input": history, "max_output_tokens": n, "stream": false, "store": false, "parallel_tool_calls": true, "include": []string{"reasoning.encrypted_content"}, "truncation": "disabled", "tools": tools, "tool_choice": choice, "text": map[string]any{"format": map[string]any{"type": "text"}}}
		if p["reasoning"] != nil {
			q["reasoning"] = p["reasoning"]
		}
	case Anthropic:
		q = map[string]any{"model": p["request_model"], "system": p["prompt"], "messages": history, "max_tokens": n, "stream": false, "tools": tools}
		if len(array(tools)) > 0 {
			q["tool_choice"] = map[string]any{"type": "auto"}
		}
		if p["thinking"] != nil {
			q["thinking"] = p["thinking"]
		}
	case Bedrock:
		q = map[string]any{"system": []any{map[string]any{"text": p["prompt"]}}, "messages": history, "inferenceConfig": map[string]any{"maxTokens": n}}
		if len(array(tools)) > 0 {
			q["toolConfig"] = map[string]any{"tools": tools, "toolChoice": map[string]any{"auto": map[string]any{}}}
		}
	case Gemini:
		config := map[string]any{"maxOutputTokens": n, "candidateCount": 1}
		if p["thinking_config"] != nil {
			config["thinkingConfig"] = p["thinking_config"]
		}
		q = map[string]any{"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": p["prompt"]}}}, "contents": history, "generationConfig": config, "tools": tools}
		if len(array(tools)) > 0 {
			q["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "AUTO"}}
		}
	default:
		return nil, contracts.ErrProtocol
	}
	return raw(q), nil
}

type Call struct {
	ID, Name  string
	Arguments any
	PartIndex int
}
type Parsed struct {
	Metrics     contracts.ModelMetrics
	Disposition string
	Calls       []Call
	Response    map[string]any
}

// Parse inspects only fields used for accounting or dispatch. New provider
// metadata, content annotations and reasoning blocks remain opaque and retained.
func Parse(codec string, responseRaw []byte) (Parsed, error) {
	var out Parsed
	v, e := contracts.Decode(responseRaw, contracts.OrdinaryLimit)
	if e != nil {
		return out, fail("invalid_json", "/response")
	}
	r := object(v)
	if r == nil {
		return out, fail("unsupported_response_structure", "/response")
	}
	out.Response = r
	out.Disposition = "text"
	var usage map[string]any
	var inKey, outKey, totalKey, usagePath, finish, finishPath string
	var blocks []any
	switch codec {
	case Chat:
		choices := array(r["choices"])
		if len(choices) != 1 {
			return out, fail("unsupported_response_structure", "/response/choices")
		}
		c := object(choices[0])
		m := object(c["message"])
		if m == nil {
			return out, fail("unsupported_response_structure", "/response/choices/0/message")
		}
		finish = str(c["finish_reason"])
		finishPath = "/response/choices/0/finish_reason"
		if v := m["tool_calls"]; v != nil {
			var ok bool
			blocks, ok = v.([]any)
			if !ok {
				return out, fail("unsupported_response_structure", "/response/choices/0/message/tool_calls")
			}
		}
		for _, v := range blocks {
			c := object(v)
			f := object(c["function"])
			if _, ok := f["arguments"].(string); !ok {
				return out, fail("unsupported_response_structure", "/response/choices/0/message/tool_calls")
			}
			out.Calls = append(out.Calls, Call{ID: str(c["id"]), Name: str(f["name"]), Arguments: f["arguments"]})
		}
		if m["refusal"] != nil {
			out.Disposition = "refusal"
		}
		usage = object(r["usage"])
		inKey, outKey, totalKey, usagePath = "prompt_tokens", "completion_tokens", "total_tokens", "/response/usage"
	case Responses:
		var ok bool
		blocks, ok = r["output"].([]any)
		if !ok {
			return out, fail("unsupported_response_structure", "/response/output")
		}
		finish = str(r["status"])
		finishPath = "/response/status"
		for _, v := range blocks {
			c := object(v)
			if c == nil {
				return out, fail("unsupported_response_structure", "/response/output")
			}
			if c["type"] == "function_call" {
				if _, ok := c["arguments"].(string); !ok {
					return out, fail("unsupported_response_structure", "/response/output")
				}
				out.Calls = append(out.Calls, Call{ID: str(c["call_id"]), Name: str(c["name"]), Arguments: c["arguments"]})
			}
			for _, v := range array(c["content"]) {
				if object(v)["type"] == "refusal" {
					out.Disposition = "refusal"
				}
			}
		}
		usage = object(r["usage"])
		inKey, outKey, totalKey, usagePath = "input_tokens", "output_tokens", "total_tokens", "/response/usage"
	case Anthropic:
		var ok bool
		blocks, ok = r["content"].([]any)
		if !ok {
			return out, fail("unsupported_response_structure", "/response/content")
		}
		finish = str(r["stop_reason"])
		finishPath = "/response/stop_reason"
		for _, v := range blocks {
			c := object(v)
			if c == nil {
				return out, fail("unsupported_response_structure", "/response/content")
			}
			if c["type"] == "tool_use" {
				if object(c["input"]) == nil {
					return out, fail("unsupported_response_structure", "/response/content")
				}
				out.Calls = append(out.Calls, Call{ID: str(c["id"]), Name: str(c["name"]), Arguments: c["input"]})
			}
		}
		usage = object(r["usage"])
		inKey, outKey, usagePath = "input_tokens", "output_tokens", "/response/usage"
	case Bedrock:
		var ok bool
		blocks, ok = object(object(r["output"])["message"])["content"].([]any)
		if !ok {
			return out, fail("unsupported_response_structure", "/response/output/message/content")
		}
		finish = str(r["stopReason"])
		finishPath = "/response/stopReason"
		for _, v := range blocks {
			c := object(v)
			if c == nil {
				return out, fail("unsupported_response_structure", "/response/output/message/content")
			}
			if v, exists := c["toolUse"]; exists {
				t := object(v)
				if object(t["input"]) == nil {
					return out, fail("unsupported_response_structure", "/response/output/message/content")
				}
				out.Calls = append(out.Calls, Call{ID: str(t["toolUseId"]), Name: str(t["name"]), Arguments: t["input"]})
			}
		}
		usage = object(r["usage"])
		inKey, outKey, totalKey, usagePath = "inputTokens", "outputTokens", "totalTokens", "/response/usage"
	case Gemini:
		candidates := array(r["candidates"])
		if len(candidates) == 0 && object(r["promptFeedback"])["blockReason"] != nil {
			out.Disposition = "filtered"
			finish = "SAFETY"
		} else {
			if len(candidates) != 1 {
				return out, fail("unsupported_response_structure", "/response/candidates")
			}
			c := object(candidates[0])
			finish = str(c["finishReason"])
			blocks = array(object(c["content"])["parts"])
		}
		finishPath = "/response/candidates/0/finishReason"
		for i, v := range blocks {
			c := object(v)
			if c == nil {
				return out, fail("unsupported_response_structure", "/response/candidates/0/content/parts")
			}
			if v, exists := c["functionCall"]; exists {
				t := object(v)
				arguments, present := t["args"]
				if !present {
					arguments = map[string]any{}
				}
				if object(arguments) == nil {
					return out, fail("unsupported_response_structure", "/response/candidates/0/content/parts")
				}
				id := str(t["id"])
				if id == "" {
					id = fmt.Sprintf("gemini-part-%d", i)
				}
				out.Calls = append(out.Calls, Call{ID: id, Name: str(t["name"]), Arguments: arguments, PartIndex: i})
			}
		}
		usage = object(r["usageMetadata"])
		inKey, outKey, totalKey, usagePath = "promptTokenCount", "candidatesTokenCount", "totalTokenCount", "/response/usageMetadata"
	default:
		return out, fail("unsupported_response_structure", "/response")
	}
	seen := map[string]bool{}
	for _, c := range out.Calls {
		if c.ID == "" || c.Name == "" {
			return out, fail("unsupported_response_structure", "/response")
		}
		if seen[c.ID] {
			return out, fail("duplicate_tool_call_identity", "/response")
		}
		seen[c.ID] = true
	}
	switch finish {
	case "tool_calls", "tool_use":
		if len(out.Calls) == 0 {
			return out, fail("inconsistent_stop_reason", finishPath)
		}
		out.Disposition = "tool-calls"
	case "stop", "end_turn", "stop_sequence":
		if len(out.Calls) > 0 {
			return out, fail("inconsistent_stop_reason", finishPath)
		}
	case "completed", "STOP":
		if len(out.Calls) > 0 {
			out.Disposition = "tool-calls"
		}
	case "length", "max_tokens", "max_output_tokens", "MAX_TOKENS", "model_context_window_exceeded", "malformed_model_output", "malformed_tool_use", "MALFORMED_FUNCTION_CALL", "LANGUAGE", "OTHER", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE", "MALFORMED_RESPONSE":
		out.Disposition = "truncated"
	case "content_filter", "content_filtered", "guardrail_intervened", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "ESCALATION":
		out.Disposition = "filtered"
	case "refusal":
		out.Disposition = "refusal"
	case "incomplete", "failed", "cancelled", "pause_turn":
		out.Disposition = "truncated"
	default:
		return out, fail("unsupported_response_structure", finishPath)
	}
	if codec == Anthropic && object(r["stop_details"])["type"] == "refusal" {
		out.Disposition = "refusal"
	}
	if filtered(r) || (codec == Responses && object(r["incomplete_details"])["reason"] == "content_filter") {
		out.Disposition = "filtered"
	}
	out.Metrics.ToolCalls = int64(len(out.Calls))
	if usage == nil {
		out.Disposition = "usage-unknown"
		return out, nil
	}
	incoming, e := count(usage[inKey], usagePath+"/"+inKey)
	if e != nil {
		return out, e
	}
	outgoingValue := usage[outKey]
	if codec == Gemini && outgoingValue == nil {
		outgoingValue = json.Number("0")
	}
	outgoing, e := count(outgoingValue, usagePath+"/"+outKey)
	if e != nil {
		return out, e
	}
	base := incoming
	extras := []string{}
	if codec == Anthropic {
		extras = []string{"cache_creation_input_tokens", "cache_read_input_tokens"}
	}
	if codec == Bedrock {
		extras = []string{"cacheReadInputTokens", "cacheWriteInputTokens"}
	}
	for _, key := range extras {
		if v, ok := usage[key]; ok {
			n, e := count(v, usagePath+"/"+key)
			if e != nil {
				return out, e
			}
			if n > contracts.MaxSafeInteger-incoming {
				return out, fail("usage_invalid", usagePath)
			}
			incoming += n
		}
	}
	if codec == Gemini {
		if v, ok := usage["thoughtsTokenCount"]; ok {
			n, e := count(v, usagePath+"/thoughtsTokenCount")
			if e != nil {
				return out, e
			}
			if n > contracts.MaxSafeInteger-outgoing {
				return out, fail("usage_invalid", usagePath)
			}
			outgoing += n
		}
	}
	if outgoing > contracts.MaxSafeInteger-incoming {
		return out, fail("usage_invalid", usagePath)
	}
	total := incoming + outgoing
	if totalKey != "" {
		n, e := count(usage[totalKey], usagePath+"/"+totalKey)
		if e != nil {
			return out, e
		}
		if n != total && !(codec == Bedrock && n == base+outgoing) {
			return out, fail("usage_invalid", usagePath+"/"+totalKey)
		}
	}
	out.Metrics.Known = true
	out.Metrics.InputTokens = incoming
	out.Metrics.OutputTokens = outgoing
	out.Metrics.TotalTokens = total
	return out, nil
}

func filtered(r map[string]any) bool {
	for _, key := range []string{"prompt_filter_results", "content_filters", "choices"} {
		for _, v := range array(r[key]) {
			m := object(v)
			if m["blocked"] == true {
				return true
			}
			for _, v := range object(m["content_filter_results"]) {
				if object(v)["filtered"] == true {
					return true
				}
			}
		}
	}
	return false
}
