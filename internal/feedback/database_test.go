package feedback

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"testing"
)

func TestDatabaseOracleProjection(t *testing.T) {
	body := []byte(`{"oracle_id":"refund","oracle_kind":"database_query_match","fired":true,"sql":"SECRET","credentials":"SECRET"}`)
	for _, tc := range []struct {
		profile, state, reason string
		positive               bool
		selection              *interceptor.ObservationSelection
	}{
		{"oracle-assisted", "available", "", true, nil},
		{"oracle-assisted", "empty", "no_positive_attributed_result_not_proof_of_failure", false, nil},
		{"oracle-assisted", "unavailable", "oracle_evaluation_unavailable", false, nil},
		{"oracle-assisted", "partial", "oracle_evaluation_unavailable", true, nil},
		{"oracle-assisted", "partial", "state_oracles_require_post_run_evidence", true, nil},
		{"black-box", "available", "", true, nil}, {"diagnostic", "available", "", true, nil},
		{"oracle-assisted", "available", "", true, &interceptor.ObservationSelection{Mode: "selected", Kinds: []string{"target_output"}}},
	} {
		t.Run(tc.profile+tc.state+tc.reason, func(t *testing.T) {
			allowed := []string{}
			for _, k := range kinds {
				if permitted(tc.profile, k) {
					allowed = append(allowed, k)
				}
			}
			p, err := New("oracle-assisted", tc.profile, allowed, tc.selection)
			if err != nil {
				t.Fatal(err)
			}
			s, v := view(p)
			content := map[string][]byte{}
			if tc.positive {
				entry(&v, "db", "oracle_outcome", body, interceptor.HarnessVisible)
				content["db"] = body
			}
			for i := range v.Categories {
				if v.Categories[i].Kind == "oracle_outcome" {
					v.Categories[i].State = tc.state
					v.Categories[i].Reason = tc.reason
				}
			}
			if tc.state == "partial" || tc.state == "unavailable" {
				v.CollectionState = "partial"
			}
			r, err := Project(catalog(t), p, s, "receipt", seal(v), content, nil, 1024)
			if err != nil {
				t.Fatal(err)
			}
			var m Manifest
			_ = json.Unmarshal(r.ManifestJSON(), &m)
			visible := tc.profile == "oracle-assisted" && tc.selection == nil
			for _, c := range m.Categories {
				if c.Kind == "oracle_outcome" {
					if visible && (c.State != tc.state || c.Reason != tc.reason) {
						t.Fatalf("lost category: %+v", c)
					}
					if !visible && c.State != "withheld" && c.State != "not_requested" {
						t.Fatalf("exposed oracle category: %+v", c)
					}
				}
			}
			if (len(r.entries) > 0) != (visible && tc.positive) {
				t.Fatal("profile/selection leak")
			}
			if len(r.entries) > 0 {
				if bytes.Contains(r.entries[0].content, []byte("SECRET")) {
					t.Fatal("private oracle details escaped")
				}
			}
		})
	}
	normalized, err := normalize("oracle_outcome", body, false, nil, "session")
	if err != nil || bytes.Contains(normalized, []byte("SECRET")) || bytes.Contains(normalized, []byte(`"refund"`)) {
		t.Fatalf("unsafe projection %s %v", normalized, err)
	}
	if _, err = normalize("oracle_outcome", []byte(`{"oracle_id":"refund","oracle_kind":"database_query_match","fired":false}`), false, nil, "session"); err == nil {
		t.Fatal("negative verdict accepted")
	}
}
