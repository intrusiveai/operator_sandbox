package attemptadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/feedback"
	"github.com/intrusive-ai/operator-sandbox/internal/nativeexec"
	"github.com/intrusive-ai/operator-sandbox/schemas"
)

func brokerFixture(t *testing.T) (*Broker, []byte, *nativePeer) {
	t.Helper()
	c, raw, in := fixture(t)
	raw = mutate(raw, func(m map[string]any) { m["cleanup"] = map[string]bool{"delete_actions_after_observation": false} })
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	_, w, a, peer := executionSetup(t, raw, p, true)
	protocol, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	makeGuard := func() *nativeexec.Guard {
		m := w.Manifest()
		docker := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
		g, err := nativeexec.NewGuard(peer, runtimeStub{}, docker, "instance-1", peer.plan.binding)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	b, err := NewBroker(BrokerConfig{Catalog: c, Protocol: protocol, Writer: w, Attempts: a, Deadline: time.Now().Add(5 * time.Minute), Admitted: func() bool { return true }, ReadKinds: func() []string { return []string{"target_output"} }, Prepare: func(_ context.Context, body []byte, deadline time.Time) (*Plan, *nativeexec.Guard, error) {
		in.CreatedAt = time.Now()
		in.Deadline = deadline
		in.SessionRevision = peer.revision
		plan, err := Compile(c, body, in)
		if err != nil {
			return nil, nil, err
		}
		peer.plan = plan
		return plan, makeGuard(), nil
	}, Cleanup: func(_ context.Context, _ time.Time) (CleanupTarget, error) {
		return CleanupTarget{Guard: makeGuard(), Binding: peer.plan.binding, SessionRevision: peer.revision}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return b, raw, peer
}
func wire(b *Broker, id, op string, body []byte) []byte {
	return encode(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 1, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": b.config.Attempts.Status().RunRevision, "call_id": "call-" + id, "operation_id": id, "operation": op, "timeout_ms": 30000, "body": json.RawMessage(body)})
}
func call(t *testing.T, b *Broker, id, op string, body []byte) reply {
	t.Helper()
	q := wire(b, id, op, body)
	raw, err := b.Handle(context.Background(), q, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.config.Protocol.ValidateResponse(q, raw); err != nil {
		t.Fatal(err)
	}
	var r reply
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func receipt(t *testing.T, r reply) (string, string) {
	t.Helper()
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	var v struct {
		Receipt  string            `json:"receipt_id"`
		Feedback feedback.Manifest `json:"feedback"`
	}
	_ = json.Unmarshal(r.Result, &v)
	if len(v.Feedback.Entries) != 1 {
		t.Fatal("missing feedback", string(r.Result))
	}
	return v.Receipt, v.Feedback.Entries[0].ID
}
func TestBrokerAttemptReadCleanupAndRestore(t *testing.T) {
	b, raw, peer := brokerFixture(t)
	first := call(t, b, "request-1", "engine.attempt_execute", raw)
	id, entry := receipt(t, first)
	saved, result, err := b.config.Attempts.Lookup("request-1")
	if err != nil || saved.Publication == nil || !bytes.Equal(result, first.Result) {
		t.Fatal("reply not retained", err)
	}
	count := len(peer.calls)
	repeat := call(t, b, "request-1", "engine.attempt_execute", raw)
	if !bytes.Equal(first.Result, repeat.Result) || len(peer.calls) != count {
		t.Fatal("attempt repeated")
	}
	query := encode(map[string]any{"receipt_id": id, "entry_id": entry, "offset": 0, "max_bytes": 100})
	read := call(t, b, "read-1", "engine.observation_read", query)
	if read.Error != nil {
		t.Fatal(read.Error)
	}
	var chunk feedback.ReadResult
	_ = json.Unmarshal(read.Result, &chunk)
	if !bytes.Equal(chunk.Content, peer.output) || !chunk.EOF {
		t.Fatal("wrong bytes")
	}
	usage := b.config.Attempts.Tools().Usage()
	if usage.Requests != 1 || usage.Bytes != int64(len(peer.output)) || usage.Reserved != 0 {
		t.Fatal(usage)
	}
	call(t, b, "read-1", "engine.observation_read", query)
	if b.config.Attempts.Tools().Usage() != usage {
		t.Fatal("transport replay charged again")
	}
	call(t, b, "read-2", "engine.observation_read", query)
	if b.config.Attempts.Tools().Usage().Bytes != 2*usage.Bytes {
		t.Fatal("new overlapping read not charged")
	}
	cleanup := encode(map[string]string{"attempt_receipt_id": id, "action_id": "action-1"})
	deleted := call(t, b, "delete-1", "engine.injection_delete", cleanup)
	if deleted.Error != nil || !bytes.Contains(deleted.Result, []byte(`"deleted"`)) {
		t.Fatal(deleted)
	}
	count = len(peer.calls)
	if !bytes.Equal(call(t, b, "delete-1", "engine.injection_delete", cleanup).Result, deleted.Result) || len(peer.calls) != count {
		t.Fatal("cleanup repeated")
	}
	// Restore retains the harness and its historical receipt; cleanup targets replacement.
	target := b.config.Attempts.Target()
	target.SessionID = "replacement"
	target.WorkerInstanceID = "new-worker"
	if err = b.Rebind(2, target); err != nil {
		t.Fatal(err)
	}
	peer.plan.binding.SessionID = target.SessionID
	peer.plan.binding.WorkerInstanceID = target.WorkerInstanceID
	peer.plan.binding.RunRevision = 2
	if !bytes.Equal(call(t, b, "delete-1", "engine.injection_delete", cleanup).Result, deleted.Result) || len(peer.calls) != count {
		t.Fatal("old cleanup applied to replacement")
	}
	again := call(t, b, "delete-2", "engine.injection_delete", cleanup)
	if again.Error != nil || len(peer.calls) != count+1 {
		t.Fatal("restored injection not removed", again)
	}
	peer.absent = true
	absent := call(t, b, "delete-3", "engine.injection_delete", cleanup)
	if absent.Error != nil || !bytes.Contains(absent.Result, []byte(`"already_absent"`)) {
		t.Fatalf("absence result: %s, error: %+v", absent.Result, absent.Error)
	}
	old := call(t, b, "read-3", "engine.observation_read", query)
	if !bytes.Equal(old.Result, read.Result) {
		t.Fatal("old source changed")
	}
	if b.config.Attempts.Status().Admissions != 1 {
		t.Fatal("nonattempt admitted an attempt")
	}
	b.config.ReadKinds = func() []string { return nil }
	denied := call(t, b, "read-1", "engine.observation_read", query)
	if denied.Error == nil || denied.Error.Code != "OBSERVATION_NOT_PERMITTED" {
		t.Fatal("cached bytes bypassed narrowing")
	}
}
func TestBrokerRejectionConsumesIndexAndNeverContactsTarget(t *testing.T) {
	b, raw, peer := brokerFixture(t)
	bad := mutate(raw, func(m map[string]any) { m["invocation"].(map[string]any)["operation_id"] = "forbidden" })
	rejected := call(t, b, "request-1", "engine.attempt_execute", bad)
	if rejected.Error == nil || len(peer.calls) != 0 || b.config.Attempts.Status().HighWatermark != 1 || b.config.Attempts.Status().Admissions != 0 {
		t.Fatal("bad rejection accounting")
	}
	replay := call(t, b, "request-1", "engine.attempt_execute", bad)
	if replay.Error == nil || replay.Error.Code != rejected.Error.Code {
		t.Fatal("rejection not replayed")
	}
	conflict := call(t, b, "request-1", "engine.attempt_execute", raw)
	if conflict.Error == nil || conflict.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed command accepted")
	}
	next := mutate(raw, func(m map[string]any) {
		m["request_id"] = "request-2"
		m["attempt_id"] = "attempt-2"
		m["attempt_index"] = 2
	})
	receipt(t, call(t, b, "request-2", "engine.attempt_execute", next))
	if b.config.Attempts.Status().HighWatermark != 2 || b.config.Attempts.Status().Admissions != 1 {
		t.Fatal("next index rejected")
	}
}
func TestBrokerReadBoundsDenialsAndBudget(t *testing.T) {
	b, raw, peer := brokerFixture(t)
	id, entry := receipt(t, call(t, b, "request-1", "engine.attempt_execute", raw))
	count := len(peer.calls)
	cases := []struct {
		id, receipt, entry string
		offset, max        int
		code               string
	}{{"foreign", "foreign", entry, 0, 10, "OBSERVATION_NOT_FOUND"}, {"entry", id, "missing", 0, 10, "OBSERVATION_NOT_FOUND"}, {"range", id, entry, 999, 10, "OBSERVATION_RANGE_INVALID"}, {"budget", id, entry, 0, 10001, "FEEDBACK_BUDGET_EXCEEDED"}}
	for _, tc := range cases {
		r := call(t, b, tc.id, "engine.observation_read", encode(map[string]any{"receipt_id": tc.receipt, "entry_id": tc.entry, "offset": tc.offset, "max_bytes": tc.max}))
		if r.Error == nil || r.Error.Code != tc.code {
			t.Fatal(tc.id, r)
		}
	}
	if len(peer.calls) != count || b.config.Attempts.Tools().Usage().Bytes != 0 || b.config.Attempts.Tools().Usage().Reserved != 0 {
		t.Fatal("denied read accessed target or charged bytes")
	}
	missing := call(t, b, "no-action", "engine.injection_delete", encode(map[string]string{"attempt_receipt_id": id, "action_id": "missing"}))
	if missing.Error == nil || missing.Error.Code != "ACTION_UNAVAILABLE" || len(peer.calls) != count {
		t.Fatal("guessed cleanup dispatched")
	}
}
func TestBrokerUnknownCleanupClosesBeforeAnotherRequest(t *testing.T) {
	b, raw, peer := brokerFixture(t)
	id, _ := receipt(t, call(t, b, "request-1", "engine.attempt_execute", raw))
	peer.lostCleanup = true
	r := call(t, b, "delete-1", "engine.injection_delete", encode(map[string]string{"attempt_receipt_id": id, "action_id": "action-1"}))
	if r.Error == nil || r.Error.Code != "OUTCOME_UNKNOWN" || b.config.Writer.Fence().Err() == nil {
		t.Fatal(r)
	}
	count := len(peer.calls)
	if _, err := b.Handle(context.Background(), wire(b, "delete-2", "engine.injection_delete", encode(map[string]string{"attempt_receipt_id": id, "action_id": "action-1"})), 2); err == nil || len(peer.calls) != count {
		t.Fatal("execution reopened")
	}
	_, saved, err := b.config.Attempts.Tools().Lookup("delete-1")
	if err != nil || !bytes.Contains(saved, []byte("OUTCOME_UNKNOWN")) {
		t.Fatal("unknown not retained", err)
	}
}
func TestBrokerDuplicateConcurrencyAndClosedAdmission(t *testing.T) {
	b, raw, peer := brokerFixture(t)
	q := wire(b, "request-1", "engine.attempt_execute", raw)
	results := make(chan []byte, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := b.Handle(context.Background(), q, 1); results <- r; failures <- err }()
	}
	wg.Wait()
	if err := errors.Join(<-failures, <-failures); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(<-results, <-results) || b.config.Attempts.Status().Admissions != 1 {
		t.Fatal("concurrent replay repeated")
	}
	count := len(peer.calls)
	b.config.Admitted = func() bool { return false }
	if _, err := b.Handle(context.Background(), q, 1); err == nil || len(peer.calls) != count {
		t.Fatal("unadmitted channel read result")
	}
}

func TestBrokerNativeCleanupFailuresNeverBecomeAbsence(t *testing.T) {
	for _, mode := range []string{"storage", "malformed", "lost", "journal"} {
		t.Run(mode, func(t *testing.T) {
			b, raw, peer := brokerFixture(t)
			id, _ := receipt(t, call(t, b, "request-1", "engine.attempt_execute", raw))
			switch mode {
			case "storage":
				peer.fail = "injection.delete"
			case "malformed":
				peer.malformed = "injection.delete"
			case "lost":
				peer.lostCleanup = true
			case "journal":
				peer.after = func(op string) {
					if op == "injection.delete" {
						_ = b.config.Writer.Close()
					}
				}
			}
			q := wire(b, "cleanup-failed", "engine.injection_delete", encode(map[string]string{"attempt_receipt_id": id, "action_id": "action-1"}))
			result, err := b.Handle(context.Background(), q, 1)
			if mode == "journal" {
				if err == nil || len(result) != 0 {
					t.Fatal("storage loss returned a reply")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var r reply
				_ = json.Unmarshal(result, &r)
				if r.Error == nil || r.Error.Disposition != "terminate" {
					t.Fatal("failure became success")
				}
			}
			if b.config.Writer.Fence().Err() == nil {
				t.Fatal("cleanup failure did not close execution")
			}
		})
	}
}

func TestBrokerPublicationFailureCannotExposeResult(t *testing.T) {
	b, raw, peer := brokerFixture(t)
	peer.after = func(op string) {
		if op == "observation.content.read" {
			_ = b.config.Writer.Close()
		}
	}
	result, err := b.Handle(context.Background(), wire(b, "request-1", "engine.attempt_execute", raw), 1)
	if err == nil || len(result) != 0 || b.config.Writer.Fence().Err() == nil {
		t.Fatal("publication failure exposed result")
	}
	if _, err := b.config.Attempts.FindPublication(opaque("receipt", "campaign-1", "attempt-1")); err == nil {
		t.Fatal("uncommitted receipt readable")
	}
}

func TestBrokerTerminalAttemptsPublishConfirmedHandles(t *testing.T) {
	for _, op := range []string{"injection.arm", "application.invoke", "observation.read"} {
		t.Run(op, func(t *testing.T) {
			b, raw, peer := brokerFixture(t)
			peer.fail = op
			r := call(t, b, "request-1", "engine.attempt_execute", raw)
			if r.Error != nil {
				t.Fatal(r.Error)
			}
			var result struct {
				Status  string `json:"status"`
				Receipt string `json:"receipt_id"`
			}
			_ = json.Unmarshal(r.Result, &result)
			if result.Status != "failed" && result.Status != "unknown" {
				t.Fatal("failed attempt reported success")
			}
			index, err := b.config.Attempts.FindPublication(result.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if op == "injection.arm" {
				expected = 0
			}
			if len(index.Injections) != expected {
				t.Fatal("unconfirmed handle or lost confirmed handle")
			}
			count := len(peer.calls)
			if _, err = b.Handle(context.Background(), wire(b, "request-1", "engine.attempt_execute", raw), 2); err == nil || len(peer.calls) != count {
				t.Fatal("terminal attempt reopened")
			}
		})
	}
}
