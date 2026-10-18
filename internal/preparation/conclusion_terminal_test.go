//go:build linux || darwin

package preparation_test

import (
	"context"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

// A rejection-event write may fail while reserved terminal writes still succeed.
// Report the terminal copy without requiring the missing rejection event.
func TestConclusionReportTerminalFallback(t *testing.T) {
	for _, kind := range []string{"launch.terminal", "service.terminal-result"} {
		t.Run(kind, func(t *testing.T) {
			var root string
			s, _, _, w, launch := serviceWithSettings(t, 0, completionRoutes, func(c *campaignservice.Config) { root = c.StateRoot })
			if e := s.Admit(context.Background(), launch); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Shutdown(context.Background()); e != nil {
				t.Fatal(e)
			}
			d := campaign.NewConclusionDiagnostic("conclusion-record", &contracts.ConclusionError{Reason: "unresolved_hypothesis", FieldPath: "/hypotheses/0/provenance/parent_hypothesis_id", Identifier: "hyp-missing"})
			event := campaign.Entry{RunRevision: 1, Kind: kind, Metadata: encode(map[string]any{})}
			if kind == "launch.terminal" {
				event.Metadata = encode(map[string]any{"execution_admission": "closed", "diagnostic": d})
			} else {
				event.Content = []campaign.Content{{Role: "response", MediaType: "application/json", Bytes: encode(map[string]any{"closure": "confirmed", "conclusion_diagnostic": d})}}
			}
			if _, e := w.AppendStored(event, "", false); e != nil {
				t.Fatal(e)
			}
			if e := w.Close(); e != nil {
				t.Fatal(e)
			}
			receipt, e := reporting.Generate(context.Background(), root, "campaign-1", "")
			if e != nil {
				t.Fatal(e)
			}
			result := reportResult(t, root, receipt)
			if len(result.ConclusionRejections) != 1 || result.ConclusionRejections[0] != d {
				t.Fatal(result.ConclusionRejections)
			}
		})
	}
}
