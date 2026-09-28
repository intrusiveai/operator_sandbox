package preparation_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	startupgate "github.com/intrusiveai/operator_sandbox/internal/startup"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

type attachmentRecoveryPeer struct {
	*peer
	statusCalls int
}

func (p *attachmentRecoveryPeer) Status(ctx context.Context, id string) (interceptor.Status, error) {
	p.statusCalls++
	return p.peer.Status(ctx, id)
}

func TestStartupReconcilesAttachmentBeforeCampaignPreparation(t *testing.T) {
	for _, kind := range []string{"complete", "stop permitted", "lost attach", "lost status", "lost close", "changed instance", "changed session", "changed policy", "partial preparation", "prepared journal", "interrupted cleanup"} {
		t.Run(kind, func(t *testing.T) {
			in := fixture(t)
			if kind == "stop permitted" {
				settings := in.Profile.Settings()
				settings.AllowTargetStop = true
				in.Profile, _ = targetprofile.Parse(encode(settings))
			}
			root := t.TempDir()
			os.Chmod(root, 0700)
			if kind == "partial preparation" || kind == "prepared journal" {
				target, e := preparation.Build(in)
				if e != nil {
					t.Fatal(e)
				}
				w, launch, preparedRoot := preparedLaunchConfigured(t, target, nil, "")
				root = preparedRoot
				if kind == "prepared journal" {
					if _, e = target.Persist(w, launch.EngineContext); e != nil {
						t.Fatal(e)
					}
				}
				w.Close()
			}
			intent := campaign.AttachmentIntent{CampaignID: "campaign-1", LaunchID: "launch-1", StartRequestID: strings.Repeat("a", 32), WorkerInstanceID: "worker-1", InputsFingerprint: contracts.RawDigest([]byte("inputs")), AllowTargetStop: in.Profile.Settings().AllowTargetStop}
			w, e := campaign.CreateAttachment(root, intent)
			if e != nil {
				t.Fatal(e)
			}
			if kind != "lost attach" {
				if e = w.Bind(in.Attachment); e != nil {
					t.Fatal(e)
				}
			}
			if kind != "lost attach" && kind != "lost status" {
				if e = w.Verify(in.Status); e != nil {
					t.Fatal(e)
				}
			}
			w.Close()
			p := &peer{input: in, revision: 5, unknownClose: kind == "lost close"}
			switch kind {
			case "changed instance":
				p.input.Status.InstanceID = "other-instance"
			case "changed session":
				p.input.Status.Active.SessionID = "other-session"
			case "changed policy":
				settings := p.input.Profile.Settings()
				settings.AllowTargetStop = true
				p.input.Profile, _ = targetprofile.Parse(encode(settings))
			case "interrupted cleanup":
				a, _, e := campaign.OpenAttachmentRecovery(root, "campaign-1")
				if e != nil {
					t.Fatal(e)
				}
				if _, _, e = a.Begin(); e != nil {
					t.Fatal(e)
				}
				a.Close()
			}
			checked := &attachmentRecoveryPeer{peer: p}
			gate, prior, e := startupgate.Acquire(context.Background(), root, &composedDocker{})
			if e != nil {
				t.Fatal(e)
			}
			if len(prior) != 1 || prior[0].CampaignID != "campaign-1" {
				t.Fatal(prior)
			}
			rows, e := gate.FinalizeNative(context.Background(), checked)
			if e != nil {
				t.Fatal(e)
			}
			gate.Close()
			out := rows[0].Native
			if out == nil {
				t.Fatal(rows)
			}
			complete := kind == "complete" || kind == "stop permitted" || kind == "partial preparation" || kind == "prepared journal"
			if (out.State == "complete") != complete {
				t.Fatal(out, p.calls)
			}
			if slices.Contains(p.calls, "session.stop") != (kind == "stop permitted") {
				t.Fatal(p.calls)
			}
			if slices.Contains(p.calls, "injection.delete") {
				t.Fatal("preparation issued cleanup for an injection", p.calls)
			}
			if !complete && kind != "lost close" && slices.Contains(p.calls, "session.owner") {
				t.Fatal("closed unverified target", p.calls)
			}
			if kind == "prepared journal" {
				if out.ManifestDigest == "" || out.AttachmentDigest != "" {
					t.Fatal(out)
				}
				if _, e = os.Stat(filepath.Join(root, "attachments/campaign-1/native-recovery")); !os.IsNotExist(e) {
					t.Fatal("duplicate recovery", e)
				}
			} else if out.AttachmentDigest == "" || out.ManifestDigest != "" {
				t.Fatal(out)
			}
			if (kind == "lost attach" || kind == "lost status" || kind == "interrupted cleanup") && checked.statusCalls != 0 {
				t.Fatal("queried target without saved identity/claim", checked.statusCalls)
			}
			calls := len(p.calls)
			statusCalls := checked.statusCalls
			gate, _, e = startupgate.Acquire(context.Background(), root, &composedDocker{})
			if e != nil {
				t.Fatal(e)
			}
			again, e := gate.FinalizeNative(context.Background(), checked)
			gate.Close()
			if e != nil || len(p.calls) != calls || checked.statusCalls != statusCalls || *again[0].Native != *out {
				t.Fatal("replayed recovery", again, e, p.calls)
			}
			if kind == "prepared journal" {
				if _, e = nativerecovery.RunAttachment(context.Background(), root, "campaign-1", p); e == nil {
					t.Fatal("attachment recovery bypassed journal")
				}
			}
		})
	}
}
