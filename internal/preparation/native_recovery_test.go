package preparation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	startupgate "github.com/intrusiveai/operator_sandbox/internal/startup"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

func recoveryFixture(t *testing.T, allow bool) (string, *campaign.Writer, *peer) {
	t.Helper()
	in := fixture(t)
	if allow {
		s := in.Profile.Settings()
		s.AllowTargetStop = true
		p, e := targetprofile.Parse(encode(s))
		if e != nil {
			t.Fatal(e)
		}
		in.Profile = p
	}
	target, e := preparation.Build(in)
	if e != nil {
		t.Fatal(e)
	}
	w, launch, root := preparedLaunchConfigured(t, target, nil, "")
	if _, e = target.Persist(w, launch.EngineContext); e != nil {
		t.Fatal(e)
	}
	return root, w, &peer{input: in, revision: 5}
}
func recoveryInjection(t *testing.T, w *campaign.Writer, id string, known bool) {
	t.Helper()
	p, e := interceptor.PrepareOperation(interceptor.OperationRequest{CampaignID: "campaign-1", SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1, RequestID: id, OperationID: id, Operation: "injection.arm", Deadline: time.Now().Add(time.Minute)}, encode(map[string]any{"definition": map[string]string{"id": id}}))
	if e != nil {
		t.Fatal(e)
	}
	refs, e := w.AppendStored(campaign.Entry{RunRevision: w.Revision(), Kind: "fixture.native-request", Metadata: json.RawMessage(`{}`), Content: []campaign.Content{{Role: "native-request-0", MediaType: "application/json", Bytes: p.Bytes()}}}, "", false)
	if e != nil {
		t.Fatal(e)
	}
	s := campaign.NativeStep{ID: id, OperationID: id, SessionID: "session-1", Operation: "injection.arm", Request: refs, State: campaign.ResultCommitted, Outcome: "succeeded"}
	if !known {
		s.State = campaign.Unknown
		s.Outcome = "unknown"
	}
	if _, e = w.Append(campaign.Entry{RunRevision: w.Revision(), Kind: "native.resolved", Metadata: encode(map[string]any{"native_step": s})}); e != nil {
		t.Fatal(e)
	}
}

func TestNativeRecoveryClosesCleansAndHonorsStopPolicy(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprint(allow), func(t *testing.T) {
			root, w, p := recoveryFixture(t, allow)
			recoveryInjection(t, w, "known", true)
			recoveryInjection(t, w, "unknown", false)
			w.Close()
			before, e := campaign.Inspect(root, "campaign-1", nil)
			if e != nil {
				t.Fatal(e)
			}
			out, e := nativerecovery.Run(context.Background(), root, "campaign-1", true, p)
			if e != nil || out.State != "complete" || out.Closure != "confirmed" || out.CleanupConfirmed != 1 || out.CleanupRemaining != 0 {
				t.Fatal(out, e)
			}
			if slices.Contains(p.calls, "session.stop") != allow {
				t.Fatal(p.calls)
			}
			if slices.Index(p.calls, "injection.delete") < slices.Index(p.calls, "session.owner") {
				t.Fatal("deleted before closure", p.calls)
			}
			calls := len(p.calls)
			again, e := nativerecovery.Run(context.Background(), root, "campaign-1", true, p)
			if e != nil || again != out || len(p.calls) != calls {
				t.Fatal("replayed cleanup", again, e)
			}
			after, e := campaign.Inspect(root, "campaign-1", nil)
			if e != nil || before.VerifiedBytes != after.VerifiedBytes || before.VerifiedEvents != after.VerifiedEvents {
				t.Fatal(after, e)
			}
		})
	}
}

func TestNativeRecoveryRefusesUncertainIdentityAndDispatch(t *testing.T) {
	for _, kind := range []string{"active writer", "container unknown", "instance changed", "session changed", "environment changed", "policy changed", "lost close", "lost delete", "previous close", "previous cleanup", "previous stop", "corrupt journal", "cancelled", "interrupted recovery"} {
		t.Run(kind, func(t *testing.T) {
			root, w, p := recoveryFixture(t, kind == "previous stop")
			recoveryInjection(t, w, "known", true)
			switch kind {
			case "instance changed":
				p.input.Status.InstanceID = "new-instance"
			case "session changed":
				p.input.Status.Active.SessionID = "new-session"
			case "environment changed":
				p.input.Attachment.Session.EnvironmentDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "policy changed":
				s := p.input.Profile.Settings()
				s.AllowTargetStop = true
				p.input.Profile, _ = targetprofile.Parse(encode(s))
			case "lost close":
				p.unknownClose = true
			case "lost delete":
				p.unknownDelete = true
			}
			previous := map[string]string{"previous close": "service.native-close-intent", "previous cleanup": "service.native-cleanup-intent", "previous stop": "service.target-stop-intent"}
			if event := previous[kind]; event != "" {
				if _, e := w.Append(campaign.Entry{RunRevision: w.Revision(), Kind: event, Metadata: json.RawMessage(`{}`)}); e != nil {
					t.Fatal(e)
				}
			}
			if kind != "active writer" {
				w.Close()
			}
			if kind == "corrupt journal" {
				os.WriteFile(filepath.Join(root, "campaigns/campaign-1/journal-head.json"), []byte("bad"), 0600)
			}
			if kind == "interrupted recovery" {
				a, e := campaign.OpenNativeRecovery(root, "campaign-1")
				if e != nil {
					t.Fatal(e)
				}
				fresh, _, e := a.Begin()
				a.Close()
				if e != nil || !fresh {
					t.Fatal(e)
				}
			}
			ctx := context.Background()
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			out, e := nativerecovery.Run(ctx, root, "campaign-1", kind != "container unknown", p)
			if out.State == "complete" {
				t.Fatal("uncertainty became completion", out, e)
			}
			mutations := 0
			for _, op := range p.calls {
				if op != "session.status" {
					mutations++
				}
			}
			switch kind {
			case "lost close":
				if mutations != 1 {
					t.Fatal(p.calls)
				}
			case "lost delete":
				if mutations != 2 {
					t.Fatal(p.calls)
				}
			case "previous cleanup":
				if slices.Contains(p.calls, "injection.delete") {
					t.Fatal(p.calls)
				}
			case "previous stop":
				if slices.Contains(p.calls, "session.stop") {
					t.Fatal(p.calls)
				}
			default:
				if mutations != 0 {
					t.Fatal("unsafe mutation", p.calls)
				}
			}
		})
	}
}

func TestNativeRecoveryBoundsCleanupAndUsesVerifiedRestore(t *testing.T) {
	root, w, p := recoveryFixture(t, true)
	for i := range 65 {
		recoveryInjection(t, w, fmt.Sprintf("injection-%03d", i), true)
	}
	b := p.input.Status.Active
	b.SessionID = "replacement"
	b.RunRevision++
	p.input.Status.Active = b
	p.input.Status.Sessions[b.SessionID] = b
	p.input.Attachment.Binding = b
	p.input.Attachment.Session.ID = b.SessionID
	if _, e := w.Append(campaign.Entry{RunRevision: w.Revision(), Kind: "state.replacement-verified", Metadata: json.RawMessage(`{}`), Content: []campaign.Content{{Role: "request", MediaType: "application/json", Bytes: encode(map[string]any{"binding": b, "status": p.input.Status})}}}); e != nil {
		t.Fatal(e)
	}
	w.Close()
	out, e := nativerecovery.Run(context.Background(), root, "campaign-1", true, p)
	if e != nil || out.Cleanup != "bounded-remainder" || out.CleanupConfirmed != 64 || out.CleanupRemaining != 1 || out.TargetStop != "confirmed" {
		t.Fatal(out, e)
	}
}

func TestStartupGateFinalizesNativeBeforeFreshWorkerClaim(t *testing.T) {
	root, w, p := recoveryFixture(t, false)
	w.Close()
	gate, _, err := startupgate.Acquire(context.Background(), root, &composedDocker{})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	rows, err := gate.FinalizeNative(context.Background(), p)
	if err != nil || len(rows) != 1 || rows[0].Native == nil || rows[0].Native.State != "complete" {
		t.Fatal(rows, err)
	}
	if _, err = gate.FinalizeNative(context.Background(), p); err == nil {
		t.Fatal("repeated finalization")
	}
	if !gate.Claim(root) {
		t.Fatal("cannot transfer reconciled gate")
	}
	if _, err = gate.FinalizeNative(context.Background(), p); err == nil {
		t.Fatal("finalization after worker claim")
	}
}
