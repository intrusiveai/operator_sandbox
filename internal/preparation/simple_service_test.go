//go:build linux || darwin

package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
)

func TestSimpleInputRejectionAndRetentionBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"receipt", "retention-budget", "parent"} {
		t.Run(mode, func(t *testing.T) {
			s, p, _, w, launch := serviceWithTemplate(t, 0, []string{"engine.attempt_execute"}, nil, func(v map[string]any) {
				if mode == "retention-budget" {
					v["remaining_limits"].(map[string]any)["artifact_objects"] = 1
				}
			})
			if e := s.Admit(context.Background(), launch); e != nil {
				t.Fatal(e)
			}
			body := map[string]any{"operation_id": "invoke", "input": map[string]any{"encoding": "json", "value": map[string]any{"query": "new input"}}}
			reason := "ARTIFACT_BUDGET_EXCEEDED"
			if mode == "receipt" {
				body["input"] = map[string]any{"artifact_receipt": "invented-receipt"}
				reason = "invalid_artifact_receipt"
			}
			if mode == "parent" {
				body["parent_attempt_receipt_id"] = "rejected-submission"
				reason = "invalid_parent_receipt"
			}
			q := stateWire("engine.attempt_execute", "same-request", 1, body)
			var first map[string]any
			for i := 0; i < 2; i++ {
				raw, e := s.Handle(context.Background(), q, int64(i))
				if e != nil {
					t.Fatal(e)
				}
				var reply map[string]any
				_ = json.Unmarshal(raw, &reply)
				if reply["result"] != nil || !strings.Contains(string(raw), reason) {
					t.Fatal(string(raw))
				}
				if i == 0 {
					first = reply["error"].(map[string]any)
				} else if !bytes.Equal(encode(first), encode(reply["error"])) {
					t.Fatal("duplicate rejection changed")
				}
			}
			body["input"] = map[string]any{"encoding": "json", "value": map[string]any{"query": "changed"}}
			raw, e := s.Handle(context.Background(), stateWire("engine.attempt_execute", "same-request", 1, body), 3)
			if e != nil || !strings.Contains(string(raw), "IDEMPOTENCY_CONFLICT") {
				t.Fatal(string(raw), e)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			for _, op := range p.calls {
				if op == "application.invoke" {
					t.Fatal("rejected input reached target")
				}
			}
			if w.Fence().Err() != nil {
				t.Fatal("correctable rejection terminated campaign", w.Fence().Err())
			}
		})
	}
}

func TestRuntimeDiagnosticDropsSubmittedProse(t *testing.T) {
	var root string
	s, _, _, w, launch := serviceWithSettings(t, 0, []string{"engine.diagnostic_report"}, func(c *campaignservice.Config) { root = c.StateRoot })
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	secret := "synthetic-credential-never-retain-XYZ"
	body := map[string]any{"tool_name": "attempt_execute", "diagnostic": map[string]any{"source": "harness-local", "stage": "arguments", "reason": "invalid_structure", "message": secret, "identifier": secret, "correction": secret, "field_path": "/input", "execution_occurred": "no", "resubmission_safe": true}, "stopping": map[string]any{"condition": "invalid-call-limit", "observed_count": 5, "configured_limit": 5}}
	raw, e := s.Handle(context.Background(), stateWire("engine.diagnostic_report", "runtime-diagnostic", 1, body), 0)
	stateResult(t, raw, e)
	if d := s.WorkflowFailure(); d == nil || d.Reason != "invalid_structure" || !d.Omitted {
		t.Fatal(d)
	}
	_, e = s.Shutdown(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	_, e = campaign.Inspect(root, "campaign-1", func(event campaign.Event) error {
		if bytes.Contains(event.Metadata, []byte(secret)) {
			t.Fatal("prose retained in metadata")
		}
		for _, c := range event.Content {
			data, e := os.ReadFile(filepath.Join(root, "campaigns/campaign-1", filepath.FromSlash(c.Path)))
			if e != nil {
				return e
			}
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("prose retained in content", event.Kind)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestFinishAcknowledgementDoesNotClaimUncertainCleanup(t *testing.T) {
	s, p, _, w, launch := serviceWithOperations(t, 0, []string{"engine.finish", "engine.attempt_execute"})
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	p.unknownClose = true
	p.mu.Unlock()
	request := stateWire("engine.finish", "finish-once", 1, map[string]any{"finish_reason": "budget-limit", "assessment": map[string]any{"summary": "No attempt was run.", "objectives": []any{map[string]any{"objective_id": "objective-marker", "outcome": "not-tested", "summary": "Budget exhausted before execution."}}}})
	var receipt any
	for i := 0; i < 2; i++ {
		raw, e := s.Handle(context.Background(), request, int64(i))
		ack := stateResult(t, raw, e)
		if ack["finalization_status"] != "pending" {
			t.Fatal(ack)
		}
		if i == 0 {
			receipt = ack["stop_receipt"]
		} else if ack["stop_receipt"] != receipt {
			t.Fatal("duplicate finish")
		}
	}
	raw, e := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 2)
	if e != nil || !strings.Contains(string(raw), "STATE_CHANGED") {
		t.Fatal(string(raw), e)
	}
	terminal, e := s.Shutdown(context.Background())
	if e != nil || terminal.Closure != "unconfirmed" || terminal.CleanupState == "complete" {
		t.Fatal(terminal, e)
	}
	if _, e = s.Handle(context.Background(), request, 3); e == nil {
		t.Fatal("closed campaign resumed")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, op := range p.calls {
		if op == "application.invoke" {
			t.Fatal("execution reopened after finish")
		}
	}
}

func TestAssessmentRetentionBudgetAllowsHonestUnavailableFinish(t *testing.T) {
	s, _, _, _, launch := serviceWithTemplate(t, 0, []string{"engine.finish"}, nil, func(v map[string]any) { v["remaining_limits"].(map[string]any)["artifact_objects"] = 1 })
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	raw, e := s.Handle(context.Background(), stateWire("engine.finish", "assessment", 1, map[string]any{"finish_reason": "budget-limit", "assessment": map[string]any{"summary": "Not tested.", "objectives": []any{}}}), 0)
	if e != nil || !strings.Contains(string(raw), "assessment_budget_exceeded") || s.StopAccepted() {
		t.Fatal(string(raw), e)
	}
	raw, e = s.Handle(context.Background(), stateWire("engine.finish", "unavailable", 1, map[string]any{"finish_reason": "budget-limit", "unavailable": map[string]any{"reason": "budget-limit"}}), 1)
	ack := stateResult(t, raw, e)
	if ack["assessment_state"] != "unavailable" || !s.StopAccepted() {
		t.Fatal(ack)
	}
}
