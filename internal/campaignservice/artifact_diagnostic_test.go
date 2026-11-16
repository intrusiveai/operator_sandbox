//go:build linux || darwin

package campaignservice

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

func TestArtifactCapacityCorrectionAndRetainedProjection(t *testing.T) {
	for _, tc := range []struct {
		bytes, objects int64
		disposition    string
	}{{10, 1, "correct-and-resubmit"}, {10, 0, "gap-and-continue"}, {0, 1, "gap-and-continue"}} {
		r := artifactCapacityDenied("ARTIFACT_BUDGET_EXCEEDED", 20, tc.bytes, tc.objects)
		if r.Error.Disposition != tc.disposition || r.Error.Effect != "none" {
			t.Fatal(r.Error)
		}
		d := campaign.WorkflowFromWire("artifact-1", r.Error.Diagnostic)
		if d == nil || d.Facts == nil || !strings.Contains(d.Message(), "available_bytes") {
			t.Fatal(d, string(r.Error.Diagnostic))
		}
		raw, _ := json.Marshal(d)
		if campaign.DecodeWorkflowDiagnostic(raw) == nil {
			t.Fatal("retained diagnostic rejected")
		}
	}
}
