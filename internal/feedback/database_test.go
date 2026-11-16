package feedback

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"os"
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

func TestNativeDatabaseFeedbackFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/database-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string            `json:"name"`
		View    json.RawMessage   `json:"view"`
		Content map[string][]byte `json:"content"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var v interceptor.ObservationView
			if err := json.Unmarshal(tc.View, &v); err != nil {
				t.Fatal(err)
			}
			source := Source{CampaignID: v.CampaignID, SessionID: v.SessionID, AttemptID: v.AttemptID, AttemptContextDigest: v.AttemptContextDigest, TurnID: v.TurnID, RunRevision: 1, SessionRevision: v.SessionRevision}
			policy, err := New("oracle-assisted", "oracle-assisted", []string{"oracle_outcome"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Project(catalog(t), policy, source, "receipt", tc.View, tc.Content, nil, 65536)
			if err != nil {
				t.Fatal(err)
			}
			var manifest Manifest
			_ = json.Unmarshal(r.ManifestJSON(), &manifest)
			want := "empty"
			reason := "no_positive_attributed_result_not_proof_of_failure"
			positive := false
			switch tc.Name {
			case "positive":
				want = "available"
				reason = ""
				positive = true
			case "missing", "late", "ambiguous":
				want = "unavailable"
				reason = "oracle_evaluation_unavailable"
			case "mixed":
				want = "partial"
				reason = "oracle_evaluation_unavailable"
				positive = true
			case "other-state":
				want = "partial"
				reason = "state_oracles_require_post_run_evidence"
				positive = true
			}
			for _, c := range manifest.Categories {
				if c.Kind == "oracle_outcome" && (c.State != want || c.Reason != reason) {
					t.Fatal("native category lost", c, want, reason)
				}
			}
			if (len(r.entries) > 0) != positive {
				t.Fatal("native boundary changed", tc.Name, len(r.entries))
			}
		})
	}
}
