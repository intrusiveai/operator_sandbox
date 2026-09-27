//go:build linux || darwin

package modelprovider

import (
	"encoding/json"
	"sync"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

// Compile the implementation's schema catalog once. Campaign admission separately
// verifies its installed package pin and validates the completed launch context.
var modelCatalog = sync.OnceValues(func() (*contracts.Catalog, error) { return contracts.LoadCatalog(schemas.Files) })

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
func publicPolicy(s Settings, digest string, toolsRaw []byte) (map[string]any, error) {
	value, err := contracts.Decode(s.CodecOptions, 64<<10)
	options, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, ErrProfile
	}
	fields, ok := optionFields[s.Codec]
	if !ok || len(options) != len(fields) {
		return nil, ErrProfile
	}
	policy := map[string]any{"codec_id": s.Codec, "profile_id": s.ID, "profile_digest": digest, "request_model": s.Model, "prompt": "", outputField(s.Codec): s.MaximumCompletionTokens}
	for _, key := range fields {
		value, ok := options[key]
		if !ok {
			return nil, ErrProfile
		}
		policy[key] = value
	}
	tools, err := contracts.Decode(toolsRaw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrProfile
	}
	policy["tools"] = tools
	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, ErrProfile
	}
	catalog, err := modelCatalog()
	if err != nil {
		return nil, ErrProfile
	}
	validated, err := catalog.Validate(contracts.ModelCodecPolicySchema, raw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrProfile
	}
	policy = validated.(map[string]any)
	if s.Codec == "anthropic-messages-text-tools-v1" {
		thinking := policy["thinking"].(map[string]any)
		if thinking["type"] == "enabled" {
			budget := thinking["budget_tokens"].(json.Number)
			n, e := budget.Float64()
			if e != nil || n >= float64(s.MaximumCompletionTokens) {
				return nil, ErrProfile
			}
		}
	}
	return policy, nil
}

// PublicModel projects only the selected native codec settings and immutable pins.
// nativeTools MUST come from the installed release's trusted tool projection.
// This does not authorize launch or replace full EngineContext/identity validation.
func (p *Profile) PublicModel(nativeTools []byte) (json.RawMessage, error) {
	if p == nil {
		return nil, ErrProfile
	}
	policy, err := publicPolicy(p.settings, p.digest, nativeTools)
	if err != nil {
		return nil, err
	}
	digest, err := contracts.CanonicalDigest(nativeTools, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrProfile
	}
	settings := map[string]any{outputField(p.settings.Codec): p.settings.MaximumCompletionTokens, "tools_digest": digest}
	for _, key := range optionFields[p.settings.Codec] {
		settings[key] = policy[key]
	}
	model := map[string]any{"codec_id": p.settings.Codec, "profile_id": p.settings.ID, "profile_digest": p.digest, "model_id": p.settings.Model,
		"features": []string{"text", "function-tools"}, "codec_settings": settings}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, ErrProfile
	}
	return contracts.Canonicalize(raw, 64<<10)
}
