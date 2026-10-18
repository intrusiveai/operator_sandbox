//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

// The committed fixture preserves the shape of aws-openai-gpt5-mini-008 while
// replacing retained identities and prose. An optional local path verifies the
// original bytes, without copying private evidence or contacting its real target.
func TestConclusionLocalHypothesisCompletion(t *testing.T) { retainedConclusionSequence(t, "", nil) }
func TestConclusionUnavailableEvidence(t *testing.T) {
	retainedConclusionSequence(t, "evidence_unavailable", nil)
}
func TestConclusionObservationProtections(t *testing.T) {
	for _, tc := range []struct {
		reason string
		mutate func(map[string]any)
	}{
		{"invalid_observation", func(c map[string]any) {
			c["claims"].([]any)[0].(map[string]any)["observation_refs"].([]any)[0].(map[string]any)["entry_id"] = "missing-entry"
		}},
		{"invalid_range", func(c map[string]any) {
			c["claims"].([]any)[0].(map[string]any)["observation_refs"].([]any)[0].(map[string]any)["range"] = map[string]any{"offset": 1000000, "length": 1}
		}},
		{"invalid_attempt", func(c map[string]any) {
			cl := c["claims"].([]any)[0].(map[string]any)
			cl["attempt_receipt_refs"] = []any{"missing-attempt"}
			cl["observation_refs"].([]any)[0].(map[string]any)["attempt_receipt_id"] = "missing-attempt"
		}},
	} {
		t.Run(tc.reason, func(t *testing.T) { retainedConclusionSequence(t, tc.reason, tc.mutate) })
	}
}
func retainedConclusionSequence(t *testing.T, reason string, mutate func(map[string]any)) {
	source := "testdata/conclusion-local-hypothesis.json"
	if path := os.Getenv("OPERATOR_TEST_RETAINED_CONCLUSION"); path != "" {
		source = path
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.ValidateConclusion(data); err != nil {
		t.Fatal("retained conclusion fails structure", err)
	}
	var root string
	s, p, _, w, launch := serviceWithSettings(t, 0, append(append([]string{}, completionRoutes...), "engine.attempt_execute"), func(c *campaignservice.Config) { root = c.StateRoot })
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	call := func(op, id string, body any) map[string]any {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, 1, body), 0)
		return stateResult(t, raw, err)
	}
	var wire map[string]any
	_ = json.Unmarshal(attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), &wire)
	wire["body"].(map[string]any)["observation_selection"] = map[string]any{"mode": "all-permitted"}
	raw, err := s.Handle(context.Background(), encode(wire), 0)
	attempt := stateResult(t, raw, err)
	entry := attempt["feedback"].(map[string]any)["entries"].([]any)[0].(map[string]any)
	if reason == "evidence_unavailable" {
		var path string
		_, err := campaign.Observe(context.Background(), root, w.Manifest().CampaignID, func(e campaign.Event) error {
			if e.Kind == "attempt.feedback-staged" && len(e.Content) > 0 {
				path = e.Content[0].Path
			}
			return nil
		})
		if err != nil || path == "" {
			t.Fatal("missing stored feedback", err)
		}
		if err = os.WriteFile(filepath.Join(root, "campaigns", w.Manifest().CampaignID, filepath.FromSlash(path)), []byte("corrupted feedback"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	var c, bound map[string]any
	_ = json.Unmarshal(data, &c)
	_ = json.Unmarshal(conclusion(t, w, launch, 1), &bound)
	c["binding"] = bound["binding"]
	var rebind func(any)
	rebind = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, v := range x {
				rebind(v)
			}
		case map[string]any:
			for k, v := range x {
				switch k {
				case "objective_id":
					x[k] = "objective-marker"
				case "objective_refs":
					x[k] = []any{"objective-marker"}
				case "scenario_id":
					x[k] = "scenario-marker"
				case "attempt_receipt_id":
					x[k] = attempt["receipt_id"]
				case "attempt_receipt_refs":
					refs := []any{}
					for range v.([]any) {
						refs = append(refs, attempt["receipt_id"])
					}
					x[k] = refs
				case "entry_id":
					x[k] = entry["entry_id"]
				default:
					rebind(v)
				}
			}
		}
	}
	rebind(c)
	if mutate != nil {
		mutate(c)
	}
	data = encode(c)
	upload := call("engine.artifact_begin", "begin", map[string]any{"purpose": "conclusion", "artifact": descriptor(data)})["upload_id"]
	call("engine.artifact_put_part", "part", map[string]any{"upload_id": upload, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)})
	artifact := call("engine.artifact_commit", "commit", map[string]any{"upload_id": upload})["artifact_receipt"]
	request := stateWire("engine.record_append", "record", 1, map[string]any{"record_kind": "conclusion", "record": map[string]any{"artifact_receipt": artifact, "finish_reason": c["finish_reason"]}})
	raw, err = s.Handle(context.Background(), request, 0)
	if reason != "" {
		d := s.ConclusionFailure()
		if err != nil || d == nil || d.Reason != reason || !strings.HasPrefix(d.FieldPath, "/claims/0/") {
			t.Fatal(string(raw), err, d)
		}
		return
	}
	record := stateResult(t, raw, err)["receipt_id"]
	stop := call("engine.request_stop", "stop", map[string]any{"finish_reason": c["finish_reason"], "conclusion": map[string]any{"state": "committed", "artifact_receipt": artifact, "record_receipt": record}})
	if stop["status"] != "accepted" || !s.StopAccepted() {
		t.Fatal(stop)
	}
}
