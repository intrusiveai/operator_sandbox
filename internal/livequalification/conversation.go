//go:build linux || darwin

package livequalification

import (
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

const probePrompt = "This is a harmless API compatibility test. Follow each user instruction. The snapshot_list tool is simulated; no target exists. After its result, reply briefly in text without calling tools."
const probeText = "Reply briefly in text that you are ready. Do not call tools yet."
const probeTool = "Now call snapshot_list exactly once with empty arguments."
const probeResult = `{"snapshots":[],"qualification_fixture":true}`

type conversation struct {
	protocol *contracts.Protocol
	policy   []byte
	codec    string
	body     map[string]any
	request  map[string]any
}

func jsonBytes(v any) []byte { raw, _ := json.Marshal(v); return raw }
func newConversation(q *Prepared) (*conversation, error) {
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		return nil, ErrPlan
	}
	s := q.profile.Settings()
	toolsRaw, err := p.ModelTools(s.Codec, []string{"engine.snapshot_list"})
	if err != nil {
		return nil, ErrPlan
	}
	tools, err := contracts.Decode(toolsRaw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrPlan
	}
	options, err := contracts.Decode(s.CodecOptions, 64<<10)
	if err != nil {
		return nil, ErrPlan
	}
	policy := options.(map[string]any)
	policy["codec_id"] = s.Codec
	policy["profile_id"] = s.ID
	policy["profile_digest"] = q.profile.Digest()
	policy["request_model"] = s.Model
	policy["prompt"] = probePrompt
	policy["tools"] = tools
	n := q.plan.MaximumOutputTokens
	c := &conversation{protocol: p, codec: s.Codec, body: map[string]any{"codec_id": s.Codec, "profile_id": s.ID, "profile_digest": q.profile.Digest()}}
	// Native framing mirrors Attack Harness NativeConversation. All requests and
	// returned continuations are independently checked by the shared Go contract.
	switch s.Codec {
	case "openai-chat-text-tools-v1":
		policy["max_completion_tokens"] = n
		c.request = map[string]any{"model": s.Model, "messages": []any{map[string]any{"role": policy["instruction_role"], "content": probePrompt}, c.user(probeText)}, "max_completion_tokens": n, "stream": false, "store": false, "n": 1, "parallel_tool_calls": true, "tools": tools, "tool_choice": "auto"}
	case "openai-responses-text-tools-v1":
		policy["max_output_tokens"] = n
		c.request = map[string]any{"model": s.Model, "instructions": probePrompt, "input": []any{c.user(probeText)}, "max_output_tokens": n, "stream": false, "store": false, "parallel_tool_calls": true, "include": []string{"reasoning.encrypted_content"}, "truncation": "disabled", "reasoning": policy["reasoning"], "tools": tools, "tool_choice": "auto", "text": map[string]any{"format": map[string]any{"type": "text"}}}
	case "anthropic-messages-text-tools-v1":
		policy["max_tokens"] = n
		c.request = map[string]any{"model": s.Model, "system": probePrompt, "messages": []any{c.user(probeText)}, "max_tokens": n, "stream": false, "thinking": policy["thinking"], "tools": tools, "tool_choice": map[string]any{"type": "auto"}}
	case "bedrock-converse-text-tools-v1":
		policy["max_tokens"] = n
		c.request = map[string]any{"system": []any{map[string]any{"text": probePrompt}}, "messages": []any{c.user(probeText)}, "inferenceConfig": map[string]any{"maxTokens": n}, "toolConfig": map[string]any{"tools": tools, "toolChoice": map[string]any{"auto": map[string]any{}}}}
	case "gemini-text-tools-v1":
		policy["max_output_tokens"] = n
		c.request = map[string]any{"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": probePrompt}}}, "contents": []any{c.user(probeText)}, "generationConfig": map[string]any{"maxOutputTokens": n, "candidateCount": 1, "thinkingConfig": policy["thinking_config"]}, "tools": tools, "toolConfig": map[string]any{"functionCallingConfig": map[string]any{"mode": "AUTO"}}}
	default:
		return nil, ErrPlan
	}
	c.policy = jsonBytes(policy)
	c.body["request"] = c.request
	if _, err = p.ValidateModelRequest(c.policy, jsonBytes(c.body)); err != nil {
		return nil, ErrPlan
	}
	return c, nil
}
func (c *conversation) user(text string) map[string]any {
	switch c.codec {
	case "bedrock-converse-text-tools-v1":
		return map[string]any{"role": "user", "content": []any{map[string]any{"text": text}}}
	case "gemini-text-tools-v1":
		return map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}}
	default:
		return map[string]any{"role": "user", "content": text}
	}
}
func (c *conversation) extend(raw []byte, askTool bool) error {
	v, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	if err != nil {
		return err
	}
	field := "messages"
	if c.codec == "openai-responses-text-tools-v1" {
		field = "input"
	}
	if c.codec == "gemini-text-tools-v1" {
		field = "contents"
	}
	history := append(c.request[field].([]any), v.([]any)...)
	if askTool {
		history = append(history, c.user(probeTool))
	}
	c.request[field] = history
	_, err = c.protocol.ValidateModelRequest(c.policy, jsonBytes(c.body))
	return err
}

// continuation accepts only the single advertised inert tool and uses the
// production contract's native-history constructors, preserving opaque reasoning.
func (c *conversation) continuation(result []byte) ([]byte, error) {
	v, err := contracts.Decode(result, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	response := v.(map[string]any)["response"].(map[string]any)
	calls := []contracts.ChatToolResult{}
	gemini := []contracts.GeminiToolResult{}
	add := func(id, name any, args any) error {
		if name != "snapshot_list" {
			return ErrCheck
		}
		raw := jsonBytes(args)
		if s, ok := args.(string); ok {
			raw = []byte(s)
		}
		if _, err := c.protocol.ValidateToolArguments("snapshot_list", raw); err != nil {
			return ErrCheck
		}
		callID, _ := id.(string)
		calls = append(calls, contracts.ChatToolResult{ToolCallID: callID, Content: probeResult})
		return nil
	}
	switch c.codec {
	case "openai-chat-text-tools-v1":
		message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		items, _ := message["tool_calls"].([]any)
		for _, v := range items {
			call := v.(map[string]any)
			f := call["function"].(map[string]any)
			if err = add(call["id"], f["name"], f["arguments"]); err != nil {
				return nil, err
			}
		}
		return c.protocol.ChatContinuation(result, calls)
	case "openai-responses-text-tools-v1":
		for _, v := range response["output"].([]any) {
			call := v.(map[string]any)
			if call["type"] == "function_call" {
				if err = add(call["call_id"], call["name"], call["arguments"]); err != nil {
					return nil, err
				}
			}
		}
		return c.protocol.ResponsesContinuation(result, calls)
	case "anthropic-messages-text-tools-v1":
		for _, v := range response["content"].([]any) {
			call := v.(map[string]any)
			if call["type"] == "tool_use" {
				if err = add(call["id"], call["name"], call["input"]); err != nil {
					return nil, err
				}
			}
		}
		return c.protocol.AnthropicContinuation(result, calls)
	case "bedrock-converse-text-tools-v1":
		for _, v := range response["output"].(map[string]any)["message"].(map[string]any)["content"].([]any) {
			if call, ok := v.(map[string]any)["toolUse"].(map[string]any); ok {
				if err = add(call["toolUseId"], call["name"], call["input"]); err != nil {
					return nil, err
				}
			}
		}
		return c.protocol.BedrockContinuation(result, calls)
	case "gemini-text-tools-v1":
		for i, v := range response["candidates"].([]any)[0].(map[string]any)["content"].(map[string]any)["parts"].([]any) {
			if call, ok := v.(map[string]any)["functionCall"].(map[string]any); ok {
				if err = add(call["id"], call["name"], call["args"]); err != nil {
					return nil, err
				}
				gemini = append(gemini, contracts.GeminiToolResult{PartIndex: i, Content: probeResult})
			}
		}
		return c.protocol.GeminiContinuation(result, gemini)
	}
	return nil, ErrCheck
}
