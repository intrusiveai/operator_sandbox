//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"strings"
	"testing"
)

func TestRetainedBudgetDiagnosticValidation(t *testing.T) {
	d := NewWorkflowDiagnostic("report-1", "harness-local", "termination", "model_reservation_unavailable", "", "", "")
	d.Stopping = "budget-limit"
	d.Budget = &contracts.ModelTokenBudget{Kind: "model-tokens", TotalAllowance: 524288, Consumed: 493460, Remaining: 30828, NextRequestReservation: 36864}
	raw, _ := json.Marshal(d)
	if got := DecodeWorkflowDiagnostic(raw); got == nil || got.Budget == nil || !strings.Contains(got.Message(), "required reservation: 36864") {
		t.Fatal(string(raw))
	}
	for _, mutate := range []func(*WorkflowDiagnostic){
		func(d *WorkflowDiagnostic) { d.Budget = nil },
		func(d *WorkflowDiagnostic) { d.Budget.Remaining = 1 },
		func(d *WorkflowDiagnostic) { d.Observed = 50354; d.Limit = 268435456 },
		func(d *WorkflowDiagnostic) { d.Reason = "range_exceeded" },
		func(d *WorkflowDiagnostic) { d.Tool = "finish" },
	} {
		var value WorkflowDiagnostic
		json.Unmarshal(raw, &value)
		mutate(&value)
		broken, _ := json.Marshal(value)
		if DecodeWorkflowDiagnostic(broken) != nil {
			t.Fatal("accepted inconsistent retained diagnostic", string(broken))
		}
	}
	legacy := NewWorkflowDiagnostic("old-report", "harness-local", "arguments", "range_exceeded", "/offset", "observation_read", "")
	legacy.Stopping = "budget-limit"
	legacy.Observed = 50354
	legacy.Limit = 268435456
	old, _ := json.Marshal(legacy)
	if got := DecodeWorkflowDiagnostic(old); got == nil || got.Budget != nil {
		t.Fatal("old records unreadable or inferred token facts")
	}
}

func TestInvalidCallLimitDiagnosticRetention(t *testing.T) {
	d := NewWorkflowDiagnostic("stopping-1", "harness-local", "termination", "invalid_call_limit", "", "", "")
	d.Stopping, d.Observed, d.Limit = "invalid-call-limit", 5, 5
	d.HostFinalized, d.CleanupCompleted = true, true
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := DecodeWorkflowDiagnostic(raw)
	if saved == nil || saved.Reason != "invalid_call_limit" || saved.Stage != "termination" || saved.Tool != "" || saved.FieldPath != "" || !strings.Contains(saved.Message(), "5 invalid tool calls (configured limit 5)") || !strings.Contains(saved.Message(), "No final assessment was committed") {
		t.Fatal(saved)
	}
	// Existing records remain readable; no contract or evidence migration.
	d.Reason, d.Stage = "local_error", "arguments"
	raw, _ = json.Marshal(d)
	if DecodeWorkflowDiagnostic(raw) == nil {
		t.Fatal("legacy stopping record rejected")
	}
}
