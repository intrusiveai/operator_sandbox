//go:build linux || darwin

package livequalification

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

type generatorFunc func(context.Context, []byte) ([]byte, error)

func (f generatorFunc) Generate(c context.Context, b []byte) ([]byte, error) { return f(c, b) }
func providerFixture(t *testing.T, codec, provider, auth string) (*Prepared, []byte, []byte) {
	t.Helper()
	file := map[string]string{"openai-chat-text-tools-v1": "model-codec", "openai-responses-text-tools-v1": "responses-model-codec", "anthropic-messages-text-tools-v1": "anthropic-model-codec", "bedrock-converse-text-tools-v1": "bedrock-model-codec", "gemini-text-tools-v1": "gemini-model-codec"}[codec]
	raw, err := os.ReadFile("../../schemas/fixtures/" + file + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Mode, Disposition string
		Valid             bool
		Policy            map[string]any
		Result            struct{ Response map[string]any }
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	var policy, textResult, toolResult map[string]any
	for _, f := range fixtures {
		if f.Valid && f.Mode == "bound" {
			if f.Disposition == "text" && textResult == nil {
				policy = f.Policy
				textResult = f.Result.Response
			}
			if f.Disposition == "tool-calls" && toolResult == nil {
				toolResult = f.Result.Response
			}
		}
	}
	// Limit the common multiple-call fixture to one inert snapshot_list call.
	switch codec {
	case "openai-chat-text-tools-v1":
		m := toolResult["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		m["tool_calls"] = m["tool_calls"].([]any)[:1]
	case "openai-responses-text-tools-v1":
		toolResult["output"] = toolResult["output"].([]any)[:1]
	case "anthropic-messages-text-tools-v1":
		toolResult["content"] = toolResult["content"].([]any)[:1]
	case "bedrock-converse-text-tools-v1":
		m := toolResult["output"].(map[string]any)["message"].(map[string]any)
		m["content"] = m["content"].([]any)[:1]
	case "gemini-text-tools-v1":
		m := toolResult["candidates"].([]any)[0].(map[string]any)["content"].(map[string]any)
		m["parts"] = m["parts"].([]any)[:1]
	}
	options := map[string]any{}
	for _, key := range []string{"instruction_role", "response_models", "reasoning", "thinking", "thinking_config"} {
		if v, ok := policy[key]; ok {
			options[key] = v
		}
	}
	suffix := map[string]string{"openai-chat-text-tools-v1": "/chat/completions", "openai-responses-text-tools-v1": "/responses", "anthropic-messages-text-tools-v1": "/messages", "gemini-text-tools-v1": "/models/fixture-model:generateContent"}[codec]
	s := modelprovider.Settings{APIVersion: modelprovider.Version, ID: "probe", Provider: provider, Codec: codec, Model: "fixture-model", Endpoint: "https://private.example" + suffix, Authentication: auth, MaximumPromptTokens: 32768, MaximumCompletionTokens: 4096, MaximumResponseBytes: 1 << 20, CodecOptions: jsonBytes(options)}
	if auth == "aws-profile" {
		s.AWSProfile = "test-selected-profile"
	}
	if auth == "secret-store" {
		s.CredentialID = "probe"
	}
	if provider == "anthropic-messages" {
		s.APIVersionHeader = "2023-06-01"
	}
	if provider == "bedrock-converse" {
		s.Endpoint = ""
		s.Region = "us-east-1"
	}
	profile, err := modelprovider.Parse(jsonBytes(s))
	if err != nil {
		t.Fatal(err)
	}
	q := &Prepared{plan: Plan{APIVersion: Version, Kind: "provider", CaseID: "case-1", MaximumModelCalls: 3, MaximumOutputTokens: 4096, TimeoutSeconds: 60}, profile: profile}
	return q, jsonBytes(textResult), jsonBytes(toolResult)
}
func TestEveryProviderRouteConversation(t *testing.T) {
	routes := [][3]string{{"gemini-text-tools-v1", "vertex-gemini", "secret-store"}, {"gemini-text-tools-v1", "vertex-gemini", "google-adc"}, {"bedrock-converse-text-tools-v1", "bedrock-converse", "aws-profile"}, {"openai-chat-text-tools-v1", "openai-chat", "secret-store"}, {"openai-responses-text-tools-v1", "openai-responses", "secret-store"}, {"anthropic-messages-text-tools-v1", "anthropic-messages", "secret-store"}, {"bedrock-converse-text-tools-v1", "bedrock-converse", "workload-identity"}, {"gemini-text-tools-v1", "gemini-api", "secret-store"}, {"gemini-text-tools-v1", "vertex-gemini", "workload-identity"}}
	for _, provider := range []string{"azure-openai", "litellm"} {
		for _, codec := range []string{"openai-chat-text-tools-v1", "openai-responses-text-tools-v1"} {
			routes = append(routes, [3]string{codec, provider, "secret-store"})
			if provider == "azure-openai" {
				for _, auth := range []string{"workload-identity", "azure-cli", "azure-client-secret"} {
					routes = append(routes, [3]string{codec, provider, auth})
				}
			}
		}
	}
	for _, route := range routes {
		t.Run(strings.Join(route[:], "/"), func(t *testing.T) {
			q, text, tool := providerFixture(t, route[0], route[1], route[2])
			c, err := newConversation(q)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			var records []Record
			e := &execution{q: q, sink: func(r Record) error { records = append(records, r); return nil }}
			fake := generatorFunc(func(ctx context.Context, raw []byte) ([]byte, error) {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				calls++
				c.body["request"] = json.RawMessage(raw)
				if _, err := c.protocol.ValidateModelRequest(c.policy, jsonBytes(c.body)); err != nil {
					t.Fatal(err)
				}
				response := text
				if calls == 2 {
					response = tool
				}
				if route[0] == "openai-responses-text-tools-v1" {
					var native, result map[string]any
					_ = json.Unmarshal(raw, &native)
					_ = json.Unmarshal(response, &result)
					for _, key := range []string{"instructions", "max_output_tokens", "tools", "tool_choice"} {
						result[key] = native[key]
					}
					if calls == 3 {
						result["output"].([]any)[0].(map[string]any)["id"] = "msg-final"
					}
					response = jsonBytes(result)
				}
				return response, nil
			})
			if err = e.provider(context.Background(), fake); err != nil {
				t.Fatalf("%v records=%v", err, records)
			}
			if calls != 3 {
				t.Fatal(calls)
			}
			raw := string(jsonBytes(records))
			for _, private := range []string{"private.example", "fixture-model-2026", "Ready.", "snapshot_list"} {
				if strings.Contains(raw, private) {
					t.Fatal("private evidence")
				}
			}
		})
	}
}
func TestProviderUncertaintyStopsAndNeverReplays(t *testing.T) {
	q, text, _ := providerFixture(t, "openai-chat-text-tools-v1", "openai-chat", "secret-store")
	for _, malformed := range []bool{false, true} {
		calls := 0
		var records []Record
		e := &execution{q: q, sink: func(r Record) error { records = append(records, r); return nil }}
		g := generatorFunc(func(ctx context.Context, _ []byte) ([]byte, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			calls++
			if malformed {
				return []byte(`{"private_error":"DO_NOT_RECORD"}`), nil
			}
			return nil, errors.New("DO_NOT_RECORD")
		})
		if e.provider(context.Background(), g) == nil || calls != 1 {
			t.Fatal("failed generation retried")
		}
		if strings.Contains(string(jsonBytes(records)), "DO_NOT_RECORD") {
			t.Fatal("leaked error")
		}
	}
	calls := 0
	e := &execution{q: q, sink: func(r Record) error {
		if r.Check == "tool_call_after_text" {
			return errors.New("disk full")
		}
		return nil
	}}
	g := generatorFunc(func(ctx context.Context, _ []byte) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		calls++
		return text, nil
	})
	if e.provider(context.Background(), g) == nil || calls != 1 {
		t.Fatal("sent after failed evidence persistence")
	}
}
func TestNativeProbeBoundedBeforeDispatch(t *testing.T) {
	q, _, _ := providerFixture(t, "anthropic-messages-text-tools-v1", "anthropic-messages", "secret-store")
	s := q.profile.Settings()
	s.CodecOptions = jsonBytes(map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 1024}, "response_models": []string{"fixture-model-2026"}})
	q.profile, _ = modelprovider.Parse(jsonBytes(s))
	q.plan.MaximumOutputTokens = 512
	if _, err := newConversation(q); err == nil {
		t.Fatal("accepted thinking budget exceeding probe cap")
	}
}
