//go:build linux || darwin

package modelprovider

import (
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

var optionFields = map[string][]string{
	"openai-chat-text-tools-v1":        {"instruction_role", "response_models"},
	"openai-responses-text-tools-v1":   {"reasoning", "response_models"},
	"anthropic-messages-text-tools-v1": {"thinking", "response_models"},
	"bedrock-converse-text-tools-v1":   {},
	"gemini-text-tools-v1":             {"thinking_config", "response_models"},
}

func outputField(codec string) string {
	switch codec {
	case "openai-chat-text-tools-v1":
		return "max_completion_tokens"
	case "anthropic-messages-text-tools-v1", "bedrock-converse-text-tools-v1":
		return "max_tokens"
	default:
		return "max_output_tokens"
	}
}
func privatePolicy(s Settings, digest string, toolsRaw []byte) (map[string]any, error) {
	value, err := contracts.Decode(s.CodecOptions, 64<<10)
	options, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, ErrProfile
	}
	fields, ok := optionFields[s.Codec]
	if !ok {
		return nil, ErrProfile
	}
	policy := map[string]any{"codec_id": s.Codec, "profile_id": s.ID, "profile_digest": digest, "request_model": s.Model, "prompt": "", outputField(s.Codec): s.MaximumCompletionTokens}
	allowed := map[string]bool{}
	for _, key := range fields {
		allowed[key] = true
	}
	for key, value := range options {
		if !allowed[key] {
			return nil, ErrProfile
		}
		if key != "response_models" {
			policy[key] = value
		}
	}
	if s.Codec == "openai-chat-text-tools-v1" && policy["instruction_role"] == nil {
		policy["instruction_role"] = "developer"
	}
	tools, err := contracts.Decode(toolsRaw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrProfile
	}
	policy["tools"] = tools
	if s.Codec == "openai-chat-text-tools-v1" && policy["instruction_role"] != "system" && policy["instruction_role"] != "developer" {
		return nil, ErrProfile
	}
	return policy, nil
}

// Policy contains trusted provider configuration. It never enters Harness inputs.
func (p *Profile) Policy(tools, prompt []byte) ([]byte, error) {
	if p == nil {
		return nil, ErrProfile
	}
	policy, err := privatePolicy(p.settings, p.digest, tools)
	if err != nil {
		return nil, err
	}
	policy["prompt"] = string(prompt)
	policy["context_limits"] = contracts.ContextLimits{MaximumPromptTokens: p.settings.MaximumPromptTokens, MaximumCompletionTokens: p.settings.MaximumCompletionTokens, WarningThresholdTokens: contracts.ContextWarningThreshold(p.settings.MaximumPromptTokens, p.settings.ContextWarningPercent)}
	return json.Marshal(policy)
}

// PublicModel exposes only conversation format, informational model and allowances.
func (p *Profile) PublicModel() (json.RawMessage, error) {
	if p == nil {
		return nil, ErrProfile
	}
	return json.Marshal(map[string]any{"codec_id": p.settings.Codec, "model_id": p.settings.Model,
		"features": []string{"text", "function-tools"}, "tool_selection": map[string]any{"injection_surfaces": []string{}},
		"context_limits": contracts.ContextLimits{MaximumPromptTokens: p.settings.MaximumPromptTokens, MaximumCompletionTokens: p.settings.MaximumCompletionTokens, WarningThresholdTokens: contracts.ContextWarningThreshold(p.settings.MaximumPromptTokens, p.settings.ContextWarningPercent)}})
}
