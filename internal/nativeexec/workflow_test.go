package nativeexec

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/dockercontrol"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/termination"
)

func statusQuery(t *testing.T, p interceptor.PreparedOperation) interceptor.PreparedOperation {
	t.Helper()
	q := p.Request()
	q.RequestID = "report-request"
	q.OperationID = "report-query"
	q.Operation = "operation.status"
	q.AttemptID = ""
	q.AttemptContextDigest = ""
	q.BodyDigest = ""
	result, err := interceptor.PrepareOperationStatus(q, p)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func recordResponse(t *testing.T, original interceptor.PreparedOperation) interceptor.Response {
	t.Helper()
	now := time.Now().UTC()
	response, err := interceptor.ParseResponse([]byte(`{"status":200,"session_revision":4,"body":{"receipt_id":"native-receipt-1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	rawRequest, err := json.Marshal(original.Request())
	if err != nil {
		t.Fatal(err)
	}
	record := interceptor.OperationRecord{Request: original.Request(), Fingerprint: contracts.RawDigest(rawRequest), CommandFingerprint: original.CommandFingerprint(), State: "completed", Response: response, AdmittedAt: now, FinishedAt: &now}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(interceptor.Response{Status: 200, SessionRevision: 4, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	result, err := interceptor.ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type watcherKiller struct{ calls chan campaign.DockerBinding }

func (k watcherKiller) Terminate(ctx context.Context, b campaign.DockerBinding) dockercontrol.Outcome {
	if ctx.Err() != nil {
		return dockercontrol.Outcome{Code: "canceled"}
	}
	k.calls <- b
	return dockercontrol.Outcome{Confirmed: true, KillAttempted: true, State: "exited", Code: "confirmed_stopped"}
}
func TestLostReplyReconciliationAndIndependentTermination(t *testing.T) {
	root, w, a, peer, guard, e := setup(t)
	original := request(t, "invoke", "application.invoke")
	// The native peer commits its result, then loses the delivery reply. Its native
	// ledger knows completion; Operator must still end this campaign's execution.
	nativeRecord := recordResponse(t, original)
	effects := 0
	queries := 0
	peer.send = func(ctx context.Context, p interceptor.PreparedOperation) (interceptor.Response, error) {
		if p.Request().Operation == "operation.status" {
			queries++
			if p.Request().SessionID != original.Request().SessionID {
				t.Error("query redirected to another session")
			}
			return nativeRecord, nil
		}
		effects++
		return interceptor.Response{}, &interceptor.CallError{Kind: "transport_unavailable", Uncertain: true}
	}
	killed := make(chan campaign.DockerBinding, 1)
	terminated := make(chan termination.Receipt, 1)
	service := termination.New(watcherKiller{killed})
	go func() { terminated <- service.Watch(context.Background(), w.Fence(), root, guard.docker) }()
	result, err := e.Execute(context.Background(), "parent", original)
	if !errors.Is(err, ErrUnknown) || result.Step.Outcome != "unknown" {
		t.Fatal(result, err)
	}
	select {
	case b := <-killed:
		if b.DockerContainerID != guard.docker.DockerContainerID || b.Endpoint != guard.docker.Endpoint {
			t.Fatal("wrong independent kill binding")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unknown outcome did not trigger independent termination")
	}
	select {
	case receipt := <-terminated:
		if !receipt.Outcome.Confirmed {
			t.Fatal(receipt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("termination observer did not finish")
	}
	complete(t, a, "unknown")
	// Closed target and destroyed harness still permit a read of retained native
	// records. Checking harness readiness here would prevent useful reconciliation.
	peer.status.Closed = true
	peer.status.Phase = "error"
	guard.runtime = testRuntime{check: func(context.Context, campaign.DockerBinding) error {
		t.Error("reporting required a running harness")
		return ErrBinding
	}}
	query := statusQuery(t, original)
	record, err := e.Reconcile(context.Background(), result.Step.ID, query)
	if err != nil || record.State != "completed" || record.Response.Status != 200 {
		t.Fatal(record, err)
	}
	if _, err = e.Reconcile(context.Background(), result.Step.ID, query); err != nil {
		t.Fatal(err)
	}
	if effects != 1 || queries != 1 {
		t.Fatal("native effect/query repeated", effects, queries)
	}
	final, _, _, err := a.NativeSteps().Lookup(result.Step.ID)
	if err != nil || final.Outcome != "unknown" || w.Fence().Err() == nil {
		t.Fatal("reconciliation reopened execution", final, err)
	}
	if _, err = e.Execute(context.Background(), "parent", request(t, "new-invoke", "application.invoke")); err == nil || effects != 1 {
		t.Fatal("resumed execution")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := campaign.Inspect(root, "campaign-1", nil)
	if err != nil || !report.JournalIntact || len(report.Reservations) != 0 {
		t.Fatal(report, err)
	}
	for _, op := range report.Operations {
		if op.Outcome != campaign.Unknown {
			t.Fatal("recovery promoted uncertain work", op)
		}
	}
}

func TestReconciliationRejectsForeignRecordAndNeverRetries(t *testing.T) {
	for _, scenario := range []string{"foreign record", "lost read", "wrong instance", "missing operation"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, _, peer, _, e := setup(t)
			original := request(t, "invoke", "application.invoke")
			peer.send = func(context.Context, interceptor.PreparedOperation) (interceptor.Response, error) {
				return interceptor.Response{}, &interceptor.CallError{Kind: "transport_unavailable", Uncertain: true}
			}
			result, err := e.Execute(context.Background(), "parent", original)
			if !errors.Is(err, ErrUnknown) {
				t.Fatal(err)
			}
			peer.send = func(ctx context.Context, p interceptor.PreparedOperation) (interceptor.Response, error) {
				switch scenario {
				case "foreign record":
					return recordResponse(t, request(t, "other", "application.invoke")), nil
				case "lost read":
					return interceptor.Response{}, &interceptor.CallError{Kind: "transport_unavailable", Uncertain: true}
				case "missing operation":
					return interceptor.ParseResponse([]byte(`{"status":404,"session_revision":4,"body":{"code":"operation_not_recorded_outcome_unknown"}}`))
				default:
					return recordResponse(t, original), nil
				}
			}
			if scenario == "wrong instance" {
				peer.status.InstanceID = "different"
			}
			query := statusQuery(t, original)
			if _, err = e.Reconcile(context.Background(), result.Step.ID, query); err == nil {
				t.Fatal("accepted unverified record")
			}
			calls := peer.count()
			if _, err = e.Reconcile(context.Background(), result.Step.ID, query); err == nil {
				t.Fatal("replay invented evidence")
			}
			if peer.count() != calls {
				t.Fatal("retried reporting read")
			}
			if scenario == "wrong instance" && calls != 1 {
				t.Fatal("contacted replacement instance")
			}
		})
	}
}

func TestVerifiedRestoreRebindRetainsOriginalStepAttribution(t *testing.T) {
	root, w, a, peer, guard, e := setup(t)
	old := request(t, "invoke", "application.invoke")
	first, err := e.Execute(context.Background(), "parent", old)
	if err != nil {
		t.Fatal(err)
	}
	complete(t, a, "succeeded")
	// Lifecycle restore/lineage verification is performed by the existing native
	// lifecycle adapter. Supply its accepted replacement to both ledger and guard.
	saved, _, err := a.Lookup("parent")
	if err != nil {
		t.Fatal(err)
	}
	target := saved.Target
	target.SessionID = "session-2"
	target.WorkerInstanceID = "worker-2"
	if err = a.Rebind(2, target); err != nil {
		t.Fatal(err)
	}
	binding := interceptor.Binding{SessionID: "session-2", WorkerInstanceID: "worker-2", RunRevision: 2}
	peer.status.Active = binding
	peer.status.Sessions[binding.SessionID] = binding
	nextGuard, err := NewGuard(peer, testRuntime{}, guard.docker, guard.instance, binding)
	if err != nil {
		t.Fatal(err)
	}
	next, err := New(a.NativeSteps(), nextGuard, testAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := next.Execute(context.Background(), "parent", old); err != nil || !result.Replay || result.Step.ID != first.Step.ID || result.Step.WorkerInstanceID != "worker-1" || peer.count() != 1 {
		t.Fatal("restore changed old step replay", result, err)
	}
	input := campaign.AttemptInput{CampaignID: "campaign-1", WorkerInstanceID: "worker-2", RunRevision: 2, Body: []byte(`{"request_id":"second","attempt_id":"attempt-2","attempt_index":2}`)}
	if _, _, err = a.Observe(input); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Admit("second", []byte(`{"operation":"application.invoke"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.MarkDispatched("second"); err != nil {
		t.Fatal(err)
	}
	q := old.Request()
	q.SessionID = binding.SessionID
	q.WorkerInstanceID = binding.WorkerInstanceID
	q.RunRevision = binding.RunRevision
	q.AttemptID = "attempt-2"
	q.BodyDigest = ""
	prepared, err := interceptor.PrepareOperation(q, []byte(`{"replacement":true}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := next.Execute(context.Background(), "second", prepared)
	if err != nil || second.Step.ID == first.Step.ID || second.Step.SessionID != "session-2" || peer.count() != 2 {
		t.Fatal(second, err)
	}
	if err = a.Resolve("second", campaign.AttemptCompletion{Outcome: "succeeded", Result: []byte(`{"fixture":true}`), Receipts: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if status := a.Status(); status.Admissions != 2 || status.HighWatermark != 2 {
		t.Fatal("restore reset accounting", status)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := campaign.Inspect(root, "campaign-1", nil)
	if err != nil || len(report.Operations) != 4 || len(report.Reservations) != 0 {
		t.Fatal(report, err)
	}
}

func TestStopDuringLiveCheckPreventsEffect(t *testing.T) {
	_, w, _, peer, guard, e := setup(t)
	guard.runtime = testRuntime{check: func(context.Context, campaign.DockerBinding) error { w.Fence().Stop(campaign.ErrClosed); return nil }}
	result, err := e.Execute(context.Background(), "parent", request(t, "invoke", "application.invoke"))
	if !errors.Is(err, ErrFailed) || result.Step.Outcome != "not_dispatched" || peer.count() != 0 {
		t.Fatal(result, err)
	}
}

func TestConcurrentDifferentStepsCannotOverlap(t *testing.T) {
	_, _, _, peer, _, e := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	peer.send = func(context.Context, interceptor.PreparedOperation) (interceptor.Response, error) {
		once.Do(func() { close(entered) })
		<-release
		return interceptor.ParseResponse([]byte(`{"status":200,"session_revision":4,"body":{"ok":true}}`))
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.Execute(context.Background(), "parent", request(t, "first", "application.invoke"))
		done <- err
	}()
	<-entered
	if _, err := e.Execute(context.Background(), "parent", request(t, "second", "application.invoke")); !errors.Is(err, campaign.ErrActive) {
		t.Fatal("overlapping plan steps", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peer.count() != 1 {
		t.Fatal("overlapping effects")
	}
}
