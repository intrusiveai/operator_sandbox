//go:build linux || darwin

package hostrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type attachTestPeer struct {
	attach func() (interceptor.Attachment, error)
	status func() (interceptor.Status, error)
}

func (p attachTestPeer) Attach(context.Context, string, string, bool) (interceptor.Attachment, error) {
	return p.attach()
}
func (p attachTestPeer) Status(context.Context, string) (interceptor.Status, error) {
	return p.status()
}
func TestRecordedAttachOrdersDurabilityBeforeEffects(t *testing.T) {
	for _, fault := range []string{"none", "attach reply", "binding write", "status reply", "status drift", "terminal status", "instance write", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			os.Chmod(root, 0700)
			digest := contracts.RawDigest([]byte("inputs"))
			intent := campaign.AttachmentIntent{CampaignID: "campaign-1", LaunchID: "launch-1", StartRequestID: strings.Repeat("a", 32), WorkerInstanceID: "worker-1", InputsFingerprint: digest, AllowTargetStop: true}
			dir := filepath.Join(root, "attachments", "campaign-1")
			binding := interceptor.Binding{SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1}
			attachment := interceptor.Attachment{CampaignID: "campaign-1", Binding: binding, Session: interceptor.Session{ID: "session-1", CampaignID: "campaign-1", OperationAPIVersion: interceptor.OperationVersion, Revision: 1, Phase: "running", FeedbackProfile: "black-box", EnvironmentDigest: digest, AppDigest: digest, CapabilityManifestDigest: digest}}
			attaches, statuses := 0, 0
			check := func(name string) {
				t.Helper()
				if _, e := os.Stat(filepath.Join(dir, name)); e != nil {
					t.Fatal("effect before durable predecessor", e)
				}
			}
			p := attachTestPeer{attach: func() (interceptor.Attachment, error) {
				attaches++
				check("intent.json")
				if fault == "attach reply" {
					return interceptor.Attachment{}, errors.New("lost")
				}
				if fault == "binding write" {
					os.WriteFile(filepath.Join(dir, "binding.json.pending"), []byte("partial"), 0600)
				}
				return attachment, nil
			}, status: func() (interceptor.Status, error) {
				statuses++
				check("binding.json")
				if fault == "status reply" {
					return interceptor.Status{}, errors.New("lost")
				}
				if fault == "instance write" {
					os.WriteFile(filepath.Join(dir, "instance.json.pending"), []byte("partial"), 0600)
				}
				s := interceptor.Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: binding, Phase: "ready", StoreAvailable: true}
				if fault == "terminal status" {
					s.Phase = "error"
					s.Closed = true
				}
				if fault == "status drift" {
					s.Active.SessionID = "changed"
				}
				return s, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancelled" {
				cancel()
			}
			_, _, e := attachRecorded(ctx, root, intent, p)
			if (e == nil) != (fault == "none") {
				t.Fatal(e)
			}
			expectedStatus := 1
			if fault == "attach reply" || fault == "binding write" || fault == "cancelled" {
				expectedStatus = 0
			}
			if statuses != expectedStatus || (attaches == 0) != (fault == "cancelled") {
				t.Fatal(attaches, statuses)
			}
			if fault == "none" || fault == "terminal status" {
				check("instance.json")
			}
			if _, _, e = attachRecorded(ctx, root, intent, p); e == nil {
				t.Fatal("reattached old campaign")
			}
			if attaches > 1 {
				t.Fatal("replayed native attach")
			}
		})
	}
}
