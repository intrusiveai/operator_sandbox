//go:build linux || darwin

package campaign

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func attachmentFixture(t *testing.T) (string, AttachmentIntent, interceptor.Attachment, interceptor.Status) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	d := contracts.RawDigest([]byte("test"))
	i := AttachmentIntent{CampaignID: "campaign-1", LaunchID: "launch-1", StartRequestID: strings.Repeat("a", 32), WorkerInstanceID: "worker-1", InputsFingerprint: d, MinimumFreeBytes: 1}
	b := interceptor.Binding{SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1}
	a := interceptor.Attachment{CampaignID: i.CampaignID, Binding: b, Session: interceptor.Session{ID: b.SessionID, CampaignID: i.CampaignID, OperationAPIVersion: interceptor.OperationVersion, Revision: 1, Phase: "running", FeedbackProfile: "black-box", EnvironmentDigest: d, AppDigest: d, CapabilityManifestDigest: d}}
	s := interceptor.Status{CampaignID: i.CampaignID, InstanceID: "instance-1", Active: b, Phase: "ready", StoreAvailable: true}
	return root, i, a, s
}
func TestAttachmentPersistenceAndIdentityChecks(t *testing.T) {
	for _, kind := range []string{"complete", "lost reply", "lost status", "binding pending", "instance pending", "missing intent", "changed binding", "changed permission", "changed status", "partial binding", "active writer"} {
		t.Run(kind, func(t *testing.T) {
			root, i, b, s := attachmentFixture(t)
			w, e := CreateAttachment(root, i)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = CreateAttachment(root, i); e == nil {
				t.Fatal("recreated attachment")
			}
			if kind == "active writer" {
				if _, _, e = OpenAttachmentRecovery(root, i.CampaignID); !errors.Is(e, ErrActive) {
					t.Fatal(e)
				}
				w.Close()
				return
			}
			if kind != "lost reply" {
				if e = w.Bind(b); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "changed status" {
				s.Active.SessionID = "wrong"
				if e = w.Verify(s); e == nil {
					t.Fatal("accepted drift")
				}
				s.Active = b.Binding
				if e = w.Verify(s); e == nil {
					t.Fatal("revived failed writer")
				}
				w.Close()
				return
			}
			if kind != "lost reply" && kind != "lost status" {
				if e = w.Verify(s); e != nil {
					t.Fatal(e)
				}
			}
			w.Close()
			dir := filepath.Join(root, "attachments", i.CampaignID)
			switch kind {
			case "binding pending", "instance pending":
				name := strings.Split(kind, " ")[0] + ".json.pending"
				if e = os.WriteFile(filepath.Join(dir, name), []byte("partial"), 0600); e != nil {
					t.Fatal(e)
				}
			case "missing intent":
				os.Remove(filepath.Join(dir, "intent.json"))
			case "partial binding":
				os.WriteFile(filepath.Join(dir, "binding.json"), []byte(`{}`), 0600)
			case "changed binding":
				raw, _ := os.ReadFile(filepath.Join(dir, "binding.json"))
				raw = []byte(strings.ReplaceAll(string(raw), "session-1", "session-2"))
				os.WriteFile(filepath.Join(dir, "binding.json"), raw, 0600)
			case "changed permission":
				raw, _ := os.ReadFile(filepath.Join(dir, "intent.json"))
				raw = []byte(strings.ReplaceAll(string(raw), `"allow_target_stop":false`, `"allow_target_stop":true`))
				os.WriteFile(filepath.Join(dir, "intent.json"), raw, 0600)
			}
			audit, saved, e := OpenAttachmentRecovery(root, i.CampaignID)
			valid := kind == "complete" || kind == "lost reply" || kind == "lost status"
			if !valid {
				if e == nil {
					audit.Close()
					t.Fatal("accepted damaged attachment")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer audit.Close()
			if saved.Intent != i || (saved.Binding != nil) != (kind != "lost reply") || (saved.InstanceID != "") != (kind == "complete") {
				t.Fatal(saved)
			}
			fresh, _, e := audit.Begin()
			if e != nil || !fresh {
				t.Fatal(e)
			}
			if e = audit.Finish(map[string]string{"state": "unknown"}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestAttachmentPublicationFailureCannotBeRetried(t *testing.T) {
	root, i, b, s := attachmentFixture(t)
	w, e := CreateAttachment(root, i)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	pending := filepath.Join(root, "attachments", i.CampaignID, "binding.json.pending")
	if e = os.WriteFile(pending, []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = w.Bind(b); e == nil {
		t.Fatal("replaced partial binding")
	}
	os.Remove(pending)
	if e = w.Bind(b); e == nil {
		t.Fatal("retried after persistence failure")
	}
	if e = w.Verify(s); e == nil {
		t.Fatal("continued after persistence failure")
	}
}
