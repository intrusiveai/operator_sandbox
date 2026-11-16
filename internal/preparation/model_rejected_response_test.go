//go:build linux || darwin

package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

// Real TLS provider -> production campaign handling -> native journal -> separate
// CLI inspection/export. Target and termination dependencies are controlled; no
// paid model, cloud account or Docker runtime is involved.
func TestRejectedProviderResponseEvidenceCLI(t *testing.T) {
	binary := processCLI(t)
	for _, mode := range []string{"schema", "stop", "duplicate", "usage", "model-metadata", "future-metadata", "valid"} {
		t.Run(mode, func(t *testing.T) {
			policy, request, result := modelFixture(t)
			response := result["response"].(map[string]any)
			choice := response["choices"].([]any)[0].(map[string]any)
			message := choice["message"].(map[string]any)
			message["content"] = "private provider diagnostic evidence"
			reason, path, stage := "", "", ""
			switch mode {
			case "schema":
				response["usage"].(map[string]any)["prompt_tokens"] = "synthetic-provider-key"
				reason, path, stage = "usage_invalid", "/response/usage/prompt_tokens", "adapter"
			case "stop":
				choice["finish_reason"] = "tool_calls"
				reason, path, stage = "inconsistent_stop_reason", "/response/choices/0/finish_reason", "adapter"
			case "duplicate":
				choice["finish_reason"] = "tool_calls"
				message["tool_calls"] = []any{modelCall("call-duplicate", "attempt_execute", map[string]any{}), modelCall("call-duplicate", "attempt_execute", map[string]any{})}
				reason, path, stage = "duplicate_tool_call_identity", "/response", "adapter"
			case "usage":
				response["usage"].(map[string]any)["total_tokens"] = 999
				reason, path, stage = "usage_invalid", "/response/usage/total_tokens", "adapter"
			case "model-metadata":
				response["model"] = "not-the-requested-model"
			case "future-metadata":
				response["new_provider_metadata"] = map[string]any{"extra": true}
			}
			responseBytes := encode(response)
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "req_0123456789abcdef0123456789abcdef")
				w.Header().Set("Authorization", "Bearer synthetic-header-secret")
				_, _ = w.Write(responseBytes)
			}))
			defer server.Close()
			profile, err := modelprovider.Parse(encode(map[string]any{
				"api_version": modelprovider.Version, "id": "test", "provider": "openai-chat", "codec_id": policy["codec_id"], "model": policy["request_model"], "endpoint": server.URL + "/v1/chat/completions", "authentication": "secret-store", "credential_id": "test-key",
				"maximum_prompt_tokens": 200, "maximum_completion_tokens": policy["max_completion_tokens"], "maximum_response_bytes": 65536,
				"ca_certificates_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})),
				"codec_options":       map[string]any{"instruction_role": policy["instruction_role"], "response_models": policy["response_models"]},
			}))
			if err != nil {
				t.Fatal(err)
			}
			client, err := modelprovider.New(context.Background(), profile, diagnosticCredential{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var root string
			s, native, runtime, w, launch := serviceWithTemplate(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
				root = c.StateRoot
				c.Model = &campaignservice.ModelConfig{Policy: encode(policy), Provider: client, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
			}, func(c map[string]any) {
				digest, e := contracts.CanonicalDigest(encode(policy["tools"]), contracts.OrdinaryLimit)
				if e != nil {
					t.Fatal(e)
				}
				c["model"] = map[string]any{"codec_id": policy["codec_id"], "profile_id": policy["profile_id"], "profile_digest": policy["profile_digest"], "model_id": policy["request_model"], "features": []string{"text", "function-tools"}, "codec_settings": map[string]any{"instruction_role": policy["instruction_role"], "response_models": policy["response_models"], "max_completion_tokens": policy["max_completion_tokens"], "tools_digest": digest}}
			})
			if err = s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
			raw, err := s.Handle(context.Background(), stateWire("engine.model_generate", "model-rejected-1", 1, request), 0)
			if err != nil {
				t.Fatal(err)
			}
			d := s.ModelFailure()
			if reason == "" {
				stateResult(t, raw, nil)
				if d != nil {
					t.Fatal(d)
				}
			} else {
				if d == nil || !d.Valid() || d.Reason != reason || d.FieldPath != path || d.ValidationStage != stage || d.HTTPStatus != 200 || d.RequestID == "" || d.RejectedResponse.State != "retained" {
					t.Fatalf("diagnostic: %+v; response: %s", d, raw)
				}
				if mode == "sensitive-key" && !d.FieldPathOmitted {
					t.Fatal("unsafe path omission lost")
				}
				if strings.Contains(string(raw), "private provider") || strings.Contains(string(raw), "tool_calls") || !strings.Contains(string(raw), "terminate") {
					t.Fatal("rejected response delivered", string(raw))
				}
				select {
				case <-runtime.killed:
				case <-time.After(3 * time.Second):
					t.Fatal("not terminated")
				}
				_, _ = s.Handle(context.Background(), stateWire("engine.model_generate", "model-rejected-1", 1, request), 0)
			}
			if calls.Load() != 1 {
				t.Fatal("provider replay", calls.Load())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			terminal, err := s.Shutdown(ctx)
			if err != nil || terminal.CleanupState != "complete" {
				t.Fatal(terminal, err)
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			native.mu.Lock()
			for _, call := range native.calls {
				if call == "application.invoke" {
					t.Error("rejected tool dispatched")
				}
			}
			native.mu.Unlock()
			var content campaign.ContentDescriptor
			_, err = campaign.Inspect(root, "campaign-1", func(e campaign.Event) error {
				if e.Kind != "model.exchange" {
					return nil
				}
				if len(e.Content) != 1 {
					t.Fatalf("missing response evidence: %+v", e)
				}
				content = e.Content[0]
				if reason != "" {
					if content.Role != "provider-response-rejected-untrusted" {
						t.Fatal(content)
					}
					var m map[string]json.RawMessage
					_ = json.Unmarshal(e.Metadata, &m)
					saved := modelprovider.DecodeDiagnostic(m["diagnostic"])
					if saved == nil || *saved != *d {
						t.Fatal("native facts lost")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			cli := func(args ...string) []byte {
				t.Helper()
				inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer inspectCancel()
				cmd := exec.CommandContext(inspectCtx, binary, args...)
				b, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatal(e, string(b))
				}
				return b
			}
			inspection := cli("inspect", "--state-root", root, "--campaign", "campaign-1")
			output := filepath.Join(t.TempDir(), "export")
			cli("export", "--state-root", root, "--campaign", "campaign-1", "--output", output)
			exported, err := os.ReadFile(filepath.Join(output, "content", strings.TrimPrefix(content.Digest, "sha256:")))
			if err != nil {
				t.Fatal(err)
			}
			if contracts.RawDigest(exported) != content.Digest {
				t.Fatal("content integrity lost")
			}
			if reason != "" {
				markdown, _ := os.ReadFile(filepath.Join(output, "report.md"))
				manifest, _ := os.ReadFile(filepath.Join(output, "result-manifest.json"))
				for _, v := range [][]byte{inspection, markdown, manifest} {
					if !bytes.Contains(v, []byte(reason)) || (path != "" && !bytes.Contains(v, []byte(path))) {
						t.Fatalf("diagnostic missing: %s", v)
					}
				}
				if !bytes.Contains(exported, []byte("private provider diagnostic evidence")) {
					t.Fatal("rejected content discarded")
				}
				if mode == "schema" || mode == "sensitive-key" {
					if !d.RejectedResponse.Redacted {
						t.Fatal("redaction not identified")
					}
				}
				if bytes.Contains(exported, []byte("synthetic-provider-key")) {
					t.Fatal("credential retained")
				}
				t.Logf("operatorctl inspect --campaign campaign-1: %s; %s; field %s; rejected evidence %s", d.Reason, d.ValidationStage, d.FieldPath, d.RejectedResponse.Digest)
			} else if !bytes.Equal(exported, responseBytes) {
				t.Fatal("successful response changed")
			}
		})
	}
}
