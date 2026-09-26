package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/capabilities"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/preparation"
)

func snapshotInput(t *testing.T) func(*preparation.Input) {
	return func(in *preparation.Input) {
		// Change the captured native hash preimage, preserving native struct order.
		raw := bytes.TrimSpace(read(t, "../../schemas/fixtures/capability-chain/native-digest-input.json"))
		raw = bytes.Replace(raw, []byte(`"snapshot_capable":false`), []byte(`"snapshot_capable":true`), 1)
		var native map[string]any
		_ = json.Unmarshal(raw, &native)
		native["digest"] = contracts.RawDigest(raw)
		updated := encode(native)
		export, err := capabilities.FromNative(in.Protocol.Catalog(), updated, "delivery-example")
		if err != nil {
			t.Fatal(err)
		}
		in.Attachment.Capabilities = updated
		in.Attachment.Session.CapabilityManifestDigest = export.SourceDigest()
	}
}
func (p *peer) createCheckpoint(raw []byte, result func(int, any) (interceptor.Response, error)) (interceptor.Response, error) {
	var input interceptor.SnapshotCreate
	_ = json.Unmarshal(raw, &input)
	p.allowances = append(p.allowances, input.MaximumCommittedBytes)
	if p.restoreMode == "create-budget" {
		return result(429, map[string]string{"code": "snapshot_bytes_exhausted"})
	}
	s := p.input.Attachment.Session
	cp := interceptor.Checkpoint{CampaignID: s.CampaignID, Description: input.Description, SchemaVersion: 1, ID: fmt.Sprintf("cp-%d-%024x", len(p.checkpoints)+1, len(p.checkpoints)+1), Label: input.Label, SourceSessionID: s.ID, EnvironmentDigest: s.EnvironmentDigest, AppDigest: s.AppDigest, JournalSeq: 1, JournalHash: contracts.RawDigest(nil), FileManifestDigest: contracts.RawDigest(nil), CanonicalSizeBytes: 2, EventSeq: 1, CreatedAt: time.Now().UTC(), InterceptorVersion: "test", JournalSchemaVersion: 1, Status: "ready"}
	cp.Hash = contracts.RawDigest(encode(cp))
	p.checkpoints = append(p.checkpoints, cp)
	if p.checkpointContexts == nil {
		p.checkpointContexts = map[string]map[string]interceptor.AttemptContext{}
	}
	contexts := map[string]interceptor.AttemptContext{}
	for id, c := range p.contexts {
		contexts[id] = c
	}
	p.checkpointContexts[cp.SourceSessionID+"/"+cp.ID] = contexts
	if p.restoreMode == "create-lost" {
		return interceptor.Response{}, &interceptor.CallError{Kind: "lost_reply", Uncertain: true}
	}
	return result(201, cp)
}
func (p *peer) ListSnapshots(_ context.Context, q interceptor.SnapshotListRequest) (interceptor.SnapshotPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	all := []interceptor.Checkpoint{}
	for _, cp := range p.checkpoints {
		if q.SourceSessionID == "" || q.SourceSessionID == cp.SourceSessionID {
			all = append(all, cp)
		}
	}
	start := min(q.Offset, len(all))
	end := min(start+q.Limit, len(all))
	page := interceptor.SnapshotPage{CampaignID: q.CampaignID, Offset: q.Offset, Total: len(all), Checkpoints: append([]interceptor.Checkpoint{}, all[start:end]...)}
	if end < len(all) {
		page.NextOffset = &end
	}
	return page, nil
}
func (p *peer) InspectSnapshot(_ context.Context, campaign, source, id string) (interceptor.Checkpoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cp := range p.checkpoints {
		if cp.CampaignID == campaign && cp.SourceSessionID == source && cp.ID == id {
			return cp, nil
		}
	}
	return interceptor.Checkpoint{}, &interceptor.RemoteError{Response: interceptor.Response{Status: 404, Body: json.RawMessage(`{"code":"checkpoint_not_found"}`)}}
}
func (p *peer) restoreCheckpoint(q interceptor.PreparedLifecycle) (interceptor.Response, error) {
	r := q.Request()
	p.restores++
	if p.restoreMode == "reject" {
		return interceptor.ParseResponse(encode(map[string]any{"status": 409, "session_revision": p.revision, "body": map[string]string{"code": "checkpoint_unavailable_or_incompatible"}}))
	}
	prior := p.input.Attachment.Binding
	binding := interceptor.Binding{SessionID: fmt.Sprintf("session-%d", prior.RunRevision+1), WorkerInstanceID: prior.WorkerInstanceID, RunRevision: prior.RunRevision + 1}
	metadata := p.input.Attachment.Session
	metadata.ID = binding.SessionID
	metadata.Revision = 1
	p.contexts = map[string]interceptor.AttemptContext{}
	for id, c := range p.checkpointContexts[r.SourceSessionID+"/"+r.CheckpointID] {
		p.contexts[id] = c
	}
	p.revision = 1
	p.closed = false
	p.input.Attachment.Binding = binding
	p.input.Attachment.Session = metadata
	p.input.Status.Active = binding
	if p.restoreMode == "lost" {
		return interceptor.Response{}, &interceptor.CallError{Kind: "lost_reply", Uncertain: true}
	}
	session := map[string]any{}
	_ = json.Unmarshal(encode(metadata), &session)
	session["parent_session_id"] = r.SourceSessionID
	session["parent_checkpoint"] = r.CheckpointID
	if p.restoreMode == "wrong-lineage" {
		session["parent_checkpoint"] = "cp-wrong"
	}
	return interceptor.ParseResponse(encode(map[string]any{"status": 200, "session_revision": p.revision, "body": map[string]any{"binding": binding, "session": session, "source_session_id": r.SourceSessionID, "checkpoint_id": r.CheckpointID}}))
}

var snapshotRoutes = []string{"engine.attempt_execute", "engine.observation_read", "engine.injection_delete", "engine.snapshot_request", "engine.snapshot_list", "engine.snapshot_inspect", "engine.restore_request"}

func stateWire(op, id string, rev int, body any) []byte {
	return encode(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 0, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": rev, "call_id": id, "operation_id": id, "operation": op, "timeout_ms": 30000, "body": body})
}
func stateResult(t *testing.T, raw []byte, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	if v["error"] != nil {
		t.Fatal(string(raw))
	}
	return v["result"].(map[string]any)
}
func TestServiceSnapshotsRestoreAndCumulativeLimits(t *testing.T) {
	s, p, r, w, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	call := func(op, id string, rev int, body any) map[string]any {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, rev, body), 0)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return stateResult(t, raw, err)
	}
	created := call("engine.snapshot_request", "snapshot-1", 1, map[string]any{"label": "baseline", "description": "Before branch one"})
	cp := created["snapshot"].(map[string]any)
	if cp["description"] != "Before branch one" || cp["campaign_id"] != "campaign-1" {
		t.Fatal(cp)
	}
	handle := map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}
	listed := call("engine.snapshot_list", "list-1", 1, map[string]any{})
	if listed["total"] != float64(1) {
		t.Fatal(listed)
	}
	inspected := call("engine.snapshot_inspect", "inspect-1", 1, handle)
	if inspected["snapshot"].(map[string]any)["checkpoint_id"] != cp["checkpoint_id"] {
		t.Fatal(inspected)
	}
	restored := call("engine.restore_request", "restore-1", 1, handle)
	if restored["run_revision"] != float64(2) || restored["harness_disposition"] != "continue" {
		t.Fatal(restored)
	}
	duplicate := call("engine.restore_request", "restore-1", 2, handle)
	if duplicate["transition_receipt"] != restored["transition_receipt"] || duplicate["run_revision"] != float64(2) {
		t.Fatal(duplicate)
	}
	for _, test := range []struct {
		id       string
		revision int
		body     any
		code     string
	}{
		{"stale-restore", 1, handle, "STATE_CHANGED"},
		{"restore-1", 2, map[string]any{"source_session": "session-1", "checkpoint_id": "cp-missing"}, "IDEMPOTENCY_CONFLICT"},
		{"missing-restore", 2, map[string]any{"source_session": "session-1", "checkpoint_id": "cp-missing"}, "SNAPSHOT_NOT_FOUND"},
	} {
		raw, err := s.Handle(context.Background(), stateWire("engine.restore_request", test.id, test.revision, test.body), 0)
		if err != nil || !strings.Contains(string(raw), test.code) {
			t.Fatal(string(raw), err)
		}
	}
	second := call("engine.snapshot_request", "snapshot-2", 2, map[string]any{})
	remaining := second["remaining_limits"].(map[string]any)
	if remaining["snapshot_admissions"] != float64(0) || remaining["snapshot_bytes"] != float64(0) {
		t.Fatal(remaining)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot-3", 2, map[string]any{}), 0)
	if err != nil || !strings.Contains(string(raw), "SNAPSHOT_BUDGET_EXCEEDED") {
		t.Fatal(string(raw), err)
	}
	raw, err = s.Handle(context.Background(), attemptWire(p, 2, 1, w.Manifest().ReleaseRecordDigest), 0)
	stateResult(t, raw, err)
	select {
	case <-r.killed:
		t.Fatal("healthy restore killed harness")
	default:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.restores != 1 || len(p.allowances) != 2 || p.allowances[0] != 4 || p.allowances[1] != 2 {
		t.Fatal(p.restores, p.allowances)
	}
}
func TestServiceRestorePreflightAndUncertainty(t *testing.T) {
	for _, mode := range []string{"reject", "lost", "wrong-lineage"} {
		t.Run(mode, func(t *testing.T) {
			s, p, r, _, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{}), 0)
			cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
			p.mu.Lock()
			p.restoreMode = mode
			p.mu.Unlock()
			raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}), 0)
			if mode == "reject" {
				if err != nil || !strings.Contains(string(raw), "SNAPSHOT_INCOMPATIBLE") {
					t.Fatal(string(raw), err)
				}
				raw, err = s.Handle(context.Background(), stateWire("engine.snapshot_list", "list", 1, map[string]any{}), 0)
				stateResult(t, raw, err)
				select {
				case <-r.killed:
					t.Fatal("known rejection killed harness")
				default:
				}
			} else {
				if err == nil {
					t.Fatal("uncertain restore returned success", string(raw))
				}
				select {
				case <-r.killed:
				case <-time.After(time.Second):
					t.Fatal("uncertainty did not terminate")
				}
			}
		})
	}
}

func TestServiceRestorePreservesCampaignLineageAcrossNativeRoots(t *testing.T) {
	s, p, _, w, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	attempt := func(rev, index int, parent string) {
		t.Helper()
		var wire map[string]any
		_ = json.Unmarshal(attemptWire(p, rev, index, w.Manifest().ReleaseRecordDigest), &wire)
		if parent != "" {
			wire["body"].(map[string]any)["parent_attempt_id"] = parent
		}
		raw, err := s.Handle(context.Background(), encode(wire), 0)
		result := stateResult(t, raw, err)
		if result["status"] != "completed" {
			t.Fatal(result)
		}
	}
	attempt(1, 1, "")
	raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "baseline", 1, map[string]any{}), 0)
	cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
	handle := map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}
	attempt(1, 2, "")
	raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, handle), 0)
	stateResult(t, raw, err)
	attempt(2, 3, "") // Parent attempt-2 is absent in the restored registry.
	attempt(2, 4, "") // Its native child must use native generation two, harness four.
	p.mu.Lock()
	if p.contexts["attempt-3"].Generation != 1 || p.contexts["attempt-3"].ParentAttemptID != "" || p.contexts["attempt-4"].Generation != 2 {
		t.Fatal(p.contexts)
	}
	p.mu.Unlock()
	raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore-again", 2, handle), 0)
	stateResult(t, raw, err)
	// A parent that actually exists in the checkpoint keeps its native edge.
	var wire map[string]any
	_ = json.Unmarshal(attemptWire(p, 3, 5, w.Manifest().ReleaseRecordDigest), &wire)
	body := wire["body"].(map[string]any)
	body["parent_attempt_id"] = "attempt-1"
	body["generation"] = 2
	raw, err = s.Handle(context.Background(), encode(wire), 0)
	stateResult(t, raw, err)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.contexts["attempt-5"].ParentAttemptID != "attempt-1" || p.contexts["attempt-5"].Generation != 2 {
		t.Fatal(p.contexts)
	}
}

func TestServiceSnapshotFailuresPreserveCountsAndPreventReplay(t *testing.T) {
	for _, mode := range []string{"create-budget", "create-lost"} {
		t.Run(mode, func(t *testing.T) {
			s, p, r, _, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			p.mu.Lock()
			p.restoreMode = mode
			p.mu.Unlock()
			request := stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{})
			raw, err := s.Handle(context.Background(), request, 0)
			if mode == "create-lost" {
				if err == nil {
					t.Fatal("lost commitment replied", string(raw))
				}
				select {
				case <-r.killed:
				case <-time.After(time.Second):
					t.Fatal("lost commitment did not terminate")
				}
				if _, err = s.Handle(context.Background(), request, 0); err == nil {
					t.Fatal("reopened uncertain commitment")
				}
			} else {
				if err != nil || !strings.Contains(string(raw), "SNAPSHOT_BUDGET_EXCEEDED") {
					t.Fatal(string(raw), err)
				}
				duplicate, e := s.Handle(context.Background(), request, 1)
				if e != nil || !strings.Contains(string(duplicate), "SNAPSHOT_BUDGET_EXCEEDED") {
					t.Fatal(string(duplicate), e)
				}
				p.mu.Lock()
				p.restoreMode = ""
				p.mu.Unlock()
				raw, err = s.Handle(context.Background(), stateWire("engine.snapshot_request", "second", 1, map[string]any{}), 0)
				remaining := stateResult(t, raw, err)["remaining_limits"].(map[string]any)
				if remaining["snapshot_admissions"] != float64(0) || remaining["snapshot_bytes"] != float64(2) {
					t.Fatal(remaining)
				}
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			want := 1
			if mode == "create-budget" {
				want = 2
			}
			if len(p.allowances) != want {
				t.Fatal("replayed native effect", p.allowances)
			}
		})
	}
}

func TestServiceSpoolContinuesSameLaunchAfterRestore(t *testing.T) {
	s, p, r, w, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	exchange := serviceSpool(t, s, p, w, launch)
	cp := stateResult(t, exchange(stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{})), nil)["snapshot"].(map[string]any)
	handle := map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}
	result := stateResult(t, exchange(stateWire("engine.restore_request", "restore", 1, handle)), nil)
	if result["run_revision"] != float64(2) {
		t.Fatal(result)
	}
	stateResult(t, exchange(attemptWire(p, 2, 1, w.Manifest().ReleaseRecordDigest)), nil)
	stateResult(t, exchange(stateWire("engine.restore_request", "restore", 2, handle)), nil)
	select {
	case <-r.killed:
		t.Fatal("restore killed the live launch")
	default:
	}
}

func TestServicePlannedRestoreWatcherAndIndependentStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			s, p, r, _, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{}), 0)
			cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
			entered, release := make(chan struct{}), make(chan struct{})
			p.mu.Lock()
			p.restoreEntered = entered
			p.restoreRelease = release
			p.mu.Unlock()
			done := make(chan error, 1)
			go func() {
				_, err := s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}), 0)
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("restore not started")
			}
			if stop {
				s.Stop(fmt.Errorf("administrator stop during restore"))
				select {
				case <-r.killed:
				case <-time.After(time.Second):
					t.Fatal("native restore blocked Docker termination")
				}
			} else {
				select {
				case <-r.killed:
					t.Fatal("planned transition treated as failure")
				case <-time.After(1200 * time.Millisecond):
				}
			}
			close(release)
			select {
			case err := <-done:
				if (err != nil) != stop {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("restore did not finish")
			}
		})
	}
}

func TestServiceRestoreJournalLossCannotPublishOrContinue(t *testing.T) {
	s, p, r, w, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{}), 0)
	cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
	p.mu.Lock()
	p.beforeRestoreResult = func() { w.Close() }
	p.mu.Unlock()
	raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}), 0)
	if err == nil || len(raw) != 0 {
		t.Fatal("published an unretained transition", string(raw), err)
	}
	select {
	case <-r.killed:
	case <-time.After(time.Second):
		t.Fatal("journal loss did not terminate")
	}
	if _, err = s.Handle(context.Background(), stateWire("engine.snapshot_list", "list", 2, map[string]any{}), 0); err == nil {
		t.Fatal("journal loss reopened execution")
	}
}

type delayedStatus struct {
	entered, release chan struct{}
	status           interceptor.Status
}

func TestServiceDiscardsStaleWatcherSampleAfterRejectedRestore(t *testing.T) {
	s, p, r, _, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{}), 0)
	cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
	sample := &delayedStatus{entered: make(chan struct{}), release: make(chan struct{})}
	p.mu.Lock()
	sample.status = p.input.Status
	sample.status.Phase = "transitioning"
	p.statusSample = sample
	p.restoreMode = "reject"
	p.mu.Unlock()
	// The watcher's query starts before restore, but its stale transition result
	// arrives after preflight rejection restored the unchanged ready binding.
	select {
	case <-sample.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not sample")
	}
	raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}), 0)
	if err != nil || !strings.Contains(string(raw), "SNAPSHOT_INCOMPATIBLE") {
		t.Fatal(string(raw), err)
	}
	close(sample.release)
	select {
	case <-r.killed:
		t.Fatal("stale watcher sample ended healthy execution")
	case <-time.After(1200 * time.Millisecond):
	}
}
