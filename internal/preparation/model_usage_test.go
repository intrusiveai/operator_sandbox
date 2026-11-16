package preparation_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

type usageProvider struct{ provider }

func (p *usageProvider) GenerateWithMetadata(ctx context.Context, raw []byte) ([]byte, modelprovider.Diagnostic, error) {
	response, err := p.Generate(ctx, raw)
	return response, modelprovider.Diagnostic{Provider: "bedrock-converse", HTTPStatus: 200}, err
}

func TestModelUsageAllowanceEnforcementAndRetention(t *testing.T) {
	for _, mode := range []string{"input-only", "both", "exact-limits", "total-only", "malformed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			policy, request, result := namedModelFixture(t, "bedrock-model-codec.json", "bedrock: native text with separate caches")
			completion := int64(request["request"].(map[string]any)["inferenceConfig"].(map[string]any)["maxTokens"].(float64))
			input, output := int64(201), int64(1)
			switch mode {
			case "both":
				output = completion
			case "exact-limits":
				input, output = 200, completion
			case "total-only":
				input, output = 200, completion+1
			}
			response := result["response"].(map[string]any)
			response["usage"] = map[string]any{"inputTokens": input, "outputTokens": output, "totalTokens": input + output}
			if mode == "unknown" {
				delete(response, "usage")
			}
			if mode == "malformed" {
				response["usage"].(map[string]any)["inputTokens"] = "untrusted-count"
			}
			p := &usageProvider{provider{response: encode(response)}}
			s, _, runtime, w, launch := serviceWithTemplate(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
				c.Model = &campaignservice.ModelConfig{Policy: encode(policy), Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
			}, func(c map[string]any) {
				digest, err := contracts.CanonicalDigest(encode(policy["tools"]), contracts.OrdinaryLimit)
				if err != nil {
					t.Fatal(err)
				}
				c["model"] = map[string]any{"codec_id": policy["codec_id"], "profile_id": policy["profile_id"], "profile_digest": policy["profile_digest"], "model_id": policy["request_model"], "features": []string{"text", "function-tools"}, "codec_settings": map[string]any{"max_tokens": policy["max_tokens"], "tools_digest": digest}}
			})
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			request["request"].(map[string]any)["system"] = []any{map[string]any{"text": string(launch.Prompt)}}
			raw, err := s.Handle(context.Background(), stateWire("engine.model_generate", "usage-1", 1, request), 0)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "exact-limits" {
				stateResult(t, raw, nil)
				if s.ModelFailure() != nil {
					t.Fatal(s.ModelFailure())
				}
			} else {
				if !strings.Contains(string(raw), "terminate") {
					t.Fatal(string(raw))
				}
				select {
				case <-runtime.killed:
				case <-time.After(3 * time.Second):
					t.Fatal("not terminated")
				}
				d := s.ModelFailure()
				if d == nil || !d.Valid() || d.HTTPStatus != 200 {
					t.Fatal(d)
				}
				if mode == "input-only" || mode == "both" || mode == "total-only" {
					u := d.UsageAllowance
					if d.Reason != "usage_exceeds_reservation" || u.InputTokens != input || u.OutputTokens != output || u.TotalTokens != input+output || u.MaximumPromptTokens != 200 || u.RequestedCompletionTokens != completion || u.ReservedTokens != 200+completion || u.PromptAllowanceExceeded != (mode != "total-only") || u.TotalReservationExceeded != (mode != "input-only") {
						t.Fatal(d)
					}
				} else if d.UsageAllowance != (modelprovider.UsageAllowance{}) {
					t.Fatal("unvalidated usage retained", d)
				}
				_, _ = s.Handle(context.Background(), stateWire("engine.model_generate", "usage-1", 1, request), 0)
			}
			if p.calls != 1 {
				t.Fatal("replayed provider request", p.calls)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err = s.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			// Read native evidence through its public inspection path.
			dir, err := w.EvidenceDirectory()
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			exchanges := 0
			_, err = campaign.Inspect(root, "campaign-1", func(event campaign.Event) error {
				if event.Kind != "model.exchange" {
					return nil
				}
				exchanges++
				if mode == "exact-limits" {
					if len(event.Content) != 1 {
						t.Fatal("successful response lost")
					}
				} else {
					if len(event.Content) != 0 {
						t.Fatal("rejected provider body retained")
					}
					var meta map[string]json.RawMessage
					if err := json.Unmarshal(event.Metadata, &meta); err != nil {
						return err
					}
					retained := modelprovider.DecodeDiagnostic(meta["diagnostic"])
					if retained == nil || *retained != *s.ModelFailure() {
						t.Fatal("native diagnostic lost", string(event.Metadata))
					}
				}
				return nil
			})
			if err != nil || exchanges != 1 {
				t.Fatal(err, exchanges)
			}
		})
	}
}
