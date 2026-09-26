package preparation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/attemptadapter"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/campaignservice"
	"github.com/intrusive-ai/operator-sandbox/internal/dockercontrol"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/preparation"
	"github.com/intrusive-ai/operator-sandbox/internal/targetprofile"
	"github.com/intrusive-ai/operator-sandbox/internal/transport"
)

type peer struct {
	statusSample        *delayedStatus
	mu                  sync.Mutex
	input               preparation.Input
	calls               []string
	revision            uint64
	closed              bool
	unknownClose        bool
	unknownDelete       bool
	checkpoints         []interceptor.Checkpoint
	contexts            map[string]interceptor.AttemptContext
	checkpointContexts  map[string]map[string]interceptor.AttemptContext
	allowances          []int64
	restoreMode         string
	restores            int
	block               chan struct{}
	entered             chan struct{}
	restoreEntered      chan struct{}
	restoreRelease      chan struct{}
	beforeRestoreResult func()
}

func (p *peer) Status(ctx context.Context, _ string) (interceptor.Status, error) {
	p.mu.Lock()
	block, entered := p.block, p.entered
	status := p.input.Status
	sample := p.statusSample
	p.statusSample = nil
	p.mu.Unlock()
	if sample != nil {
		close(sample.entered)
		select {
		case <-ctx.Done():
			return interceptor.Status{}, ctx.Err()
		case <-sample.release:
			return sample.status, nil
		}
	}
	if block != nil {
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return interceptor.Status{}, ctx.Err()
		case <-block:
		}
	}
	return status, nil
}
func (p *peer) Execute(_ context.Context, q interceptor.PreparedOperation) (interceptor.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := q.Request()
	p.calls = append(p.calls, r.Operation)
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	_ = json.Unmarshal(q.Bytes(), &envelope)
	result := func(status int, body any) (interceptor.Response, error) {
		return interceptor.ParseResponse(encode(map[string]any{"status": status, "session_revision": p.revision, "body": body}))
	}
	now := time.Now().UTC()
	owner := interceptor.Owner{Principal: "operator", CampaignID: "campaign-1", WorkerInstanceID: "worker-1", RunRevision: p.input.Attachment.Binding.RunRevision, BoundAt: now}
	owner.AllowTargetStop = p.input.Profile.Settings().AllowTargetStop
	if p.closed {
		owner.ClosedAt = &now
		owner.ClosureReason = "controller_closed_execution"
	}
	switch r.Operation {
	case "session.status":
		session := p.input.Attachment.Session
		session.Revision = p.revision
		return result(200, map[string]any{"session": session, "owner": owner, "execution_available": !p.closed})
	case "session.owner":
		p.closed = true
		p.revision++
		if p.unknownClose {
			return interceptor.Response{}, &interceptor.CallError{Kind: "lost_reply", Uncertain: true}
		}
		owner.ClosedAt = &now
		owner.ClosureReason = "controller_closed_execution"
		return result(200, owner)
	}
	if r.ExpectedSessionRevision != p.revision {
		return result(409, map[string]string{"code": "stale_session_revision"})
	}
	p.revision++
	switch r.Operation {
	case "artifact.register":
		var body struct {
			Descriptor interceptor.ArtifactDescriptor `json:"descriptor"`
		}
		_ = json.Unmarshal(envelope.Body, &body)
		return result(201, body.Descriptor)
	case "attempt.register":
		var body interceptor.AttemptContext
		_ = json.Unmarshal(envelope.Body, &body)
		if p.contexts == nil {
			p.contexts = map[string]interceptor.AttemptContext{}
		}
		if body.ParentAttemptID == "" && body.Generation != 1 {
			return result(400, map[string]string{"code": "invalid_root_generation"})
		}
		if body.ParentAttemptID != "" {
			parent, ok := p.contexts[body.ParentAttemptID]
			if !ok || body.Generation != parent.Generation+1 {
				return result(400, map[string]string{"code": "invalid_parent"})
			}
		}
		p.contexts[body.AttemptID] = body
		return result(201, body)
	case "application.invoke":
		return result(200, interceptor.Turn{ID: fmt.Sprintf("turn-%d", p.revision), Operation: "invoke", Status: "complete", AttemptID: r.AttemptID, PayloadDigest: p.input.Artifacts[0].Descriptor.Digest, Body: []byte(`{"answer":"test"}`), MediaType: "application/json", OutputDigest: contracts.RawDigest([]byte(`{"answer":"test"}`)), Started: now, Finished: now})
	case "snapshot.create":
		return p.createCheckpoint(envelope.Body, result)
	case "injection.arm":
		var body any
		_ = json.Unmarshal(envelope.Body, &body)
		return result(201, body)
	case "injection.delete":
		if p.unknownDelete {
			return interceptor.Response{}, &interceptor.CallError{Kind: "lost_reply", Uncertain: true}
		}
		return result(204, nil)
	}
	return result(400, map[string]string{"code": "unexpected"})
}
func (p *peer) ExecuteLifecycle(ctx context.Context, q interceptor.PreparedLifecycle) (interceptor.Response, error) {
	if q.Request().Operation == "snapshot.restore" {
		p.mu.Lock()
		entered, release := p.restoreEntered, p.restoreRelease
		p.input.Status.Phase = "transitioning"
		p.mu.Unlock()
		if entered != nil {
			close(entered)
		}
		if release != nil {
			select {
			case <-ctx.Done():
				return interceptor.Response{}, ctx.Err()
			case <-release:
			}
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.input.Status.Phase = "ready"
		result, err := p.restoreCheckpoint(q)
		if p.beforeRestoreResult != nil {
			p.beforeRestoreResult()
		}
		return result, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	r := q.Request()
	if r.Operation != "session.stop" || !p.closed || !p.input.Profile.Settings().AllowTargetStop {
		return interceptor.Response{}, fmt.Errorf("unexpected lifecycle stop")
	}
	p.calls = append(p.calls, r.Operation)
	return interceptor.ParseResponse(encode(map[string]any{"status": 200, "session_revision": p.revision, "body": map[string]string{"session_id": r.SessionID, "phase": "stopped"}}))
}

type runtime struct {
	killed chan struct{}
	once   sync.Once
}

func (r *runtime) CheckRunning(context.Context, campaign.DockerBinding) error { return nil }
func (r *runtime) Terminate(context.Context, campaign.DockerBinding) dockercontrol.Outcome {
	r.once.Do(func() { close(r.killed) })
	return dockercontrol.Outcome{Confirmed: true, State: "stopped", Code: "confirmed"}
}
func serviceFixture(t *testing.T, change ...func(*preparation.Input)) (*campaignservice.Service, *peer, *runtime, *campaign.Writer, campaign.LaunchInputs) {
	return serviceWithDeadline(t, 0, change...)
}
func serviceWithDeadline(t *testing.T, budget time.Duration, change ...func(*preparation.Input)) (*campaignservice.Service, *peer, *runtime, *campaign.Writer, campaign.LaunchInputs) {
	t.Helper()
	return serviceWithOperations(t, budget, nil, change...)
}
func serviceWithOperations(t *testing.T, budget time.Duration, operations []string, change ...func(*preparation.Input)) (*campaignservice.Service, *peer, *runtime, *campaign.Writer, campaign.LaunchInputs) {
	t.Helper()
	in := fixture(t)
	for _, fn := range change {
		fn(&in)
	}
	target, err := preparation.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	w, launch, root := preparedLaunch(t, target, operations...)
	stored, err := target.Persist(w, launch.EngineContext)
	if err != nil {
		t.Fatal(err)
	}
	m := w.Manifest()
	docker := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	if err = w.SaveDockerBinding(docker); err != nil {
		t.Fatal(err)
	}
	native := &peer{input: in, revision: 5}
	runtime := &runtime{killed: make(chan struct{})}
	deadline := time.Time{}
	if budget > 0 {
		deadline = time.Now().Add(budget)
	}
	service, err := campaignservice.New(context.Background(), campaignservice.Config{Prepared: stored, Peer: native, Runtime: runtime, Docker: docker, StateRoot: root, Deadline: deadline})
	if err != nil {
		t.Fatal(err)
	}
	// The service must keep its own Docker identity after caller buffers change.
	for key := range docker.Labels {
		docker.Labels[key] = "changed-after-construction"
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = service.Shutdown(ctx)
	})
	return service, native, runtime, w, launch
}

func TestServiceRejectsUnadvertisedRouteBeforeTargetDispatch(t *testing.T) {
	s, p, r, w, launch := serviceWithOperations(t, 0, []string{"engine.observation_read"})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0); err == nil {
		t.Fatal("unadvertised attempt route was admitted")
	}
	select {
	case <-r.killed:
	case <-time.After(3 * time.Second):
		t.Fatal("unadvertised route did not close execution")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range p.calls {
		if name == "attempt.register" || name == "application.invoke" {
			t.Fatal("unadvertised request reached target", name)
		}
	}
}
func attemptWire(p *peer, revision int, index int, release string) []byte {
	id := fmt.Sprintf("request-%d", index)
	body := map[string]any{"api_version": "operator.dev/engine-attempt-request/v1alpha2", "kind": "EngineAttemptRequest", "request_id": id, "origin": "scenario", "scenario_id": "scenario-marker", "thread_id": "thread-1", "attempt_id": fmt.Sprintf("attempt-%d", index), "generation": index, "attempt_index": index, "payload": p.input.Artifacts[0].Descriptor, "generator": map[string]string{"kind": "operator-engine", "release_digest": release}, "pre_actions": []any{}, "invocation": map[string]string{"operation_id": "invoke", "input_source": "payload", "media_type": "application/json"}, "cleanup": map[string]bool{"delete_actions_after_observation": true}, "observation_selection": map[string]any{"mode": "selected", "kinds": []string{"oracle_outcome"}}}
	if index > 1 {
		body["parent_attempt_id"] = fmt.Sprintf("attempt-%d", index-1)
	}
	return encode(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": index - 1, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": revision, "call_id": id, "operation_id": id, "operation": "engine.attempt_execute", "timeout_ms": 30000, "body": body})
}
func TestServiceAdmitsVerifiedStartupTracksRevisionAndCloses(t *testing.T) {
	s, p, r, w, launch := serviceFixture(t)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		q := attemptWire(p, 1, i, w.Manifest().ReleaseRecordDigest)
		raw, err := s.Handle(context.Background(), q, int64(i-1))
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		if result["error"] != nil || result["result"].(map[string]any)["status"] != "completed" {
			t.Fatal(string(raw))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	terminal, err := s.Shutdown(ctx)
	if err != nil || terminal.Closure != "confirmed" || terminal.CleanupState != "complete" || terminal.TargetStop != "not-permitted" {
		t.Fatal(terminal, err)
	}
	select {
	case <-r.killed:
	default:
		t.Fatal("harness not independently terminated")
	}
	if _, err = s.Handle(context.Background(), attemptWire(p, 1, 3, w.Manifest().ReleaseRecordDigest), 2); err == nil {
		t.Fatal("closed campaign reopened")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, op := range p.calls {
		if op == "application.invoke" {
			count++
		}
	}
	if count != 2 {
		t.Fatal(p.calls)
	}
}
func TestServiceInvalidStartupCannotAdmit(t *testing.T) {
	s, _, r, _, launch := serviceFixture(t)
	launch.Prompt = []byte("changed")
	if err := s.Admit(context.Background(), launch); err == nil {
		t.Fatal("changed startup admitted")
	}
	select {
	case <-r.killed:
	case <-time.After(time.Second):
		t.Fatal("failed admission did not terminate harness")
	}
}
func TestServiceTerminationBypassesBlockedOrdinarySource(t *testing.T) {
	s, p, r, w, launch := serviceFixture(t)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	p.mu.Lock()
	p.block = block
	p.entered = entered
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("call not entered")
	}
	s.Stop(fmt.Errorf("administrator stop"))
	select {
	case <-r.killed:
	case <-time.After(time.Second):
		t.Fatal("ordinary source blocked Docker kill")
	}
	close(block)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source query not canceled")
	}
}
func TestServiceLostClosureDoesNotClaimCleanup(t *testing.T) {
	s, p, _, _, _ := serviceFixture(t)
	p.mu.Lock()
	p.unknownClose = true
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Shutdown(ctx)
	if err != nil || result.Closure != "unconfirmed" || result.CleanupState != "unconfirmed" {
		t.Fatal(result, err)
	}
}

func TestServiceTargetStopRequiresHostPermissionAndConfirmedClosure(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-closure-%t", lost), func(t *testing.T) {
			s, p, _, _, launch := serviceFixture(t, func(in *preparation.Input) {
				settings := in.Profile.Settings()
				settings.AllowTargetStop = true
				var err error
				in.Profile, err = targetprofile.Parse(encode(settings))
				if err != nil {
					t.Fatal(err)
				}
			})
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			p.mu.Lock()
			p.unknownClose = lost
			p.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := s.Shutdown(ctx)
			want := "confirmed"
			if lost {
				want = "unconfirmed"
			}
			if err != nil || result.TargetStop != want {
				t.Fatal(result, err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			stops := 0
			for _, call := range p.calls {
				if call == "session.stop" {
					stops++
				}
			}
			if lost && stops != 0 || !lost && stops != 1 {
				t.Fatal(p.calls)
			}
		})
	}
}

func TestServiceRealSpoolUsesGuestLanesAndZeroSequence(t *testing.T) {
	s, p, _, w, launch := serviceFixture(t)
	exchange := serviceSpool(t, s, p, w, launch)
	exchange(attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest))
}

func serviceSpool(t *testing.T, s *campaignservice.Service, p *peer, w *campaign.Writer, launch campaign.LaunchInputs) func([]byte) []byte {
	t.Helper()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	channel, err := transport.NewSpool(dir, transport.Config{Protocol: p.input.Protocol, CampaignID: "campaign-1", LaunchID: "launch-1", Fence: w.Fence(), CampaignDeadline: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { channel.Close() })
	pump := func() {
		t.Helper()
		if err := channel.Pump(); err != nil {
			t.Fatal(err)
		}
	}
	put := func(lane string, seq int64, raw []byte) {
		t.Helper()
		name, _ := contracts.SpoolMessageName(seq, false)
		tmp, _ := contracts.SpoolMessageName(seq, true)
		if err := os.WriteFile(filepath.Join(dir, lane, tmp), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, lane, tmp), filepath.Join(dir, lane, name)); err != nil {
			t.Fatal(err)
		}
	}
	send := func(raw []byte) {
		t.Helper()
		if err := channel.Enqueue("control-in", raw); err != nil {
			t.Fatal(err)
		}
		pump()
	}
	send(launch.Messages[0])
	put("control-out", 0, launch.Messages[1])
	pump()
	if _, ok := channel.Receive("control-out"); !ok {
		t.Fatal("no confinement message")
	}
	if err = channel.BeginInitialization(); err != nil {
		t.Fatal(err)
	}
	send(launch.Messages[2])
	put("control-out", 1, launch.Messages[3])
	pump()
	if _, ok := channel.Receive("control-out"); !ok {
		t.Fatal("no initialized message")
	}
	if err = s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	send(launch.Messages[4])
	if err = channel.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	pumpDone := make(chan error, 1)
	serveDone := make(chan error, 1)
	go func() { pumpDone <- channel.Run(ctx) }()
	go func() { serveDone <- s.ServeOrdinary(ctx, channel) }()

	t.Cleanup(func() { cancel(); <-pumpDone; <-serveDone })
	ack := func(ordinary any) {
		t.Helper()
		raw := encode(map[string]any{"api_version": "operator.dev/engine-spool-ack/v1alpha1", "launch_id": "launch-1", "ordinary_seq": ordinary, "control_seq": 2})
		tmp := filepath.Join(dir, "control-out/.consumed.tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, "control-out/consumed.json")); err != nil {
			t.Fatal(err)
		}
	}
	ack(nil)
	sequence := int64(0)
	return func(request []byte) []byte {
		t.Helper()
		var value map[string]any
		_ = json.Unmarshal(request, &value)
		value["seq"] = sequence
		request = encode(value)
		put("ordinary-out", sequence, request)
		name, _ := contracts.SpoolMessageName(sequence, false)
		response := filepath.Join(dir, "ordinary-in", name)
		timeout := time.NewTimer(4 * time.Second)
		defer timeout.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				t.Fatal("spool canceled")
			case <-w.Fence().Done():
				t.Fatal("spool fenced", w.Fence().Err())
			case <-timeout.C:
				t.Fatal("no ordinary reply")
			case <-ticker.C:
				raw, err := os.ReadFile(response)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err = p.input.Protocol.ValidateResponse(request, raw); err != nil {
					t.Fatal(err)
				}
				var reply map[string]any
				_ = json.Unmarshal(raw, &reply)
				if reply["seq"] != float64(sequence) {
					t.Fatal("transport sequence reset", reply)
				}
				ack(sequence)
				if err = os.Remove(filepath.Join(dir, "ordinary-out", name)); err != nil {
					t.Fatal(err)
				}
				sequence++
				return raw
			}
		}
	}
}

func TestServiceCleansOnlyConfirmedHandlesAfterClosure(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			s, p, _, w, launch := serviceFixture(t, func(in *preparation.Input) {
				settings := in.Profile.Settings()
				settings.Scopes.AllowRetainedInjections = true
				settings.Scopes.Routes = []attemptadapter.Route{{Surface: "mcp_tool_result", Target: attemptadapter.Target{Service: "tickets", ToolName: "get_ticket"}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{"/structuredContent/description"}}}
				profile, err := targetprofile.Parse(encode(settings))
				if err != nil {
					t.Fatal(err)
				}
				in.Profile = profile
			})
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			var q map[string]any
			_ = json.Unmarshal(attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), &q)
			body := q["body"].(map[string]any)
			body["pre_actions"] = []any{map[string]any{"action_id": "setup", "action_type": "interceptor.injection/v1alpha1", "parameters": map[string]any{"surface": "mcp_tool_result", "mode": "simulation", "scope": "once", "selector": map[string]any{"service": "tickets", "tool_name": "get_ticket", "call_ordinal": "first"}, "placement": map[string]any{"operation": "replace", "pointer": "/structuredContent/description"}, "payload_digest": p.input.Artifacts[0].Descriptor.Digest}}}
			body["carrier"] = p.input.Artifacts[0].Descriptor
			body["invocation"].(map[string]any)["input_source"] = "carrier"
			body["cleanup"] = map[string]bool{"delete_actions_after_observation": false}
			raw, err := s.Handle(context.Background(), encode(q), 0)
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]any
			_ = json.Unmarshal(raw, &response)
			if response["error"] != nil || response["result"].(map[string]any)["status"] != "completed" {
				t.Fatal(string(raw))
			}
			p.mu.Lock()
			p.unknownDelete = lost
			p.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := s.Shutdown(ctx)
			if err != nil || result.Closure != "confirmed" {
				t.Fatal(result, err)
			}
			if lost {
				if result.CleanupConfirmed != 0 || result.CleanupRemaining != 1 || result.CleanupState != "unconfirmed" {
					t.Fatal(result)
				}
			} else if result.CleanupConfirmed != 1 || result.CleanupRemaining != 0 || result.CleanupState != "complete" {
				t.Fatal(result)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			closed := false
			deletes := 0
			for _, op := range p.calls {
				if op == "session.owner" {
					closed = true
				}
				if op == "injection.delete" {
					if !closed {
						t.Fatal("cleanup before closure")
					}
					deletes++
				}
			}
			if deletes != 1 {
				t.Fatal("cleanup retried", p.calls)
			}
		})
	}
}

func TestServiceIdleNativeFailureTriggersTermination(t *testing.T) {
	s, p, r, _, launch := serviceFixture(t)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.input.Status.Phase = "error"
	p.input.Status.Closed = true
	p.mu.Unlock()
	select {
	case <-r.killed:
	case <-time.After(2 * time.Second):
		t.Fatal("idle failure did not terminate harness")
	}
}

func TestServiceProfileTimeoutBoundsBlockedNativeRead(t *testing.T) {
	s, p, r, w, launch := serviceFixture(t, func(in *preparation.Input) {
		settings := in.Profile.Settings()
		settings.OperationTimeoutMS = 100
		var err error
		in.Profile, err = targetprofile.Parse(encode(settings))
		if err != nil {
			t.Fatal(err)
		}
	})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.block = make(chan struct{})
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0)
		done <- err
	}()
	select {
	case <-r.killed:
	case <-time.After(3 * time.Second):
		t.Fatal("profile timeout did not terminate blocked work")
	}
	p.mu.Lock()
	close(p.block)
	p.block = nil
	p.mu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired request produced a reply")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expired ordinary request did not return")
	}
}

func TestServiceCampaignDeadlineTerminatesWithoutOrdinaryWork(t *testing.T) {
	s, _, r, _, _ := serviceWithDeadline(t, 200*time.Millisecond)
	select {
	case <-r.killed:
	case <-time.After(2 * time.Second):
		t.Fatal("campaign timer did not independently terminate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := s.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
