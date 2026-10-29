//go:build linux || darwin

package reporting

import (
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"strings"
	"testing"
)

func TestDatabaseReportSeparatesObservedAndHistoricalFacts(t *testing.T) {
	var b strings.Builder
	r := Result{DatabaseEvidence: []DatabaseSummary{{SessionID: "session-1", Evidence: "evidence/abc.tar", Facts: interceptor.DatabaseEvidence{Events: []interceptor.DatabaseEvent{{Service: "database", Kind: "database_state_injection_committed", Provenance: "harness_injected", Sequence: 3, Digest: "sha256:abc", ObservedAttemptID: "attempt-1"}}, Oracles: []interceptor.DatabaseOracle{{ID: "refund", Fired: true, FirstEventSeq: 8, FirstEventDigest: "sha256:def"}}, Truncated: true}}}}
	writeDatabaseSummary(&b, r)
	for _, want := range []string{"receipt-bound", "session history", "not proof of injection causality", "database_state_injection_committed", "harness_injected", "observed attempt attempt-1", "Session-wide oracle refund", "first event 8", "evidence/abc.tar", "summary limit reached"} {
		if !strings.Contains(b.String(), want) {
			t.Fatal("missing report fact", want)
		}
	}
	r.DatabaseEvidence[0].Facts.Oracles = nil
	b.Reset()
	writeDatabaseSummary(&b, r)
	if !strings.Contains(b.String(), "No retained database oracle evaluation") {
		t.Fatal("missing evaluation implied success")
	}
}
