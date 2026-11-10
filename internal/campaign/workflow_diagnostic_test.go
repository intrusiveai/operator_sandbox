//go:build linux || darwin

package campaign

import (
	"strings"
	"testing"
)

func TestUnknownAttributionDoesNotBlameHarnessOrOperator(t *testing.T) {
	d := NewWorkflowDiagnostic("request-1", "invented-source", "invented-stage", "invented-reason", "", "", "")
	if !d.Valid() || d.Source != "unknown" || d.Stage != "invented-stage" || d.Reason != "invented-reason" || !strings.Contains(d.Message(), "source invalid or unknown") || strings.Contains(d.Message(), "Harness reported") {
		t.Fatalf("%+v: %s", d, d.Message())
	}
	for _, source := range []string{"operator", "harness-local"} {
		d := NewWorkflowDiagnostic("request-1", source, "arguments", "invalid_arguments", "/input", "invoke", "call-1")
		want := "Operator rejected"
		if source == "harness-local" {
			want = "Attack Harness reported a local"
		}
		if !strings.Contains(d.Message(), want) || !d.Valid() {
			t.Fatalf("%+v: %s", d, d.Message())
		}
	}
}
