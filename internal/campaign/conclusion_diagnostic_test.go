//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func TestConclusionDiagnosticBoundsAndOmissions(t *testing.T) {
	for _, tc := range []struct{ id, omission string }{{"hyp-missing", ""}, {"sk-synthetic-api-key", "credential_like"}, {"secret-value", "credential_like"}, {"https://user:password@host", "invalid_identifier"}, {strings.Repeat("h", 129), "size_limit"}} {
		d := NewConclusionDiagnostic("record-1", &contracts.ConclusionError{Reason: "unresolved_hypothesis", FieldPath: "/hypotheses/0/provenance/parent_hypothesis_id", Identifier: tc.id})
		if !d.Valid() || d.IdentifierOmitted != tc.omission {
			t.Fatal(d)
		}
		raw, _ := json.Marshal(d)
		if got := DecodeConclusionDiagnostic(raw); got == nil || *got != d {
			t.Fatal(string(raw))
		}
		if tc.omission != "" && (strings.Contains(string(raw), tc.id) || strings.Contains(d.Message(), tc.id)) {
			t.Fatal("identifier leaked")
		}
	}
	for reason := range conclusionReasons {
		d := NewConclusionDiagnostic(strings.Repeat("x", 129), &contracts.ConclusionError{Reason: reason, FieldPath: "/claims/0/observation_refs/0/range", Identifier: strings.Repeat("x", 128)})
		if !d.Valid() {
			t.Fatal("largest diagnostic invalid", d)
		}
	}
	d := NewConclusionDiagnostic("record-1", &contracts.ConclusionError{Reason: "invalid_structure", FieldPath: "/sk-synthetic-api-key"})
	if !d.Valid() || d.PathOmitted != "unsafe_or_oversized" || strings.Contains(d.Message(), "synthetic") {
		t.Fatal(d)
	}
	raw, _ := json.Marshal(d)
	raw = append(raw[:len(raw)-1], []byte(`,"body":"secret"}`)...)
	if DecodeConclusionDiagnostic(raw) != nil {
		t.Fatal("accepted unbounded extra field")
	}
}
