//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func localHypothesis(id, parent string) map[string]any {
	p := map[string]any{"origin": "exploratory"}
	if parent != "" {
		p["parent_hypothesis_id"] = parent
	}
	return map[string]any{"hypothesis_id": id, "objective_refs": []any{}, "provenance": p, "outcome": "not-tested", "summary": "A bounded hypothesis.", "attempt_receipt_refs": []any{}, "record_refs": []any{}}
}
func TestConclusionReferenceResolutionAndDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, reason, path string }{
		{"local", "", ""}, {"forward", "", ""}, {"historical", "", ""}, {"mixed-cycle", "hypothesis_cycle", "/hypotheses/0/provenance/parent_hypothesis_id"},
		{"missing", "unresolved_hypothesis", "/hypotheses/0/provenance/parent_hypothesis_id"},
		{"duplicate", "duplicate_hypothesis", "/hypotheses/1/hypothesis_id"},
		{"self", "self_parent", "/hypotheses/0/provenance/parent_hypothesis_id"},
		{"cycle", "hypothesis_cycle", "/hypotheses/0/provenance/parent_hypothesis_id"},
		{"structure", "invalid_structure", "/status"},
		{"binding", "invalid_structure", "/binding"},
		{"finish", "invalid_structure", "/finish_reason"},
		{"record", "unresolved_record", "/record_refs/0"},
		{"attempt", "invalid_attempt", "/hypotheses/0/attempt_receipt_refs/0"},
		{"objective", "unknown_objective", "/hypotheses/0/objective_refs/0"},
		{"scenario", "unknown_scenario", "/hypotheses/0/provenance/scenario_id"},
		{"artifact", "artifact_receipt_invalid", "/artifact_receipt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, w, launch := serviceWithOperations(t, 0, completionRoutes)
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			call := func(op, id string, body any) map[string]any {
				t.Helper()
				raw, err := s.Handle(context.Background(), stateWire(op, id, 1, body), 0)
				return stateResult(t, raw, err)
			}
			if tc.name == "historical" || tc.name == "mixed-cycle" {
				history := func(id, parent string) {
					h := localHypothesis(id, parent)
					delete(h, "outcome")
					delete(h, "summary")
					delete(h, "attempt_receipt_refs")
					h["statement"] = "Historical hypothesis"
					h["assumptions"] = []any{}
					h["predicted_observations"] = []any{}
					call("engine.record_append", "history-"+id, map[string]any{"record_kind": "hypothesis", "record": h})
				}
				history("prior", "")
				if tc.name == "mixed-cycle" {
					history("next", "prior")
				}
			}
			var c map[string]any
			c = assessmentDraft(conclusion(t, w, launch, 1))
			h := localHypothesis("local", "")
			c["hypotheses"] = []any{h}
			// Coverage can refer to a local definition anywhere in the document.
			c["coverage"] = []any{map[string]any{"objective_id": "objective-marker", "hypothesis_id": "local", "status": "untested", "reason": "No experiment for this hypothesis.", "attempt_receipt_refs": []any{}}}
			switch tc.name {
			case "forward":
				h["provenance"].(map[string]any)["parent_hypothesis_id"] = "later"
				c["hypotheses"] = []any{h, localHypothesis("later", "")}
			case "historical":
				h["provenance"].(map[string]any)["parent_hypothesis_id"] = "prior"
				c["coverage"].([]any)[0].(map[string]any)["hypothesis_id"] = "prior"
			case "mixed-cycle":
				h["hypothesis_id"] = "prior"
				h["provenance"].(map[string]any)["parent_hypothesis_id"] = "next"
			case "missing":
				h["provenance"].(map[string]any)["parent_hypothesis_id"] = "hyp-missing"
			case "duplicate":
				c["hypotheses"] = []any{h, h}
			case "self":
				h["provenance"].(map[string]any)["parent_hypothesis_id"] = "local"
			case "cycle":
				h["provenance"].(map[string]any)["parent_hypothesis_id"] = "later"
				c["hypotheses"] = []any{h, localHypothesis("later", "local")}
			case "structure":
				c["status"] = "invalid"
			case "binding":
				c["binding"] = map[string]any{"run_revision": 2}
			case "record":
				c["record_refs"] = []any{"missing-record"}
			case "attempt":
				h["attempt_receipt_refs"] = []any{"missing-attempt"}
			case "objective":
				h["objective_refs"] = []any{"missing-objective"}
			case "scenario":
				h["provenance"] = map[string]any{"origin": "scenario", "scenario_id": "missing-scenario"}
			}
			if tc.name == "finish" {
				c["finish_reason"] = "budget-limit"
			}
			data := encode(c)
			u := call("engine.artifact_begin", "begin", map[string]any{"purpose": "conclusion", "artifact": descriptor(data)})["upload_id"]
			call("engine.artifact_put_part", "part", map[string]any{"upload_id": u, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)})
			artifact := call("engine.artifact_commit", "commit", map[string]any{"upload_id": u})["artifact_receipt"]
			finish := "no-useful-next-experiment"
			if tc.name == "finish" {
				finish = "budget-limit"
			}
			if tc.name == "artifact" {
				artifact = "missing-artifact"
			}
			raw, err := s.Handle(context.Background(), stateWire("engine.finish", "conclusion-record", 1, map[string]any{"artifact_receipt": artifact, "finish_reason": finish}), 0)
			if tc.reason == "" {
				ack := stateResult(t, raw, err)
				duplicate, e := s.Handle(context.Background(), stateWire("engine.finish", "conclusion-record", 1, map[string]any{"artifact_receipt": artifact, "finish_reason": finish}), 1)
				if stateResult(t, duplicate, e)["stop_receipt"] != ack["stop_receipt"] {
					t.Fatal("duplicate finish changed receipt")
				}
				if !s.StopAccepted() {
					t.Fatal("not accepted")
				}
			} else {
				d := s.ConclusionFailure()
				if err != nil || d == nil || !d.Valid() || d.Reason != tc.reason || d.FieldPath != tc.path || !strings.Contains(string(raw), "CONCLUSION_INVALID") {
					t.Fatal(string(raw), err, d)
				}
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				f := body["error"].(map[string]any)
				if f["message"] != d.Message() || f["instance_path"] != d.FieldPath {
					t.Fatal(f, d)
				}
				// Another rejection and generic shutdown must not replace the first diagnostic.
				_, err = s.Handle(context.Background(), stateWire("engine.finish", "later-rejection", 1, map[string]any{"finish_reason": "budget-limit", "conclusion": map[string]any{"state": "committed", "artifact_receipt": "missing", "record_receipt": "missing"}}), 0)
				if err != nil || *s.ConclusionFailure() != *d {
					t.Fatal("original cause lost", err)
				}
				s.Stop(context.Canceled)
				terminal, _, _ := s.Wait(context.Background())
				if terminal.ConclusionDiagnostic == nil || *terminal.ConclusionDiagnostic != *d {
					t.Fatal(terminal)
				}
			}
		})
	}
}
