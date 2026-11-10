package attemptadapter

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func TestPolicyCheckRetainsActualConstraint(t *testing.T) {
	p := &Policy{scopes: Scopes{Routes: []Route{{Surface: "database_response", Target: Target{Service: "db"}, Scopes: []string{"next"}, Placements: []string{"replace"}, Pointers: []string{"/rows/0/0"}}}}}
	d := interceptor.Definition{Surface: "database_response", Scope: "next"}
	d.Selector.Service = "db"
	d.Placement.Operation = "replace"
	d.Placement.Pointer = "/rows/0/0"
	for _, tc := range []struct {
		name, path, reason string
		change             func(*interceptor.Definition)
	}{
		{"turn", "/selector/turn_id", "unknown_turn", func(d *interceptor.Definition) { d.Selector.TurnID = "unknown" }},
		{"scope", "/scope", "unauthorized_scope", func(d *interceptor.Definition) { d.Scope = "all" }},
		{"pointer", "/placement/pointer", "unauthorized_pointer", func(d *interceptor.Definition) { d.Placement.Pointer = "/rows/1/0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := d
			tc.change(&v)
			err := p.authorizeInjection(v, nil)
			var issue *contracts.ArgumentError
			if !errors.As(err, &issue) || !errors.Is(err, ErrPolicy) || issue.FieldPath != tc.path || issue.Reason != tc.reason {
				t.Fatal(err)
			}
		})
	}
}

func TestObservationCorrectionWireFacts(t *testing.T) {
	for _, disposition := range []string{"correct-and-resubmit", "gap-and-continue"} {
		r := rejection("OBSERVATION_RANGE_INVALID", "/offset", "Read within the retained byte range.", disposition, &contracts.FailureFacts{"maximum_offset": int64(42), "requested_offset": int64(43)})
		var d map[string]any
		if json.Unmarshal(r.Error.Diagnostic, &d) != nil || d["execution_occurred"] != "no" || d["resubmission_safe"] != (disposition == "correct-and-resubmit") {
			t.Fatal(r.Error)
		}
	}
}
