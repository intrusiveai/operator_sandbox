package preparation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

var artifactRoutes = []string{"engine.artifact_begin", "engine.artifact_put_part", "engine.artifact_commit", "engine.attempt_execute"}

func descriptor(raw []byte) interceptor.ArtifactDescriptor {
	return interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(raw), SizeBytes: int64(len(raw)), MediaType: "application/json", Canonicalization: "jcs-v1"}
}
func TestServiceArtifactUploadThroughSpoolAndAttempt(t *testing.T) {
	s, p, _, w, launch := serviceWithOperations(t, 0, artifactRoutes)
	exchange := serviceSpool(t, s, p, w, launch)
	call := func(op, id string, body any) map[string]any {
		t.Helper()
		return stateResult(t, exchange(stateWire(op, id, 1, body)), nil)
	}
	// Exercise a full-size part beyond the inline metadata ceiling.
	data := []byte(`{"query":"` + strings.Repeat("x", 262144) + `"}`)
	d := descriptor(data)
	begin := map[string]any{"purpose": "payload", "artifact": d}
	created := call("engine.artifact_begin", "begin-1", begin)
	upload := created["upload_id"]
	if again := call("engine.artifact_begin", "begin-1", begin); again["upload_id"] != upload {
		t.Fatal("duplicate begin allocated again")
	}
	for i, offset := 0, 0; offset < len(data); i++ {
		end := min(offset+262144, len(data))
		body := map[string]any{"upload_id": upload, "offset": offset, "content": base64.StdEncoding.EncodeToString(data[offset:end])}
		id := fmt.Sprintf("part-%d", i)
		result := call("engine.artifact_put_part", id, body)
		if again := call("engine.artifact_put_part", id, body); again["next_offset"] != result["next_offset"] {
			t.Fatal("duplicate part appended twice")
		}
		offset = end
	}
	result := call("engine.artifact_commit", "commit-1", map[string]any{"upload_id": upload})
	if again := call("engine.artifact_commit", "commit-2", map[string]any{"upload_id": upload}); again["artifact_receipt"] != result["artifact_receipt"] {
		t.Fatal("immutable receipt changed")
	}
	// Target delivery has its own 64 KiB ceiling, independent of artifact storage.
	data = []byte(`{"query":"uploaded mutation"}`)
	d = descriptor(data)
	upload = call("engine.artifact_begin", "small-begin", map[string]any{"purpose": "payload", "artifact": d})["upload_id"]
	call("engine.artifact_put_part", "small-part", map[string]any{"upload_id": upload, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)})
	call("engine.artifact_commit", "small-commit", map[string]any{"upload_id": upload})
	var attempt map[string]any
	_ = json.Unmarshal(attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), &attempt)
	attempt["body"].(map[string]any)["payload"] = d
	got := stateResult(t, exchange(encode(attempt)), nil)
	if got["receipt_id"] == nil {
		t.Fatal(got)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(strings.Join(p.calls, ","), "application.invoke") {
		t.Fatal("committed payload not executed")
	}
}
func TestServiceArtifactRangesAndCampaignQuotaSurviveRestore(t *testing.T) {
	routes := append(append([]string{}, snapshotRoutes...), artifactRoutes[:3]...)
	s, _, _, _, launch := serviceWithOperations(t, 0, routes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	call := func(op, id string, rev int, body any) map[string]any {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, rev, body), 0)
		return stateResult(t, raw, err)
	}
	deny := func(op, id string, rev int, body any, code string) {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, rev, body), 0)
		if err != nil || !strings.Contains(string(raw), code) {
			t.Fatal(string(raw), err)
		}
	}
	d := descriptor([]byte(`{}`))
	begin := map[string]any{"purpose": "payload", "artifact": d}
	u := call("engine.artifact_begin", "begin", 1, begin)["upload_id"]
	deny("engine.artifact_commit", "early", 1, map[string]any{"upload_id": u}, "UPLOAD_INCOMPLETE")
	deny("engine.artifact_put_part", "gap", 1, map[string]any{"upload_id": u, "offset": 1, "content": "e30="}, "UPLOAD_RANGE_INVALID")
	part := map[string]any{"upload_id": u, "offset": 0, "content": "e30="}
	call("engine.artifact_put_part", "part", 1, part)
	deny("engine.artifact_put_part", "overlap", 1, part, "UPLOAD_RANGE_INVALID")
	deny("engine.artifact_put_part", "part", 1, map[string]any{"upload_id": u, "offset": 0, "content": "W10="}, "IDEMPOTENCY_CONFLICT")
	cp := call("engine.snapshot_request", "snapshot", 1, map[string]any{})["snapshot"].(map[string]any)
	restored := call("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]})
	remaining := restored["remaining_limits"].(map[string]any)
	if remaining["artifact_objects"] != float64(8) {
		t.Fatal("restore refunded upload", remaining)
	}
	if again := call("engine.artifact_begin", "begin", 2, begin); again["upload_id"] != u {
		t.Fatal("restore changed duplicate identity")
	}
	call("engine.artifact_put_part", "part", 2, part)
	call("engine.artifact_commit", "commit", 2, map[string]any{"upload_id": u})
	// Reserve the remaining ordinary slots without letting them consume the
	// protected conclusion slot. Pending uploads count even without a commit.
	for i := 0; i < 7; i++ {
		call("engine.artifact_begin", fmt.Sprintf("empty-%d", i), 2, begin)
	}
	deny("engine.artifact_begin", "exhausted", 2, begin, "ARTIFACT_BUDGET_EXCEEDED")
	call("engine.artifact_begin", "conclusion", 2, map[string]any{"purpose": "conclusion", "artifact": d})
	deny("engine.artifact_begin", "over", 2, begin, "ARTIFACT_BUDGET_EXCEEDED")
}
func TestServiceArtifactIntegrityFailureTerminates(t *testing.T) {
	for _, mode := range []string{"digest", "canonical"} {
		t.Run(mode, func(t *testing.T) {
			s, _, runtime, _, launch := serviceWithOperations(t, 0, artifactRoutes)
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			data := []byte("{ }")
			d := descriptor(data)
			if mode == "digest" {
				d.Digest = contracts.RawDigest([]byte("bad"))
			}
			raw, err := s.Handle(context.Background(), stateWire("engine.artifact_begin", "begin", 1, map[string]any{"purpose": "payload", "artifact": d}), 0)
			u := stateResult(t, raw, err)["upload_id"]
			raw, err = s.Handle(context.Background(), stateWire("engine.artifact_put_part", "part", 1, map[string]any{"upload_id": u, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)}), 1)
			stateResult(t, raw, err)
			raw, err = s.Handle(context.Background(), stateWire("engine.artifact_commit", "commit", 1, map[string]any{"upload_id": u}), 2)
			if err != nil || !strings.Contains(string(raw), "ARTIFACT_INTEGRITY_FAILED") {
				t.Fatal(string(raw), err)
			}
			select {
			case <-runtime.killed:
			case <-time.After(time.Second):
				t.Fatal("integrity failure left execution open")
			}
		})
	}
}
