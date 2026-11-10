//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func TestWorkflowPreservesProducerDetailsAndUnknownReasons(t *testing.T) {
	for _, tc := range []struct{ reason, detail, correction string }{
		{"invalid_range", "Offset plus length exceeds the permitted integer range.", "Reduce length so offset plus length is at most 9007199254740991."},
		{"wrong_handle_kind", "An execution handle cannot be a record.", "Use a historical record handle returned by record_append."},
		{"future_rule", "A newly supported rule failed.", "Read the current resource state."},
	} {
		d := NewWorkflowDiagnostic("request-1", "harness-local", "references", tc.reason, "/range", "finish", "call-1")
		d.RetainDetails(tc.detail, tc.correction, &contracts.FailureFacts{"maximum": int64(9007199254740991)})
		raw, _ := json.Marshal(d)
		saved := DecodeWorkflowDiagnostic(raw)
		if saved == nil || saved.Reason != tc.reason || !strings.Contains(saved.Message(), tc.detail) || !strings.Contains(saved.Message(), tc.correction) {
			t.Fatal(string(raw), saved)
		}
	}
	d := NewWorkflowDiagnostic("request-2", "harness-local", "arguments", "future_rule", "", "", "")
	d.RetainDetails("Bearer SYNTHETIC_SECRET", "https://user:password@example.test", nil)
	if strings.Contains(d.Message(), "SYNTHETIC_SECRET") || strings.Contains(d.Message(), "password") {
		t.Fatal(d.Message())
	}
}

func TestWorkflowOmissionsSurviveRetentionAndProjection(t *testing.T) {
	d := NewWorkflowDiagnostic("request-3", "harness-local", "arguments", "invalid_structure", "/claims/0", "finish", "call-3")
	d.RetainDetails("Message omitted; follow the correction.", "Use the schema's supported fields.", nil)
	d.ProducerOmissions = &[]DiagnosticOmission{{Field: "message", Reason: "oversized"}}
	raw, _ := json.Marshal(d)
	saved := DecodeWorkflowDiagnostic(raw)
	if saved == nil || !strings.Contains(saved.Message(), "Producer omitted message (oversized)") {
		t.Fatal(string(raw))
	}
	projected := WorkflowFromWire("request-3", saved.Wire())
	if projected == nil || projected.ProducerOmissions == nil || len(*projected.ProducerOmissions) != 1 {
		t.Fatalf("omission lost: %s", saved.Wire())
	}
}
