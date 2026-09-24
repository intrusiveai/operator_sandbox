package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"testing"
	"time"
)

func lifecycleQuery() LifecycleRequest {
	return LifecycleRequest{WorkerInstanceID: "worker-9", RunRevision: 99, CampaignID: "campaign-1", SessionID: "sess-1", OperationID: "restore-1", Operation: "snapshot.restore", CheckpointID: "checkpoint-1"}
}

func preparedLifecycle(t *testing.T, q LifecycleRequest) PreparedLifecycle {
	t.Helper()
	p, err := PrepareLifecycle(q, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertUncertain(t *testing.T, err error) {
	t.Helper()
	var call *CallError
	if !errors.As(err, &call) || !call.Uncertain {
		t.Fatalf("want uncertain error, got %v", err)
	}
}

func TestLifecycleFreezeDeadlineAndNoReplay(t *testing.T) {
	p := preparedLifecycle(t, lifecycleQuery())
	want := p.Bytes()
	p.Bytes()[0] = '!'
	q := p.Request()
	q.CheckpointID = "changed"
	if !bytes.Equal(want, p.Bytes()) || p.Request().CheckpointID != "checkpoint-1" {
		t.Fatal("mutable command")
	}
	calls := 0
	c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		deadline, ok := r.Context().Deadline()
		if r.URL.String() != Address+"/v1/lifecycle" || r.Method != "POST" || r.GetBody != nil || !bytes.Equal(raw, want) || !ok || !deadline.Equal(p.Deadline()) {
			t.Error("dispatch changed command/deadline")
		}
		return nil, errors.New("lost reply after admission")
	})}}
	_, err := c.ExecuteLifecycle(context.Background(), p)
	assertUncertain(t, err)
	if calls != 1 {
		t.Fatal("replayed mutation", calls)
	}
	past, err := PrepareLifecycle(lifecycleQuery(), time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ExecuteLifecycle(context.Background(), past)
	var call *CallError
	if !errors.As(err, &call) || call.Uncertain || calls != 1 {
		t.Fatal("expired request dispatched", err, calls)
	}
	if _, err = c.ExecuteLifecycle(context.Background(), PreparedLifecycle{}); err == nil {
		t.Fatal("zero request dispatched")
	}
}

func TestLifecycleRequestGuards(t *testing.T) {
	for _, change := range []func(*LifecycleRequest){
		func(q *LifecycleRequest) { q.APIVersion = "wrong" },
		func(q *LifecycleRequest) { q.WorkerInstanceID = "" },
		func(q *LifecycleRequest) { q.RunRevision = 0 },
		func(q *LifecycleRequest) { q.CampaignID = "../bad" },
		func(q *LifecycleRequest) { q.SessionID = "" },
		func(q *LifecycleRequest) { q.OperationID = "" },
		func(q *LifecycleRequest) { q.Operation = "session.owner" },
		func(q *LifecycleRequest) { q.CheckpointID = "" },
		func(q *LifecycleRequest) { q.SourceSessionID = "../bad" },
		func(q *LifecycleRequest) { q.Operation = "session.stop" },
	} {
		q := lifecycleQuery()
		change(&q)
		if _, err := PrepareLifecycle(q, time.Now()); err == nil {
			t.Fatal("accepted", q)
		}
	}
	if _, err := PrepareLifecycle(lifecycleQuery(), time.Time{}); err == nil {
		t.Fatal("missing host deadline")
	}
}

func restoreFixture(t *testing.T, p PreparedLifecycle) (Binding, Session, map[string]any) {
	t.Helper()
	a, err := fakeClient(t, "/v1/attach", attachmentBody(t)).Attach(context.Background(), "campaign-1", "worker-2", true)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(a.RawSession, &metadata); err != nil {
		t.Fatal(err)
	}
	source := p.Request().SourceSessionID
	if source == "" {
		source = p.Request().SessionID
	}
	metadata["id"], metadata["parent_session_id"], metadata["parent_checkpoint"] = "sess-new", source, p.Request().CheckpointID
	// Native session revision can reset independently of campaign run revision.
	metadata["revision"] = 1
	return a.Binding, a.Session, map[string]any{"binding": Binding{"sess-new", "worker-original", a.Binding.RunRevision + 1}, "session": metadata, "source_session_id": source, "checkpoint_id": p.Request().CheckpointID}
}

func TestRestoreLineageAndAttribution(t *testing.T) {
	for _, source := range []string{"", "sess-older"} {
		q := lifecycleQuery()
		q.SourceSessionID = source
		p := preparedLifecycle(t, q)
		prior, target, body := restoreFixture(t, p)
		r := Response{Status: 200, Body: encoded(t, body)}
		got, err := DecodeRestore(r, p, prior, target)
		if err != nil || got.Binding.RunRevision != 3 || got.Session.Revision != 1 || got.Binding.WorkerInstanceID != "worker-original" {
			t.Fatal(got, err)
		}
		// Decoding neither adopts the binding nor alters host admission state.
		if prior.SessionID != "sess-1" || p.Request().RunRevision != 99 {
			t.Fatal("mutated original binding/request")
		}
		r.Body[0] = '!'
		if _, err := object(got.RawSession); err != nil {
			t.Fatal("raw metadata aliases reply")
		}
	}
}

func TestRestoreRejectsWrongReplacement(t *testing.T) {
	changes := map[string]func(map[string]any){
		"same session":         func(b map[string]any) { b["binding"] = Binding{"sess-1", "worker-9", 3} },
		"skipped revision":     func(b map[string]any) { b["binding"] = Binding{"sess-new", "worker-9", 4} },
		"attribution revision": func(b map[string]any) { b["binding"] = Binding{"sess-new", "worker-9", 100} },
		"missing binding revision": func(b map[string]any) {
			b["binding"] = map[string]any{"session_id": "sess-new", "worker_instance_id": "worker-9"}
		},
		"wrong source":            func(b map[string]any) { b["source_session_id"] = "sess-other" },
		"wrong checkpoint":        func(b map[string]any) { b["checkpoint_id"] = "checkpoint-other" },
		"wrong parent":            func(b map[string]any) { b["session"].(map[string]any)["parent_session_id"] = "sess-other" },
		"wrong parent checkpoint": func(b map[string]any) { b["session"].(map[string]any)["parent_checkpoint"] = "checkpoint-other" },
		"wrong campaign":          func(b map[string]any) { b["session"].(map[string]any)["campaign_id"] = "campaign-other" },
		"wrong metadata id":       func(b map[string]any) { b["session"].(map[string]any)["id"] = "sess-other" },
		"wrong image":             func(b map[string]any) { b["session"].(map[string]any)["app_digest"] = rawDigest([]byte("changed")) },
		"wrong capabilities": func(b map[string]any) {
			b["session"].(map[string]any)["capability_manifest_digest"] = rawDigest([]byte("changed"))
		},
		"changed profile":      func(b map[string]any) { b["session"].(map[string]any)["feedback_profile"] = "black-box" },
		"terminal replacement": func(b map[string]any) { b["session"].(map[string]any)["phase"] = "error" },
		"unknown top field":    func(b map[string]any) { b["owner"] = map[string]any{} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			p := preparedLifecycle(t, lifecycleQuery())
			prior, target, body := restoreFixture(t, p)
			change(body)
			_, err := DecodeRestore(Response{Status: 200, Body: encoded(t, body)}, p, prior, target)
			assertUncertain(t, err)
		})
	}
	p := preparedLifecycle(t, lifecycleQuery())
	prior, target, body := restoreFixture(t, p)
	prior.RunRevision = math.MaxUint64
	if _, err := DecodeRestore(Response{Status: 200, Body: encoded(t, body)}, p, prior, target); !errors.Is(err, ErrRequest) {
		t.Fatal("overflow accepted", err)
	}
}

func TestLifecycleNativeRejectionsAndStop(t *testing.T) {
	p := preparedLifecycle(t, lifecycleQuery())
	prior, target, _ := restoreFixture(t, p)
	for _, failure := range []struct {
		status int
		code   string
	}{{409, "checkpoint_unavailable_or_incompatible"}, {503, "restore_failed"}, {503, "lifecycle_outcome_unknown"}, {202, "operation_in_progress"}} {
		r := Response{Status: failure.status, Body: encoded(t, map[string]string{"code": failure.code})}
		_, err := DecodeRestore(r, p, prior, target)
		var remote *RemoteError
		if !errors.As(err, &remote) || remote.Response.Code() != failure.code {
			t.Fatal("lost native outcome", err)
		}
	}
	q := lifecycleQuery()
	q.Operation, q.CheckpointID = "session.stop", ""
	p = preparedLifecycle(t, q)
	for _, body := range []map[string]string{{"session_id": "sess-1", "phase": "stopped"}, {"session_id": "sess-other", "phase": "stopped"}, {"session_id": "sess-1", "phase": "stopping"}, {"phase": "stopped"}} {
		r, err := fakeClient(t, "/v1/lifecycle", body).ExecuteLifecycle(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeStop(r, p)
		if body["session_id"] == "sess-1" && body["phase"] == "stopped" {
			if err != nil || got.SessionID != "sess-1" {
				t.Fatal(got, err)
			}
		} else {
			assertUncertain(t, err)
		}
	}
}

func TestLifecycleReconciliationAfterLostReply(t *testing.T) {
	p := preparedLifecycle(t, lifecycleQuery())
	prior, target, body := restoreFixture(t, p)
	admitted := p.Request()
	admitted.WorkerInstanceID, admitted.RunRevision = "worker-original", 1
	record := LifecycleRecord{admitted, lifecycleFingerprint(admitted), "completed", Response{Status: 200, Body: encoded(t, body)}}
	// An expired mutation deadline must not prevent a fresh bounded status query.
	p.deadline = time.Now().Add(-time.Hour)
	calls := 0
	c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/status" {
			t.Fatal("mutation replayed")
		}
		raw, _ := io.ReadAll(r.Body)
		var q map[string]string
		if json.Unmarshal(raw, &q) != nil || len(q) != 3 || q["api_version"] != LifecycleVersion || q["operation_id"] != p.Request().OperationID || q["campaign_id"] != "campaign-1" {
			t.Fatal("wrong ledger query", string(raw))
		}
		return wireResponse(t, 200, record), nil
	})}}
	got, err := c.LifecycleStatus(context.Background(), p)
	if err != nil || got.State != "completed" || got.Request.WorkerInstanceID != "worker-original" {
		t.Fatal(got, err)
	}
	if _, err := DecodeRestore(got.Response, p, prior, target); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unexpected requests", calls)
	}
	// A missing record is preserved as an unresolved native response, not success.
	c.http.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return wireResponse(t, 404, map[string]string{"code": "operation_not_recorded"}), nil
	})
	_, err = c.LifecycleStatus(context.Background(), p)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Response.Code() != "operation_not_recorded" {
		t.Fatal(err)
	}
}

func TestLifecycleRecordValidation(t *testing.T) {
	p := preparedLifecycle(t, lifecycleQuery())
	base := LifecycleRecord{p.Request(), lifecycleFingerprint(p.Request()), "running", Response{Status: 202, Body: []byte(`{"code":"operation_in_progress"}`)}}
	if _, err := DecodeLifecycleRecord(Response{Status: 200, Body: encoded(t, base)}, p); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*LifecycleRecord){
		func(r *LifecycleRecord) { r.Fingerprint = rawDigest([]byte("wrong")) },
		func(r *LifecycleRecord) {
			r.Request.CheckpointID = "other"
			r.Fingerprint = lifecycleFingerprint(r.Request)
		},
		func(r *LifecycleRecord) {
			r.Request.SessionID = "other"
			r.Fingerprint = lifecycleFingerprint(r.Request)
		},
		func(r *LifecycleRecord) { r.Request.WorkerInstanceID = "" },
		func(r *LifecycleRecord) { r.State = "unknown" },
		func(r *LifecycleRecord) { r.State = "completed" },
		func(r *LifecycleRecord) { r.Response.Status = 200 },
		func(r *LifecycleRecord) { r.Response.Body = []byte(`{"code":"wrong"}`) },
	} {
		record := base
		mutate(&record)
		_, err := DecodeLifecycleRecord(Response{Status: 200, Body: encoded(t, record)}, p)
		assertUncertain(t, err)
	}
}
