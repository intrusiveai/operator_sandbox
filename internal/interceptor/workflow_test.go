//go:build linux || darwin

package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// This stateful protocol test covers routing across a replacement session and a
// lost restore reply. It does not launch Docker, implement the host broker, or
// authorize execution to resume after an uncertain outcome. After loss it only
// reconciles, closes/stops the replacement and reads retained source evidence.
func TestNativeClientWorkflowWithLostRestoreReply(t *testing.T) {
	attach := attachmentBody(t)
	metadata := attach["session"].(map[string]any)
	_, cp := checkpointFixture(t, false)
	cp.EnvironmentDigest = metadata["environment_digest"].(string)
	cp.AppDigest = metadata["app_digest"].(string)
	cp = rehashCheckpoint(t, cp)
	archive := archiveBytes(t, archiveMembers())
	var mu sync.Mutex
	active := Binding{"sess-1", "worker-2", 2}
	sessions := map[string]Binding{active.SessionID: active}
	phase, closed := "ready", false
	var savedSnapshot OperationRecord
	var savedRestore LifecycleRecord
	snapshotEffects, restoreEffects, stopEffects := 0, 0, 0
	var addressed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "bad body", 400)
			return
		}
		if r.Method != "POST" || r.Host != "127.0.0.1:8080" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected routing")
		}
		send := func(status int, body any, revision uint64) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(Response{Status: status, Body: encoded(t, body), SessionRevision: revision})
		}
		switch r.URL.Path {
		case "/v1/attach":
			var q map[string]any
			_ = json.Unmarshal(raw, &q)
			if q["campaign_id"] != "campaign-1" || q["worker_instance_id"] != "worker-2" || q["allow_target_stop"] != true {
				t.Error("wrong attachment")
			}
			send(200, attach, 4)
		case "/v1/status":
			var q map[string]string
			_ = json.Unmarshal(raw, &q)
			if q["campaign_id"] != "campaign-1" {
				t.Error("wrong campaign")
			}
			if q["operation_id"] != "" {
				send(200, savedRestore, 0)
				return
			}
			send(200, Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: active, Sessions: sessions, Phase: phase, Closed: closed, StoreAvailable: true, EvidenceMaxBytes: 4 << 30}, 0)
		case "/v1/operations":
			var q struct {
				Request OperationRequest `json:"request"`
				Body    json.RawMessage  `json:"body"`
			}
			if err := json.Unmarshal(raw, &q); err != nil || q.Request.BodyDigest != rawDigest(q.Body) {
				t.Error("invalid operation envelope")
				http.Error(w, "invalid", 400)
				return
			}
			addressed = append(addressed, q.Request.Operation+":"+q.Request.SessionID)
			switch q.Request.Operation {
			case "snapshot.create":
				if q.Request.SessionID != active.SessionID || q.Request.ExpectedSessionRevision != 4 {
					t.Error("wrong snapshot binding")
				}
				var body SnapshotCreate
				_ = json.Unmarshal(q.Body, &body)
				if body.MaximumCommittedBytes != cp.CanonicalSizeBytes || body.Description != cp.Description {
					t.Error("lost metadata/allowance")
				}
				snapshotEffects++
				now := time.Now().UTC()
				savedSnapshot = OperationRecord{Request: q.Request, Fingerprint: operationFingerprint(q.Request), CommandFingerprint: operationCommandFingerprint(q.Request, q.Body), State: "completed", AdmittedAt: now, FinishedAt: &now, Response: Response{Status: 201, Body: encoded(t, cp), SessionRevision: 5}}
				send(201, cp, 5)
			case "operation.status":
				var body map[string]string
				_ = json.Unmarshal(q.Body, &body)
				if q.Request.SessionID != cp.SourceSessionID || body["operation_id"] != savedSnapshot.Request.OperationID {
					t.Error("lookup moved to replacement session")
				}
				send(200, savedSnapshot, 5)
			case "session.owner":
				if q.Request.SessionID != active.SessionID {
					t.Error("closed wrong session")
				}
				closed = true
				now := time.Now().UTC()
				send(200, Owner{Principal: "operator", CampaignID: "campaign-1", WorkerInstanceID: "worker-original", RunRevision: 1, AllowTargetStop: true, BoundAt: now, ClosedAt: &now, ClosureReason: "controller_closed_execution"}, 2)
			default:
				t.Error("unexpected operation", q.Request.Operation)
				http.Error(w, "unexpected", 400)
			}
		case "/v1/snapshots/list":
			send(200, map[string]any{"campaign_id": "campaign-1", "checkpoints": []Checkpoint{cp}, "total": 1}, 0)
		case "/v1/snapshots/inspect":
			var q map[string]string
			_ = json.Unmarshal(raw, &q)
			if q["source_session_id"] != cp.SourceSessionID || q["checkpoint_id"] != cp.ID {
				t.Error("inspect lost source handle")
			}
			send(200, cp, 0)
		case "/v1/lifecycle":
			var q LifecycleRequest
			_ = json.Unmarshal(raw, &q)
			if q.SessionID != active.SessionID {
				t.Error("stale lifecycle request")
			}
			switch q.Operation {
			case "snapshot.restore":
				restoreEffects++
				if q.CheckpointID != cp.ID || q.SourceSessionID != cp.SourceSessionID {
					t.Error("restore lost checkpoint/source")
				}
				active = Binding{"sess-new", q.WorkerInstanceID, active.RunRevision + 1}
				sessions[active.SessionID] = active
				replacement := map[string]any{}
				for k, v := range metadata {
					replacement[k] = v
				}
				replacement["id"], replacement["revision"], replacement["parent_session_id"], replacement["parent_checkpoint"] = active.SessionID, 1, cp.SourceSessionID, cp.ID
				body := map[string]any{"binding": active, "session": replacement, "source_session_id": cp.SourceSessionID, "checkpoint_id": cp.ID}
				savedRestore = LifecycleRecord{q, lifecycleFingerprint(q), "completed", Response{Status: 200, Body: encoded(t, body)}}
				// Effect/record exists; lose only the response. Reconciliation must
				// use the lifecycle ledger and never create another restore effect.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
			case "session.stop":
				stopEffects++
				closed = true
				phase = "stopped"
				send(200, StopResult{active.SessionID, "stopped"}, 0)
			default:
				t.Error("unexpected lifecycle operation")
				http.Error(w, "unexpected", 400)
			}
		case "/v1/evidence":
			var q map[string]string
			_ = json.Unmarshal(raw, &q)
			if phase != "stopped" || !closed || q["session_id"] != cp.SourceSessionID {
				t.Error("evidence requested before stop or from wrong session")
			}
			w.Header().Set("Content-Type", "application/x-tar")
			w.Header().Set("X-Content-SHA256", rawDigest(archive))
			w.Header().Set("X-Evidence-Max-Bytes", "4294967296")
			http.ServeContent(w, r, "evidence.tar", time.Time{}, bytes.NewReader(archive))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.Error(w, "unexpected", 404)
		}
	}))
	defer srv.Close()
	c := New()
	defer c.Close()
	c.http.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "127.0.0.1:8080" {
			t.Error("unexpected dial")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp4", srv.Listener.Addr().String())
	}
	ctx := context.Background()
	a, err := c.Attach(ctx, "campaign-1", "worker-2", true)
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(ctx, a.CampaignID)
	if err != nil || !status.Ready() || !status.Matches("instance-1", a.Binding) {
		t.Fatal(status, err)
	}
	q := query()
	q.Operation = "snapshot.create"
	create, err := PrepareSnapshotCreate(q, SnapshotCreate{cp.Label, cp.Description, cp.CanonicalSizeBytes})
	if err != nil {
		t.Fatal(err)
	}
	response, err := c.Execute(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := DecodeSnapshotCreated(response, create, a.Session)
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.ListSnapshots(ctx, SnapshotListRequest{CampaignID: a.CampaignID})
	if err != nil || len(page.Checkpoints) != 1 || page.Checkpoints[0] != checkpoint {
		t.Fatal(page, err)
	}
	inspected, err := c.InspectSnapshot(ctx, a.CampaignID, checkpoint.SourceSessionID, checkpoint.ID)
	if err != nil || inspected != checkpoint {
		t.Fatal(inspected, err)
	}
	restore, err := PrepareLifecycle(LifecycleRequest{WorkerInstanceID: "replacement-worker", RunRevision: 99, CampaignID: a.CampaignID, SessionID: a.Binding.SessionID, OperationID: "restore-1", Operation: "snapshot.restore", CheckpointID: checkpoint.ID, SourceSessionID: checkpoint.SourceSessionID}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ExecuteLifecycle(ctx, restore)
	assertUncertain(t, err)
	record, err := c.LifecycleStatus(ctx, restore)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeRestore(record.Response, restore, a.Binding, a.Session)
	if err != nil || restored.Binding.RunRevision != 3 {
		t.Fatal(restored, err)
	}
	// This status locates the target for cleanup; it does not reopen execution.
	status, err = c.Status(ctx, a.CampaignID)
	if err != nil || !status.Matches("instance-1", restored.Binding) {
		t.Fatal(status, err)
	}
	q = query()
	q.Operation = ""
	q.RequestID = "lookup-request"
	q.OperationID = "lookup-operation"
	q.WorkerInstanceID = "replacement-worker"
	q.RunRevision = 3
	lookup, err := PrepareOperationStatus(q, create)
	if err != nil {
		t.Fatal(err)
	}
	response, err = c.Execute(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := DecodeOperationRecord(response, create)
	if err != nil || operation.Response.Status != 201 {
		t.Fatal(operation, err)
	}
	q = query()
	q.Operation = ""
	q.SessionID = restored.Binding.SessionID
	q.WorkerInstanceID = "cleanup-worker"
	q.RunRevision = 3
	q.RequestID = "close-request"
	q.OperationID = "close-operation"
	closeRequest, err := PrepareClose(q)
	if err != nil {
		t.Fatal(err)
	}
	response, err = c.Execute(ctx, closeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeClosure(response, a.CampaignID); err != nil {
		t.Fatal(err)
	}
	stop, err := PrepareLifecycle(LifecycleRequest{WorkerInstanceID: q.WorkerInstanceID, RunRevision: 3, CampaignID: a.CampaignID, SessionID: restored.Binding.SessionID, OperationID: "stop-operation", Operation: "session.stop"}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	response, err = c.ExecuteLifecycle(ctx, stop)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStop(response, stop); err != nil {
		t.Fatal(err)
	}
	status, err = c.Status(ctx, a.CampaignID)
	if err != nil || status.Ready() || !status.Closed {
		t.Fatal(status, err)
	}
	download, err := c.DownloadEvidence(ctx, EvidenceRequest{CampaignID: a.CampaignID, SessionID: a.Binding.SessionID, MaxArchiveBytes: 4 << 30, InterceptorMaxBytes: a.EvidenceMaxBytes, Deadline: time.Now().Add(time.Minute)}, privateEvidenceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer download.Close()
	index, err := download.InspectArchive(ctx, DefaultArchiveLimits(4<<30))
	if err != nil || len(index.Entries()) != 6 {
		t.Fatal(index, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if snapshotEffects != 1 || restoreEffects != 1 || stopEffects != 1 {
		t.Fatal("effect replay", snapshotEffects, restoreEffects, stopEffects)
	}
	if len(addressed) != 3 || addressed[0] != "snapshot.create:sess-1" || addressed[1] != "operation.status:sess-1" || addressed[2] != "session.owner:sess-new" {
		t.Fatal("wrong session routing", addressed)
	}
}
