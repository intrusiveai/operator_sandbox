//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestPublicModelMatchesSharedStartupForEveryCodec(t *testing.T) {
	protocol, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ provider, file string }{
		{"openai-chat", "model-codec.json"}, {"openai-responses", "responses-model-codec.json"},
		{"anthropic-messages", "anthropic-model-codec.json"}, {"bedrock-converse", "bedrock-model-codec.json"},
		{"gemini-api", "gemini-model-codec.json"}, {"vertex-gemini", "gemini-model-codec.json"},
		{"azure-openai", "model-codec.json"}, {"azure-openai", "responses-model-codec.json"},
		{"litellm", "model-codec.json"}, {"litellm", "responses-model-codec.json"},
	} {
		t.Run(tc.provider+"/"+tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("../../schemas/fixtures/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			var fixtures []struct {
				Mode            string
				Valid           bool
				Context, Policy map[string]any
				Tools           json.RawMessage
				Prompt          string
			}
			if err = json.Unmarshal(raw, &fixtures); err != nil {
				t.Fatal(err)
			}
			for _, f := range fixtures {
				if f.Mode != "context" || !f.Valid {
					continue
				}
				s := profile(t, tc.provider, "https://private-model.example").Settings()
				s.Codec = f.Policy["codec_id"].(string)
				s.Model = f.Policy["request_model"].(string)
				s.MaximumCompletionTokens = int64(f.Policy[outputField(s.Codec)].(float64))
				options := map[string]any{}
				for _, key := range optionFields[s.Codec] {
					options[key] = f.Policy[key]
				}
				s.CodecOptions, _ = json.Marshal(options)
				if s.Codec == "gemini-text-tools-v1" {
					s.Endpoint = "https://private-model.example/v1/models/" + s.Model + ":generateContent"
				} else if s.Codec == "openai-responses-text-tools-v1" {
					s.Endpoint = "https://private-model.example/v1/responses"
				}
				raw, _ = json.Marshal(s)
				p, err := Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				model, err := p.PublicModel()
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"private-model.example", "model-key", "authentication", "endpoint", "credential_id", "region"} {
					if bytes.Contains(model, []byte(private)) {
						t.Fatal("private profile field leaked", private)
					}
				}
				var public map[string]any
				if err = json.Unmarshal(model, &public); err != nil {
					t.Fatal(err)
				}
				f.Context["model"] = public
				contextRaw, _ := json.Marshal(f.Context)
				_, err = protocol.ValidateEngineContext(contextRaw)
				if err != nil {
					t.Fatal(err)
				}
				policy, err := p.Policy(f.Tools, []byte(f.Prompt))
				if err != nil {
					t.Fatal(err)
				}
				delete(f.Policy, "response_models")
				f.Policy["profile_id"] = s.ID
				f.Policy["profile_digest"] = p.Digest()
				f.Policy["context_limits"] = public["context_limits"]
				expected, _ := json.Marshal(f.Policy)
				expected, err = contracts.Canonicalize(expected, contracts.OrdinaryLimit)
				policy, _ = contracts.Canonicalize(policy, contracts.OrdinaryLimit)
				if err != nil || !bytes.Equal(policy, expected) {
					t.Fatal("startup policy differs", err)
				}
				changed := p.Settings()
				changed.CodecOptions[0] = '['
				if p.Settings().CodecOptions[0] != '{' {
					t.Fatal("settings expose mutable profile storage")
				}
				raw = p.JSON()
				raw[0] = '['
				if p.JSON()[0] != '{' {
					t.Fatal("JSON exposes mutable profile storage")
				}
				return
			}
			t.Fatal("missing shared startup fixture")
		})
	}
}
func TestProfileRejectsUnboundCodecOptions(t *testing.T) {
	for _, tc := range []struct{ provider, options string }{
		{"openai-chat", `{"instruction_role":"user","response_models":["test-model"]}`},
		{"openai-chat", `{"instruction_role":"developer","response_models":["x"],"endpoint":"https://private"}`},
		{"bedrock-converse", `{"tools_digest":"sha256:bad"}`}, {"bedrock-converse", `null`},
	} {
		t.Run(tc.provider+tc.options, func(t *testing.T) {
			s := profile(t, tc.provider, "https://private-model.example").Settings()
			s.CodecOptions = []byte(tc.options)
			raw, _ := json.Marshal(s)
			if _, err := Parse(raw); err == nil || strings.Contains(err.Error(), "https://private") {
				t.Fatal("invalid options accepted or exposed", err)
			}
		})
	}
}

func TestContextWarningConfiguration(t *testing.T) {
	p := profile(t, "openai-chat", "https://example.test/chat/completions")
	var fields map[string]any
	_ = json.Unmarshal(p.JSON(), &fields)
	delete(fields, "context_warning_percent")
	raw, _ := json.Marshal(fields)
	omitted, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	fields["context_warning_percent"] = 80
	raw, _ = json.Marshal(fields)
	explicit, err := Parse(raw)
	if err != nil || omitted.Digest() != explicit.Digest() {
		t.Fatal("default must be fingerprinted", err)
	}
	for _, value := range []any{0, 100, -1, 80.5, nil, "80"} {
		fields["context_warning_percent"] = value
		raw, _ = json.Marshal(fields)
		if _, err = Parse(raw); err == nil {
			t.Fatal("invalid warning", value)
		}
	}

}
