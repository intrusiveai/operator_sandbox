package attemptadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/feedback"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/nativeexec"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type runtimeStub struct{}

func (runtimeStub) CheckRunning(context.Context, campaign.DockerBinding) error { return nil }

type nativePeer struct {
	t            *testing.T
	plan         *Plan
	calls        []string
	revision     uint64
	fail         string
	malformed    string
	unavailable  bool
	unreadyAfter string
	after        func(string)
	output       []byte
	view         interceptor.ObservationView
}

func (p *nativePeer) Status(context.Context, string) (interceptor.Status, error) {
	b := p.plan.binding
	s := interceptor.Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: b, Sessions: map[string]interceptor.Binding{b.SessionID: b}, Phase: "ready", StoreAvailable: true}
	if len(p.calls) > 0 && p.calls[len(p.calls)-1] == p.unreadyAfter {
		s.Closed = true
	}
	return s, nil
}
func (p *nativePeer) Execute(_ context.Context, q interceptor.PreparedOperation) (interceptor.Response, error) {
	p.calls = append(p.calls, q.Request().Operation)
	if q.Request().ExpectedSessionRevision != p.revision {
		p.t.Fatalf("wrong expected native revision: %d != %d", q.Request().ExpectedSessionRevision, p.revision)
	}
	if q.Request().AttemptContextDigest != p.plan.context.Digest {
		p.t.Fatal("wrong context digest")
	}
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	_ = json.Unmarshal(q.Bytes(), &envelope)
	op := q.Request().Operation
	if p.fail == op {
		if op == "application.invoke" {
			return interceptor.Response{}, &interceptor.CallError{Kind: "lost_reply", Uncertain: true}
		}
		return response(p.t, 409, p.revision, map[string]any{"error": map[string]string{"code": "test_rejection", "message": "SECRET"}}), nil
	}
	status := 200
	var body any
	switch op {
	case "artifact.register":
		var input struct {
			Descriptor interceptor.ArtifactDescriptor `json:"descriptor"`
			Content    []byte                         `json:"content"`
		}
		_ = json.Unmarshal(envelope.Body, &input)
		if contracts.RawDigest(input.Content) != input.Descriptor.Digest {
			p.t.Fatal("bad artifact")
		}
		body = input.Descriptor
		status = 201
		p.revision++
	case "attempt.register":
		var a interceptor.AttemptContext
		_ = json.Unmarshal(envelope.Body, &a)
		if a.Digest != interceptor.AttemptContextDigest(a) {
			p.t.Fatal("bad attempt digest")
		}
		body = a
		status = 201
		p.revision++
	case "injection.arm":
		var a struct {
			Definition interceptor.Definition `json:"definition"`
		}
		_ = json.Unmarshal(envelope.Body, &a)
		body = a
		status = 201
		p.revision++
	case "application.invoke":
		var input interceptor.TurnRequest
		_ = json.Unmarshal(envelope.Body, &input)
		if input.ArtifactDigest != "" || !bytes.Contains(input.Input, []byte("query")) {
			p.t.Fatal("carrier translation")
		}
		now := time.Now().UTC()
		body = interceptor.Turn{ID: "turn-1", Operation: "invoke", Status: "complete", AttemptID: q.Request().AttemptID, PayloadDigest: p.plan.request.Payload.Digest, Body: p.output, MediaType: "text/plain", OutputDigest: contracts.RawDigest(p.output), Started: now, Finished: now}
		p.revision++
	case "observation.read":
		var lookup map[string]string
		_ = json.Unmarshal(envelope.Body, &lookup)
		if lookup["turn_id"] != "turn-1" || len(lookup) != 1 {
			p.t.Fatal("feedback not bound to exact invocation")
		}
		now := time.Now().UTC()
		p.view = interceptor.ObservationView{APIVersion: "interceptor.dev/observation-view/v1alpha2", CampaignID: "campaign-1", SessionID: "session-1", AttemptID: q.Request().AttemptID, AttemptContextDigest: p.plan.context.Digest, TurnID: "turn-1", ReceiptID: interceptor.FeedbackReceiptID("session-1", "turn-1"), FeedbackProfile: "black-box", SessionRevision: p.revision, CapturedAt: now, WindowStart: now, WindowEnd: now, CollectionState: "complete", Operation: interceptor.OperationView{State: "SUCCEEDED", ReceiptID: "turn-1"}, Observations: []interceptor.Observation{}, Categories: []interceptor.FeedbackCategory{{Kind: "target_output", State: "available"}, {Kind: "operation_error", State: "not_requested"}, {Kind: "injection_delivery", State: "not_requested"}, {Kind: "oracle_outcome", State: "not_requested"}}, Entries: []interceptor.FeedbackEntry{{ID: "entry-1", Kind: "target_output", Visibility: interceptor.TargetVisible, Source: "application", Assurance: "target-response", Availability: "available", Artifact: &interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(p.output), SizeBytes: int64(len(p.output)), MediaType: "text/plain", Canonicalization: "raw"}, OriginalSizeBytes: int64(len(p.output))}}}
		p.view.Hash = interceptor.ObservationViewDigest(p.view)
		body = p.view
	case "observation.content.read":
		var query interceptor.FeedbackReadRequest
		_ = json.Unmarshal(envelope.Body, &query)
		end := min(int64(len(p.output)), query.Offset+int64(query.MaxBytes))
		chunk := interceptor.FeedbackChunk{ReceiptID: p.view.ReceiptID, Entry: p.view.Entries[0], Offset: query.Offset, Content: p.output[query.Offset:end], RawLength: int(end - query.Offset), EOF: end == int64(len(p.output))}
		if p.unavailable {
			chunk.Entry.Availability = "unavailable"
			chunk.Entry.Reason = "content_missing_or_corrupt"
			chunk.Content = []byte{}
			chunk.RawLength = 0
			chunk.EOF = false
		}
		body = chunk
	case "injection.delete":
		status = 204
		p.revision++
	default:
		p.t.Fatal("unexpected operation", op)
	}
	if p.malformed == op {
		body = map[string]bool{"ok": true}
	}
	if p.after != nil {
		p.after(op)
	}
	return response(p.t, status, p.revision, body), nil
}
func response(t *testing.T, status int, revision uint64, body any) interceptor.Response {
	t.Helper()
	m := map[string]any{"status": status, "session_revision": revision}
	if body != nil {
		m["body"] = body
	}
	r, err := interceptor.ParseResponse(encode(m))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func executionSetup(t *testing.T, raw []byte, p *Plan) (*Execution, *campaign.Writer, *campaign.Attempts, *nativePeer) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	d := contracts.RawDigest([]byte("fixture"))
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 1, CreatedAt: "2026-09-25T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1", ImageDigest: d, ReleaseRecordDigest: d, Contract: campaign.ContractPin{Version: "0.1.0", Digest: d, CatalogDigest: d, OperationsDigest: d}, EngineContextDigest: d, InputTreeDigest: d, SkillSetDigest: d, ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d, Target: campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: "session-1", WorkerInstanceID: "worker-1", NativeFeedbackProfile: "diagnostic", CapabilitySourceDigest: d, CapabilityProjectionDigest: d}, RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`), HarnessLimits: json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`), Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 128 << 20, MaxSegmentBytes: campaign.MaxEventBytes}}
	m.Target.NativeFeedbackProfile = "black-box"
	m.Target.CapabilitySourceDigest, m.Target.CapabilityProjectionDigest = p.sourceDigest, p.projectionDigest
	m.Retention.MaxJournalBytes = 256 << 20
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
	if _, _, err = a.Observe(campaign.AttemptInput{CampaignID: "campaign-1", WorkerInstanceID: "worker-1", RunRevision: 1, Body: raw}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Admit(p.RequestID(), p.RecordJSON()); err != nil {
		t.Fatal(err)
	}
	if _, err = a.MarkDispatched(p.RequestID()); err != nil {
		t.Fatal(err)
	}
	docker := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	if err = w.SaveDockerBinding(docker); err != nil {
		t.Fatal(err)
	}
	peer := &nativePeer{t: t, plan: p, revision: p.revision, output: []byte("benign marker")}
	guard, err := nativeexec.NewGuard(peer, runtimeStub{}, docker, "instance-1", p.binding)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := p.NewExecution(a, guard)
	if err != nil {
		t.Fatal(err)
	}
	return execution, w, a, peer
}

func TestDurableTypedExecutionAndFeedback(t *testing.T) {
	c, raw, in := fixture(t)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	e, w, a, peer := executionSetup(t, raw, p)
	peer.output = bytes.Repeat([]byte("x"), feedback.MaxChunk+17)
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"artifact.register", "artifact.register", "attempt.register", "injection.arm", "application.invoke", "observation.read", "observation.content.read", "observation.content.read", "injection.delete"}
	if !reflect.DeepEqual(peer.calls, expected) {
		t.Fatal(peer.calls)
	}
	if len(result.Injections) != 1 || !result.Injections[0].Deleted || result.Receipt == nil || len(result.StepIDs) != len(expected) {
		t.Fatal("incomplete result")
	}
	for _, id := range result.StepIDs {
		step, _, _, err := a.NativeSteps().Lookup(id)
		if err != nil || step.State != campaign.ResultCommitted || step.Outcome != "succeeded" {
			t.Fatal("non-durable result", err)
		}
	}
	var manifest feedback.Manifest
	_ = json.Unmarshal(result.Receipt.ManifestJSON(), &manifest)
	content, ok := result.Receipt.Content(manifest.Entries[0].ID)
	if !ok || !bytes.Equal(content, peer.output) {
		t.Fatal("wrong feedback bytes")
	}
	if w.Fence().Err() != nil {
		t.Fatal(w.Fence().Err())
	}
	if _, err = e.Run(context.Background()); err == nil || !reflect.DeepEqual(peer.calls, expected) {
		t.Fatal("execution repeated")
	}
	if err = a.Resolve(p.RequestID(), campaign.AttemptCompletion{Outcome: "succeeded", Result: result.GuestJSON, Receipts: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}
func TestTypedFailuresFenceAndPreventLaterEffects(t *testing.T) {
	for _, tc := range []struct{ name, fail, malformed, last, status string }{{"setup rejected", "injection.arm", "", "injection.arm", "failed"}, {"lost invocation reply", "application.invoke", "", "application.invoke", "unknown"}, {"bad attempt receipt", "", "attempt.register", "attempt.register", "unknown"}, {"bad feedback receipt", "", "observation.read", "observation.read", "unknown"}, {"cleanup rejected", "injection.delete", "", "injection.delete", "failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			c, raw, in := fixture(t)
			p, err := Compile(c, raw, in)
			if err != nil {
				t.Fatal(err)
			}
			e, w, a, peer := executionSetup(t, raw, p)
			peer.fail, peer.malformed = tc.fail, tc.malformed
			r, err := e.Run(context.Background())
			if err == nil || w.Fence().Err() == nil {
				t.Fatal("failure did not close execution")
			}
			if peer.calls[len(peer.calls)-1] != tc.last {
				t.Fatal(peer.calls)
			}
			var result map[string]any
			if json.Unmarshal(r.GuestJSON, &result) != nil || result["status"] != tc.status || bytes.Contains(r.GuestJSON, []byte("SECRET")) {
				t.Fatalf("bad result %s (%v)", r.GuestJSON, err)
			}
			if err = a.Resolve(p.RequestID(), campaign.AttemptCompletion{Outcome: "succeeded", Result: r.GuestJSON, Receipts: json.RawMessage(`{}`)}); err == nil {
				t.Fatal("failed attempt succeeded")
			}
		})
	}
}
func TestRetainedActionsEmptySelectionAndUnavailableFeedback(t *testing.T) {
	for _, mode := range []string{"retained", "empty", "unavailable", "quota"} {
		t.Run(mode, func(t *testing.T) {
			c, raw, in := fixture(t)
			if mode == "retained" {
				raw = mutate(raw, func(m map[string]any) { m["cleanup"].(map[string]any)["delete_actions_after_observation"] = false })
			}
			if mode == "empty" {
				raw = mutate(raw, func(m map[string]any) {
					m["observation_selection"] = map[string]any{"mode": "selected", "kinds": []string{"oracle_outcome"}}
				})
			}
			if mode == "quota" {
				in.FeedbackBytes = 0
			}
			p, err := Compile(c, raw, in)
			if err != nil {
				t.Fatal(err)
			}
			e, _, _, peer := executionSetup(t, raw, p)
			peer.unavailable = mode == "unavailable"
			r, err := e.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "retained" {
				if r.Injections[0].Deleted || peer.calls[len(peer.calls)-1] == "injection.delete" {
					t.Fatal("retained action removed")
				}
			}
			if mode == "empty" {
				for _, op := range peer.calls {
					if strings.HasPrefix(op, "observation.") {
						t.Fatal("empty selection collected native feedback")
					}
				}
				if p.context.ObservationSelection != nil {
					t.Fatal("native empty selected registration")
				}
			}
			if mode == "quota" || mode == "unavailable" {
				if !bytes.Contains(r.Receipt.ManifestJSON(), []byte(`"availability":"unavailable"`)) {
					t.Fatal(string(r.Receipt.ManifestJSON()))
				}
			}
		})
	}
}
func TestAdapterRejectsChangedNativeCommand(t *testing.T) {
	c, raw, in := fixture(t)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	q := interceptor.OperationRequest{RequestID: "step", OperationID: "step", Operation: "application.invoke", CampaignID: "campaign-1", SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1, Deadline: in.Deadline}
	a, _ := interceptor.PrepareOperation(q, []byte(`{"operation":"invoke"}`))
	b, _ := interceptor.PrepareOperation(q, []byte(`{"operation":"other"}`))
	adapter := stepAdapter{plan: p, active: a}
	if !errors.Is(adapter.Authorize(b), ErrPolicy) {
		t.Fatal("command substitution admitted")
	}
}

func TestStoppedBeforeInvocationReportsNoDispatch(t *testing.T) {
	c, raw, in := fixture(t)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	e, _, _, peer := executionSetup(t, raw, p)
	peer.unreadyAfter = "injection.arm"
	r, err := e.Run(context.Background())
	if !errors.Is(err, nativeexec.ErrFailed) {
		t.Fatal(err)
	}
	var result map[string]any
	_ = json.Unmarshal(r.GuestJSON, &result)
	if result["invocation_state"] != "not-dispatched" || result["status"] != "failed" || peer.calls[len(peer.calls)-1] != "injection.arm" {
		t.Fatalf("incorrect failed preflight result: %s", r.GuestJSON)
	}
}
func TestJournalFailureAfterInvocationDoesNotClaimNoDispatch(t *testing.T) {
	c, raw, in := fixture(t)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	e, w, _, peer := executionSetup(t, raw, p)
	peer.after = func(op string) {
		if op == "application.invoke" {
			_ = w.Close()
		}
	}
	r, err := e.Run(context.Background())
	if err == nil {
		t.Fatal("expected result persistence failure")
	}
	var result map[string]any
	_ = json.Unmarshal(r.GuestJSON, &result)
	if result["invocation_state"] != "unknown" || result["target_contact"] != "unknown" || result["status"] != "unknown" || peer.calls[len(peer.calls)-1] != "application.invoke" {
		t.Fatalf("incorrect uncertain result: %s (%v)", r.GuestJSON, err)
	}
}
func TestCompiledRequestCannotBorrowAnotherAdmission(t *testing.T) {
	c, raw, in := fixture(t)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	_, _, a, _ := executionSetup(t, raw, p)
	p.rawRequest = mutate(raw, func(m map[string]any) { m["rationale"] = "different submitted request" })
	if _, err = p.NewExecution(a, nil); !errors.Is(err, ErrAttempt) {
		t.Fatal("request mismatch not rejected before executor construction", err)
	}
}
