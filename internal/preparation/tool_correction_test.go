//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestToolCorrectionRetentionAndNoProgress(t *testing.T) {
	var root string
	s, peer, _, w, launch := serviceWithSettings(t, 0, []string{"engine.diagnostic_report", "engine.finish"}, func(c *campaignservice.Config) { root = c.StateRoot })
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	protocol, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string]any{"finish_reason": "no-useful-next-experiment", "summary": "Limited.", "objectives": []any{}, "claims": []any{map[string]any{"statement": "Limited.", "interpretation": "inconclusive", "assurance": "feedback", "confidence": "low", "uncertainties": []string{strings.Repeat("explanatory prose instead of a label ", 6)}}}}
	_, err = protocol.ValidateToolArguments("finish", encode(bad))
	var issue *contracts.ArgumentError
	if !errors.As(err, &issue) || issue.Constraint == nil {
		t.Fatal(err)
	}
	body := map[string]any{"tool_name": "finish", "tool_call_id": "rejected-finish", "diagnostic": map[string]any{"source": "harness-local", "stage": "arguments", "reason": issue.Reason, "field_path": issue.FieldPath, "constraint": issue.Constraint, "message": "SYNTHETIC_SECRET", "correction": "SYNTHETIC_SECRET", "execution_occurred": "no", "resubmission_safe": true}}
	raw, err := s.Handle(context.Background(), stateWire("engine.diagnostic_report", "rejection", 1, body), 0)
	stateResult(t, raw, err)
	if d := s.WorkflowFailure(); d == nil || !strings.Contains(d.Message(), "128") || !strings.Contains(d.Message(), "top-level uncertainties") || strings.Contains(d.Message(), "SYNTHETIC_SECRET") {
		t.Fatal(d)
	}
	stop := map[string]any{"diagnostic": map[string]any{"source": "harness-local", "stage": "termination", "reason": "no_progress_limit", "message": "No progress.", "execution_occurred": "no", "resubmission_safe": false}, "stopping": map[string]any{"condition": "no-progress-limit", "observed_count": 10, "configured_limit": 10}}
	raw, err = s.Handle(context.Background(), stateWire("engine.diagnostic_report", "stopping", 1, stop), 1)
	stateResult(t, raw, err)
	raw, err = s.Handle(context.Background(), stateWire("engine.finish", "unavailable", 1, map[string]any{"finish_reason": "no-useful-next-experiment", "unavailable": map[string]any{"reason": "no-progress-limit"}}), 2)
	stateResult(t, raw, err)
	terminal, err := s.Shutdown(context.Background())
	if err != nil || terminal.CleanupState != "complete" {
		t.Fatal(terminal, err)
	}
	if terminal.WorkflowDiagnostic == nil || !strings.Contains(terminal.WorkflowDiagnostic.Message(), "10 consecutive turns") || terminal.AssessmentState != "unavailable" {
		t.Fatal(terminal.WorkflowDiagnostic)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	receipt, err := reporting.Generate(context.Background(), root, "campaign-1", "")
	if err != nil {
		t.Fatal(err)
	}
	result := reportResult(t, root, receipt)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SYNTHETIC_SECRET") {
		t.Fatal("prose leak")
	}
	found := false
	for _, d := range result.WorkflowDiagnostics {
		if strings.Contains(d.Message(), "top-level uncertainties") {
			found = true
		}
	}
	if !found {
		t.Fatal("rejection detail lost")
	}
	for _, op := range peer.calls {
		if op == "application.invoke" {
			t.Fatal("diagnostic dispatched target")
		}
	}
}
