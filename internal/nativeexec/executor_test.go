package nativeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type testAdapter struct {
	authorize func(interceptor.PreparedOperation) error
	interpret func(interceptor.PreparedOperation, interceptor.Response) (Outcome, error)
}

func (a testAdapter) Authorize(p interceptor.PreparedOperation) error {
	if a.authorize != nil {
		return a.authorize(p)
	}
	return nil
}
func (a testAdapter) Interpret(p interceptor.PreparedOperation, r interceptor.Response) (Outcome, error) {
	if a.interpret != nil {
		return a.interpret(p, r)
	}
	if r.Status == 200 {
		return Succeeded, nil
	}
	return Failed, nil
}

type testRuntime struct {
	check func(context.Context, campaign.DockerBinding) error
}

func (r testRuntime) CheckRunning(ctx context.Context, b campaign.DockerBinding) error {
	if r.check != nil {
		return r.check(ctx, b)
	}
	return nil
}

type testPeer struct {
	mu          sync.Mutex
	status      interceptor.Status
	calls       []interceptor.PreparedOperation
	send        func(context.Context, interceptor.PreparedOperation) (interceptor.Response, error)
	statusError error
}

func (p *testPeer) Status(context.Context, string) (interceptor.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, p.statusError
}
func (p *testPeer) Execute(ctx context.Context, q interceptor.PreparedOperation) (interceptor.Response, error) {
	p.mu.Lock()
	p.calls = append(p.calls, q)
	send := p.send
	p.mu.Unlock()
	if send != nil {
		return send(ctx, q)
	}
	return interceptor.ParseResponse([]byte(`{"status":200,"session_revision":4,"body":{"ok":true}}`))
}
func (p *testPeer) count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) }

func setup(t *testing.T) (string, *campaign.Writer, *campaign.Attempts, *testPeer, *Guard, *Executor) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	d := contracts.RawDigest([]byte("fixture"))
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 1, CreatedAt: "2026-09-25T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1", ImageDigest: d, ReleaseRecordDigest: d, Contract: campaign.ContractPin{Version: "0.1.0", Digest: d, CatalogDigest: d, OperationsDigest: d}, EngineContextDigest: d, InputTreeDigest: d, SkillSetDigest: d, ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d, Target: campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: "session-1", WorkerInstanceID: "worker-1", NativeFeedbackProfile: "diagnostic", CapabilitySourceDigest: d, CapabilityProjectionDigest: d}, RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`), HarnessLimits: json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`), Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 128 << 20, MaxSegmentBytes: campaign.MaxEventBytes}}
	w, err := campaign.Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err = w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	a, err := campaign.NewAttempts(w, 0)
	if err != nil {
		t.Fatal(err)
	}
	in := campaign.AttemptInput{CampaignID: "campaign-1", WorkerInstanceID: "worker-1", RunRevision: 1, Body: []byte(`{"request_id":"parent","attempt_id":"attempt-1","attempt_index":1}`)}
	if _, _, err = a.Observe(in); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Admit("parent", []byte(`{"authorized_steps":["register","invoke","observe","cleanup"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.MarkDispatched("parent"); err != nil {
		t.Fatal(err)
	}
	b := interceptor.Binding{SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1}
	p := &testPeer{status: interceptor.Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: b, Sessions: map[string]interceptor.Binding{b.SessionID: b}, Phase: "ready", StoreAvailable: true}}
	docker := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	if err = w.SaveDockerBinding(docker); err != nil {
		t.Fatal(err)
	}
	guard, err := NewGuard(p, testRuntime{}, docker, "instance-1", b)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(a.NativeSteps(), guard, testAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	return root, w, a, p, guard, e
}
func request(t *testing.T, id, operation string) interceptor.PreparedOperation {
	t.Helper()
	p, err := interceptor.PrepareOperation(interceptor.OperationRequest{RequestID: "request-" + id, OperationID: id, Operation: operation, CampaignID: "campaign-1", SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1, AttemptID: "attempt-1", Deadline: time.Now().Add(time.Minute)}, []byte(fmt.Sprintf(`{ "step": %q, "literal": "<>&" }`, id)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func complete(t *testing.T, a *campaign.Attempts, outcome string) {
	t.Helper()
	if err := a.Resolve("parent", campaign.AttemptCompletion{Outcome: outcome, Result: []byte(`{"fixture":true}`), Receipts: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMultiStepPlanAndReplay(t *testing.T) {
	root, w, a, peer, _, e := setup(t)
	requests := []interceptor.PreparedOperation{request(t, "register", "attempt.register"), request(t, "invoke", "application.invoke"), request(t, "observe", "observation.read"), request(t, "cleanup", "injection.delete")}
	for _, p := range requests {
		got, err := e.Execute(context.Background(), "parent", p)
		if err != nil || got.Replay || got.Step.Outcome != "succeeded" {
			t.Fatal(got, err)
		}
		duplicate, err := e.Execute(context.Background(), "parent", p)
		if err != nil || !duplicate.Replay || !bytes.Equal(duplicate.Native.Bytes(), got.Native.Bytes()) {
			t.Fatal("replay changed result", err)
		}
	}
	if peer.count() != 4 {
		t.Fatal("repeated native effects", peer.count())
	}
	complete(t, a, "succeeded")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	preflights := 0
	report, err := campaign.Inspect(root, "campaign-1", func(event campaign.Event) error {
		if event.Kind == "native.preflight" {
			preflights++
		}
		return nil
	})
	if err != nil || len(report.Reservations) != 0 || preflights != 4 {
		t.Fatal(report, preflights, err)
	}
	for _, op := range report.Operations {
		if op.Outcome != campaign.ResultCommitted {
			t.Fatal(op)
		}
	}
}
func TestGuardAndAuthorizationBlockContact(t *testing.T) {
	for name, change := range map[string]func(*testPeer, *Guard, *Executor){
		"instance": func(p *testPeer, g *Guard, e *Executor) { p.status.InstanceID = "different" },
		"session":  func(p *testPeer, g *Guard, e *Executor) { p.status.Active.SessionID = "different" },
		"revision": func(p *testPeer, g *Guard, e *Executor) { p.status.Active.RunRevision++ },
		"campaign": func(p *testPeer, g *Guard, e *Executor) { p.status.CampaignID = "different" },
		"closed":   func(p *testPeer, g *Guard, e *Executor) { p.status.Closed = true },
		"failure": func(p *testPeer, g *Guard, e *Executor) {
			p.status.Failure = &interceptor.Failure{Reason: "WALL_TIME_LIMIT"}
		},
		"store":              func(p *testPeer, g *Guard, e *Executor) { p.status.StoreAvailable = false },
		"status unavailable": func(p *testPeer, g *Guard, e *Executor) { p.statusError = errors.New("lost status") },
		"docker": func(p *testPeer, g *Guard, e *Executor) {
			g.runtime = testRuntime{check: func(context.Context, campaign.DockerBinding) error { return errors.New("not running") }}
		},
		"unauthorized": func(p *testPeer, g *Guard, e *Executor) {
			e.adapter = testAdapter{authorize: func(interceptor.PreparedOperation) error { return errors.New("not in plan") }}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, w, _, peer, g, e := setup(t)
			change(peer, g, e)
			result, err := e.Execute(context.Background(), "parent", request(t, "invoke", "application.invoke"))
			if !errors.Is(err, ErrFailed) || result.Step.Outcome != "not_dispatched" || peer.count() != 0 || w.Fence().Err() == nil {
				t.Fatal(result, err, peer.count())
			}
		})
	}
}
func TestUncertainAndFailedNativeResponses(t *testing.T) {
	cases := []struct {
		name, wire string
		sendErr    error
		interpret  func(interceptor.PreparedOperation, interceptor.Response) (Outcome, error)
		want       string
	}{
		{name: "lost reply", sendErr: &interceptor.CallError{Kind: "transport_unavailable", Uncertain: true}, want: "unknown"},
		{name: "locally unsent", sendErr: &interceptor.CallError{Kind: "canceled_before_dispatch"}, want: "not_dispatched"},
		{name: "202", wire: `{"status":202,"session_revision":4,"body":{"code":"operation_in_progress"}}`, want: "unknown"},
		{name: "503", wire: `{"status":503,"session_revision":4,"body":{"code":"execution_store_unavailable"}}`, want: "unknown"},
		{name: "unknown marker", wire: `{"status":409,"session_revision":4,"body":{"code":"outcome_unknown"}}`, want: "unknown"},
		{name: "known failure", wire: `{"status":409,"session_revision":4,"body":{"code":"application_failed"}}`, want: "failed"},
		{name: "invalid receipt", wire: `{"status":200,"session_revision":4,"body":{}}`, interpret: func(interceptor.PreparedOperation, interceptor.Response) (Outcome, error) {
			return Succeeded, errors.New("missing receipt")
		}, want: "unknown"},
		{name: "success interpretation of rejection", wire: `{"status":403,"session_revision":4,"body":{"code":"campaign_mismatch"}}`, interpret: func(interceptor.PreparedOperation, interceptor.Response) (Outcome, error) { return Succeeded, nil }, want: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, w, a, peer, _, e := setup(t)
			e.adapter = testAdapter{interpret: tc.interpret}
			peer.send = func(context.Context, interceptor.PreparedOperation) (interceptor.Response, error) {
				if tc.sendErr != nil {
					return interceptor.Response{}, tc.sendErr
				}
				return interceptor.ParseResponse([]byte(tc.wire))
			}
			p := request(t, "invoke", "application.invoke")
			r, err := e.Execute(context.Background(), "parent", p)
			if err == nil || r.Step.Outcome != tc.want || w.Fence().Err() == nil {
				t.Fatal(r, err)
			}
			replay, err := e.Execute(context.Background(), "parent", p)
			if err == nil || !replay.Replay || peer.count() != 1 {
				t.Fatal("uncertain replay repeated effect", replay, err)
			}
			if _, err = e.Execute(context.Background(), "parent", request(t, "next", "application.invoke")); err == nil || peer.count() != 1 {
				t.Fatal("continued after terminal step")
			}
			outcome := "failed"
			if tc.want == "unknown" {
				outcome = "unknown"
			}
			complete(t, a, outcome)
		})
	}
}
func TestConcurrentDuplicateAndFenceCancellation(t *testing.T) {
	_, w, _, peer, _, e := setup(t)
	entered := make(chan struct{})
	peer.send = func(ctx context.Context, p interceptor.PreparedOperation) (interceptor.Response, error) {
		close(entered)
		<-ctx.Done()
		return interceptor.Response{}, &interceptor.CallError{Kind: "transport_unavailable", Uncertain: true}
	}
	p := request(t, "invoke", "application.invoke")
	done := make(chan error, 1)
	go func() { _, err := e.Execute(context.Background(), "parent", p); done <- err }()
	<-entered
	duplicate, err := e.Execute(context.Background(), "parent", p)
	if !errors.Is(err, ErrPending) || !duplicate.Replay {
		t.Fatal(duplicate, err)
	}
	w.Fence().Stop(campaign.ErrClosed)
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fence did not cancel in-flight native call")
	}
	if peer.count() != 1 {
		t.Fatal("duplicate dispatch")
	}
}

func TestExecutorRejectsReplacementDockerBinding(t *testing.T) {
	_, _, a, peer, guard, _ := setup(t)
	for _, change := range []func(*campaign.DockerBinding){func(b *campaign.DockerBinding) { b.DockerContainerID = strings.Repeat("c", 64) }, func(b *campaign.DockerBinding) { b.Endpoint = "unix:///other/docker.sock" }, func(b *campaign.DockerBinding) { b.DaemonID = "other-daemon" }} {
		b := guard.docker
		change(&b)
		g, err := NewGuard(peer, testRuntime{}, b, guard.instance, guard.binding)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = New(a.NativeSteps(), g, testAdapter{}); err == nil {
			t.Fatal("accepted replacement for saved Docker identity")
		}
	}
	if peer.count() != 0 {
		t.Fatal("binding rejection contacted target")
	}
}
