package preparation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
)

var completionRoutes = []string{"engine.artifact_begin", "engine.artifact_put_part", "engine.artifact_commit", "engine.record_append", "engine.request_stop"}

func conclusion(t *testing.T, w *campaign.Writer, launch campaign.LaunchInputs, revision int) []byte {
	t.Helper()
	var c map[string]any
	_ = json.Unmarshal(read(t, "../../schemas/fixtures/conclusion-example.json"), &c)
	m := w.Manifest()
	c["binding"] = map[string]any{"campaign_id": m.CampaignID, "launch_id": m.LaunchID, "run_revision": revision, "input_tree_digest": contracts.RawDigest(launch.InputTree), "engine_context_digest": contracts.RawDigest(launch.EngineContext), "prompt_digest": contracts.RawDigest(launch.Prompt), "skill_set_digest": contracts.RawDigest(launch.SkillSet), "image_digest": m.ImageDigest, "release_record_digest": m.ReleaseRecordDigest, "contract_package_digest": m.Contract.Digest, "contract_package_version": m.Contract.Version}
	for _, k := range []string{"objectives", "hypotheses", "claims", "coverage", "uncertainties", "record_refs"} {
		c[k] = []any{}
	}
	c["status"] = "partial"
	c["finish_reason"] = "no-useful-next-experiment"
	raw, err := contracts.Canonicalize(encode(c), contracts.ConclusionLimit)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestServiceConclusionStopThroughSpoolAfterRestore(t *testing.T) {
	routes := append(append([]string{}, snapshotRoutes...), completionRoutes...)
	s, p, r, w, launch := serviceWithOperations(t, 0, routes, snapshotInput(t))
	exchange := serviceSpool(t, s, p, w, launch)
	call := func(op, id string, rev int, body any) map[string]any {
		t.Helper()
		return stateResult(t, exchange(stateWire(op, id, rev, body)), nil)
	}
	cp := call("engine.snapshot_request", "snapshot", 1, map[string]any{})["snapshot"].(map[string]any)
	call("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]})
	data := conclusion(t, w, launch, 2)
	u := call("engine.artifact_begin", "begin", 2, map[string]any{"purpose": "conclusion", "artifact": descriptor(data)})["upload_id"]
	closed := exchange(stateWire("engine.snapshot_request", "after-conclusion-begin", 2, map[string]any{}))
	if !strings.Contains(string(closed), "STATE_CHANGED") {
		t.Fatal("conclusion traffic did not close exploration", string(closed))
	}
	call("engine.artifact_put_part", "part", 2, map[string]any{"upload_id": u, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)})
	artifact := call("engine.artifact_commit", "commit", 2, map[string]any{"upload_id": u})["artifact_receipt"]
	record := call("engine.record_append", "record", 2, map[string]any{"record_kind": "conclusion", "record": map[string]any{"artifact_receipt": artifact, "finish_reason": "no-useful-next-experiment"}})["receipt_id"]
	body := map[string]any{"finish_reason": "no-useful-next-experiment", "conclusion": map[string]any{"state": "committed", "artifact_receipt": artifact, "record_receipt": record}}
	started := time.Now()
	ack := call("engine.request_stop", "stop", 2, body)
	if ack["execution_admission"] != "closed" || ack["exit_within_ms"] != float64(5000) || ack["finalization_status"] != "pending" {
		t.Fatal(ack)
	}
	if dup := call("engine.request_stop", "stop", 2, body); dup["stop_receipt"] != ack["stop_receipt"] {
		t.Fatal("stop replay changed receipt")
	}
	rejected := exchange(stateWire("engine.snapshot_request", "after-stop", 2, map[string]any{}))
	if !strings.Contains(string(rejected), "STATE_CHANGED") {
		t.Fatal(string(rejected))
	}
	select {
	case <-r.killed:
		if time.Since(started) < 4*time.Second {
			t.Fatal("ACK did not allow guest exit")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("graceful stop did not independently terminate Docker")
	}
}
func TestServiceCompletionRejectsUnboundConclusionAndAllowsUnavailableStop(t *testing.T) {
	s, _, r, w, launch := serviceWithOperations(t, 0, completionRoutes)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	call := func(op, id string, body any) map[string]any {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, 1, body), 0)
		return stateResult(t, raw, err)
	}
	data := conclusion(t, w, launch, 2) // The target is still at revision 1.
	u := call("engine.artifact_begin", "begin", map[string]any{"purpose": "conclusion", "artifact": descriptor(data)})["upload_id"]
	call("engine.artifact_put_part", "part", map[string]any{"upload_id": u, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)})
	artifact := call("engine.artifact_commit", "commit", map[string]any{"upload_id": u})["artifact_receipt"]
	raw, err := s.Handle(context.Background(), stateWire("engine.record_append", "record", 1, map[string]any{"record_kind": "conclusion", "record": map[string]any{"artifact_receipt": artifact, "finish_reason": "no-useful-next-experiment"}}), 0)
	if err != nil || !strings.Contains(string(raw), "CONCLUSION_INVALID") || w.Fence().Err() != nil {
		t.Fatal(string(raw), err)
	}
	call("engine.request_stop", "stop", map[string]any{"finish_reason": "harness-error", "conclusion": map[string]any{"state": "unavailable", "reason": "serialization-failed"}})
	// Immediate administrative stop must still bypass the grace period.
	s.Stop(context.Canceled)
	select {
	case <-r.killed:
	case <-time.After(time.Second):
		t.Fatal("graceful timer delayed administrator stop")
	}
}
func TestServiceFinalizationClosesExplorationAndBoundsRequests(t *testing.T) {
	s, _, r, w, launch := serviceWithOperations(t, 0, completionRoutes)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginFinalization(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.artifact_begin", "payload", 1, map[string]any{"purpose": "payload", "artifact": descriptor([]byte(`{}`))}), 0)
	if err != nil || !strings.Contains(string(raw), "STATE_CHANGED") || w.Fence().Err() != nil {
		t.Fatal(string(raw), err)
	}
	for i := 0; i < 16; i++ {
		id := strings.Repeat("x", i+1)
		raw, err = s.Handle(context.Background(), stateWire("engine.record_append", id, 1, map[string]any{"record_kind": "conclusion", "record": map[string]any{"artifact_receipt": "missing", "finish_reason": "budget-limit"}}), 0)
		if err != nil || !strings.Contains(string(raw), "CONCLUSION_INVALID") {
			t.Fatal(string(raw), err)
		}
	}
	_, err = s.Handle(context.Background(), stateWire("engine.request_stop", "stop", 1, map[string]any{"finish_reason": "budget-limit", "conclusion": map[string]any{"state": "unavailable", "reason": "budget-limit"}}), 0)
	if err == nil {
		t.Fatal("unbounded finalization requests")
	}
	select {
	case <-r.killed:
	case <-time.After(time.Second):
		t.Fatal("finalization overrun did not terminate")
	}
}
func TestServiceGracefulStopCannotExtendHardDeadline(t *testing.T) {
	s, _, r, _, launch := serviceWithOperations(t, 600*time.Millisecond, []string{"engine.request_stop"})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.request_stop", "stop", 1, map[string]any{"finish_reason": "budget-limit", "conclusion": map[string]any{"state": "unavailable", "reason": "budget-limit"}}), 0)
	stateResult(t, raw, err)
	select {
	case <-r.killed:
	case <-time.After(2 * time.Second):
		t.Fatal("stop extended original hard deadline")
	}
}

func TestServiceAssessmentReferencesAttributionAndRateLimit(t *testing.T) {
	routes := append(append([]string{}, snapshotRoutes...), "engine.record_append")
	s, p, _, w, launch := serviceWithOperations(t, 0, routes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	call := func(op, id string, rev int, body any) map[string]any {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, rev, body), 0)
		return stateResult(t, raw, err)
	}
	deny := func(id string, rev int, body any, code string) {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire("engine.record_append", id, rev, body), 0)
		if err != nil || !strings.Contains(string(raw), code) {
			t.Fatal(string(raw), err)
		}
	}
	hypothesis := map[string]any{"record_kind": "hypothesis", "record": map[string]any{"hypothesis_id": "hyp-1", "objective_refs": []string{"objective-marker"}, "provenance": map[string]any{"origin": "scenario", "scenario_id": "scenario-marker"}, "statement": "A marker might appear.", "assumptions": []any{}, "predicted_observations": []any{}, "record_refs": []any{}}}
	first := call("engine.record_append", "hypothesis", 1, hypothesis)
	if first["assertion_origin"] != "harness" {
		t.Fatal(first)
	}
	raw, err := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0)
	attempt := stateResult(t, raw, err)["receipt_id"]
	lineage := map[string]any{"record_kind": "lineage", "record": map[string]any{"hypothesis_id": "hyp-1", "attempt_receipt_id": attempt, "mutation_summary": "Initial mutation", "record_refs": []any{first["receipt_id"]}}}
	call("engine.record_append", "lineage", 1, lineage)
	progress := map[string]any{"record_kind": "progress", "record": map[string]any{"summary": "An attempt completed.", "objective_refs": []any{"objective-marker"}, "hypothesis_refs": []any{"hyp-1"}, "attempt_receipt_refs": []any{attempt}, "observation_refs": []any{}, "record_refs": []any{first["receipt_id"]}}}
	call("engine.record_append", "progress", 1, progress)
	call("engine.record_append", "progress", 1, progress) // Exact replay does not incur the rate limit.
	deny("too-fast", 1, progress, "LIMIT_EXCEEDED")
	cp := call("engine.snapshot_request", "snapshot", 1, map[string]any{})["snapshot"].(map[string]any)
	call("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]})
	replay := call("engine.record_append", "hypothesis", 2, hypothesis)
	if replay["attribution"].(map[string]any)["run_revision"] != float64(1) || replay["receipt_id"] != first["receipt_id"] {
		t.Fatal("record replay changed attribution", replay)
	}
	call("engine.record_append", "lineage-2", 2, lineage)
	// A current-revision record may reference earlier receipts, but invented ones
	// never authorize assertions of completed execution.
	lineage["record"].(map[string]any)["attempt_receipt_id"] = "foreign"
	deny("unknown-attempt", 2, lineage, "RECORD_REFERENCE_INVALID")
	hypothesis["record"].(map[string]any)["objective_refs"] = []string{"foreign"}
	deny("unknown-objective", 2, hypothesis, "RECORD_REFERENCE_INVALID")
	hypothesis["record"].(map[string]any)["objective_refs"] = []string{"objective-marker"}
	hypothesis["record"].(map[string]any)["record_refs"] = []string{"foreign"}
	deny("unknown-record", 2, hypothesis, "RECORD_REFERENCE_INVALID")
}

func TestServiceHypothesisCycleRejected(t *testing.T) {
	s, _, _, _, launch := serviceWithOperations(t, 0, []string{"engine.record_append"})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	body := func(id, parent string) map[string]any {
		provenance := map[string]any{"origin": "exploratory"}
		if parent != "" {
			provenance["parent_hypothesis_id"] = parent
		}
		return map[string]any{"record_kind": "hypothesis", "record": map[string]any{"hypothesis_id": id, "objective_refs": []any{}, "provenance": provenance, "statement": "A bounded hypothesis.", "assumptions": []any{}, "predicted_observations": []any{}, "record_refs": []any{}}}
	}
	for _, q := range []struct{ id, parent string }{{"one", ""}, {"two", "one"}} {
		raw, err := s.Handle(context.Background(), stateWire("engine.record_append", q.id, 1, body(q.id, q.parent)), 0)
		stateResult(t, raw, err)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.record_append", "cycle", 1, body("one", "two")), 0)
	if err != nil || !strings.Contains(string(raw), "RECORD_REFERENCE_INVALID") {
		t.Fatal(string(raw), err)
	}
}

func TestServiceAssessmentObservationReferences(t *testing.T) {
	s, p, _, w, launch := serviceWithOperations(t, 0, []string{"engine.attempt_execute", "engine.record_append"})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	_ = json.Unmarshal(attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), &wire)
	wire["body"].(map[string]any)["observation_selection"] = map[string]any{"mode": "all-permitted"}
	raw, err := s.Handle(context.Background(), encode(wire), 0)
	result := stateResult(t, raw, err)
	entry := result["feedback"].(map[string]any)["entries"].([]any)[0].(map[string]any)
	observation := map[string]any{"attempt_receipt_id": result["receipt_id"], "entry_id": entry["entry_id"], "range": map[string]any{"offset": 0, "length": 1}}
	body := map[string]any{"record_kind": "progress", "record": map[string]any{"summary": "A response byte was retained.", "objective_refs": []any{}, "hypothesis_refs": []any{}, "attempt_receipt_refs": []any{result["receipt_id"]}, "observation_refs": []any{observation}, "record_refs": []any{}, "assessment": map[string]any{"interpretation": "supported", "confidence": "low", "assurance": "recorded-attributed-fact", "summary": "The output contains one recorded byte.", "gaps": []any{}}}}
	// Invalid references fail before rate accounting; the valid record comes last.
	for _, tc := range []struct {
		id     string
		mutate func()
	}{{"unknown-entry", func() { observation["entry_id"] = "unknown" }}, {"bad-range", func() {
		observation["entry_id"] = entry["entry_id"]
		observation["range"] = map[string]any{"offset": 10000, "length": 1}
	}}} {
		tc.mutate()
		raw, err = s.Handle(context.Background(), stateWire("engine.record_append", tc.id, 1, body), 0)
		if err != nil || !strings.Contains(string(raw), "RECORD_REFERENCE_INVALID") {
			t.Fatal(string(raw), err)
		}
	}
	observation["range"] = map[string]any{"offset": 0, "length": 1}
	raw, err = s.Handle(context.Background(), stateWire("engine.record_append", "valid", 1, body), 0)
	stateResult(t, raw, err)
}
