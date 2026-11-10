//go:build linux || darwin

package modelprovider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProfileDiagnosticsIdentifyRuleWithoutPrivateValues(t *testing.T) {
	baseline := map[string]any{"api_version": Version, "id": "model", "provider": "openai-chat", "codec_id": "openai-chat-text-tools-v1", "model": "test", "endpoint": "https://example.test/chat/completions", "authentication": "api-key-env", "api_key_env": "MODEL_KEY", "maximum_prompt_tokens": 1000, "maximum_completion_tokens": 100, "maximum_response_bytes": 4096, "codec_options": map[string]any{}}
	for _, tc := range []struct {
		field string
		value any
		path  string
	}{
		{"codec_id", "wrong", "/codec_id"}, {"maximum_prompt_tokens", 0, "/maximum_prompt_tokens"}, {"ca_certificates_pem", "SYNTHETIC_SECRET", "/ca_certificates_pem"}, {"authentication", "azure-cli", "/api_key_env"}, {"endpoint", "https://user:SYNTHETIC_SECRET@example.test/other", "/endpoint"}, {"codec_options", map[string]any{"instruction_role": "SYNTHETIC_SECRET"}, "/codec_options/instruction_role"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			v := map[string]any{}
			for k, x := range baseline {
				v[k] = x
			}
			v[tc.field] = tc.value
			raw, _ := json.Marshal(v)
			_, err := Parse(raw)
			var detail *ProfileError
			if !errors.Is(err, ErrProfile) || !errors.As(err, &detail) || detail.Field != tc.path || detail.Correction == "" || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
				t.Fatal(err, detail)
			}
		})
	}
}
