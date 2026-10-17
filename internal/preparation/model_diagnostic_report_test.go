package preparation_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func TestModelReportSeparatesUsageAndReservation(t *testing.T) {
	for _, mode := range []string{"success", "rejection", "invalid", "legacy-unknown"} {
		t.Run(mode, func(t *testing.T) {
			policy, request, result := modelFixture(t)
			p := &provider{response: encode(result["response"])}
			s, _, _, w, launch := serviceWithSettings(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
				c.Model = &campaignservice.ModelConfig{Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
			})
			if e := s.Admit(context.Background(), launch); e != nil {
				t.Fatal(e)
			}
			request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
			if mode == "legacy-unknown" {
				// Old journal events with no diagnostic remain readable; no cause is invented.
				_, e := w.AppendStored(campaign.Entry{RunRevision: 1, Kind: "model.intent", Metadata: encode(map[string]any{"operation_id": "model-1", "model_turns": 1, "model_tokens": 300, "reserved_tokens": 300})}, "", false)
				if e != nil {
					t.Fatal(e)
				}
				_, e = w.AppendStored(campaign.Entry{RunRevision: 1, Kind: "model.exchange", Metadata: encode(map[string]any{"operation_id": "model-1", "outcome": "unknown", "reserved_tokens": 300})}, "", false)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				// First successful call confirms 120 tokens; second either succeeds or fails.
				raw, e := s.Handle(context.Background(), stateWire("engine.model_generate", "model-1", 1, request), 0)
				stateResult(t, raw, e)
				if mode == "rejection" {
					p.err = modelprovider.FailureError(modelprovider.Diagnostic{Provider: "openai-chat", Model: "test-model", HTTPStatus: 429, ProviderCode: "rate_limit_exceeded"}.WithFailure("http_rejection", "http_rejected"))
				}
				if mode == "invalid" {
					p.response = []byte(`{"secret":"synthetic-body-secret"}`)
				}
				raw, e = s.Handle(context.Background(), stateWire("engine.model_generate", "model-2", 1, request), 0)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "success" {
					stateResult(t, raw, nil)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, e := s.Shutdown(ctx); e != nil {
				t.Fatal(e)
			}
			dir, e := w.EvidenceDirectory()
			if e != nil {
				t.Fatal(e)
			}
			root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
			w.Close()
			receipt, e := reporting.Generate(context.Background(), root, "campaign-1", "")
			if e != nil {
				t.Fatal(e)
			}
			r := reportResult(t, root, receipt)
			actual, reserved := int64(120), int64(200)+int64(request["request"].(map[string]any)["max_completion_tokens"].(float64))
			if mode == "success" {
				actual, reserved = 240, 0
			}
			if mode == "legacy-unknown" {
				actual, reserved = 0, 300
			}
			if r.Usage["model_tokens"] != actual || r.Usage["model_tokens_unconfirmed_reserved"] != reserved || r.Usage["model_tokens_budget_charged"] != actual+reserved {
				t.Fatal(r.Usage)
			}
			failures := 0
			if mode == "rejection" || mode == "invalid" {
				failures = 1
			}
			if len(r.ModelFailures) != failures {
				t.Fatal(r.ModelFailures)
			}
			if failures == 1 {
				d := r.ModelFailures[0]
				if d.OperationID != "model-2" || !d.Valid() {
					t.Fatal(d)
				}
				raw, _ := json.Marshal(d)
				if modelprovider.DecodeDiagnostic(raw) == nil {
					t.Fatal("report diagnostic unreadable")
				}
			}
		})
	}
}

type diagnosticProviderFunc func(context.Context, []byte) ([]byte, error)

func (f diagnosticProviderFunc) Generate(ctx context.Context, raw []byte) ([]byte, error) {
	return f(ctx, raw)
}

func TestModelFailureSurvivesJournalWriteFailure(t *testing.T) {
	policy, request, _ := modelFixture(t)
	var writer *campaign.Writer
	calls := 0
	d := modelprovider.Diagnostic{Provider: "openai-chat", Model: "test-model", HTTPStatus: 503}.WithFailure("http_rejection", "http_rejected")
	provider := diagnosticProviderFunc(func(context.Context, []byte) ([]byte, error) {
		calls++
		if e := writer.Close(); e != nil {
			t.Fatal(e)
		}
		return nil, modelprovider.FailureError(d)
	})
	s, _, _, w, launch := serviceWithSettings(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
		c.Model = &campaignservice.ModelConfig{Provider: provider, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
	})
	writer = w
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	_, err := s.Handle(context.Background(), stateWire("engine.model_generate", "model-1", 1, request), 0)
	d.OperationID = "model-1"
	if modelprovider.Details(err) == nil || *modelprovider.Details(err) != d || s.ModelFailure() == nil || *s.ModelFailure() != d || calls != 1 {
		t.Fatal("primary diagnostic lost", err, s.ModelFailure())
	}
}
