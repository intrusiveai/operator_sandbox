//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func TestLateEvidenceClaimPreservesUncertaintyAndIntegrity(t *testing.T) {
	for _, damage := range []string{"none", "lost result", "missing intent", "changed result", "extra pending"} {
		t.Run(damage, func(t *testing.T) {
			root, w := newWriter(t)
			if _, e := OpenNativeRecovery(root, "campaign-1"); !errors.Is(e, ErrActive) {
				t.Fatal(e)
			}
			w.Close()
			a, e := OpenNativeRecovery(root, "campaign-1")
			if e != nil {
				t.Fatal(e)
			}
			digest := contracts.RawDigest([]byte("selection"))
			fresh, _, e := a.BeginEvidence("session-1", digest)
			if e != nil || !fresh {
				t.Fatal(e)
			}
			result := EvidenceResult{Outcome: EvidenceOutcome{SessionID: "session-1", State: "missing", Reason: "export_unavailable", LocalMaxBytes: 1024, NativeMaxBytes: 1024, Recorded: true}}
			if e = a.FinishEvidence(result); e != nil {
				t.Fatal(e)
			}
			a.Close()
			dir := filepath.Join(root, "campaigns/campaign-1/evidence-recovery/session-1")
			switch damage {
			case "lost result":
				os.Remove(filepath.Join(dir, "result.json"))
			case "missing intent":
				os.Remove(filepath.Join(dir, "intent.json"))
			case "changed result":
				os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{}`), 0600)
			case "extra pending":
				os.WriteFile(filepath.Join(dir, "result.json.pending"), []byte(`{}`), 0600)
			}
			a, e = OpenNativeRecovery(root, "campaign-1")
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			fresh, saved, e := a.BeginEvidence("session-1", digest)
			if fresh {
				t.Fatal("reclaimed uncertain or complete collection")
			}
			switch damage {
			case "none":
				if e != nil || saved == nil || saved.Outcome != result.Outcome {
					t.Fatal(saved, e)
				}
			case "lost result":
				if e != nil || saved != nil {
					t.Fatal(saved, e)
				}
			default:
				if e == nil {
					t.Fatal("accepted damaged collection")
				}
			}
			if e = a.FinishEvidence(result); e == nil {
				t.Fatal("published without fresh claim")
			}
		})
	}
}
func TestLateEvidenceRetentionReusesVerifiedBytesWithoutJournalWrites(t *testing.T) {
	ctx := context.Background()
	root, w := newWriter(t)
	dir, e := w.EvidenceDirectory()
	if e != nil {
		t.Fatal(e)
	}
	v := verifiedFixture(t, dir)
	w.Close()
	before, e := Inspect(root, "campaign-1", nil)
	if e != nil {
		t.Fatal(e)
	}
	a, e := OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	fresh, _, e := a.BeginEvidence(v.Receipt().Identity.SessionID, contracts.RawDigest([]byte("selection")))
	if e != nil || !fresh {
		t.Fatal(e)
	}
	if _, e = a.EvidenceDirectory(1 << 20); e != nil {
		t.Fatal(e)
	}
	retained, e := a.RetainEvidence(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.RetainEvidence(ctx, v); e != nil {
		t.Fatal("verified identical bytes not reusable", e)
	}
	if e = a.VerifyRetainedEvidence(ctx, retained); e != nil {
		t.Fatal(e)
	}
	a.available = func(*os.Root) (int64, error) { return 0, nil }
	if _, e = a.EvidenceDirectory(1 << 20); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	a.Close()
	after, e := Inspect(root, "campaign-1", nil)
	if e != nil || before.VerifiedBytes != after.VerifiedBytes || before.VerifiedEvents != after.VerifiedEvents {
		t.Fatal(after, e)
	}
	if e = os.WriteFile(filepath.Join(root, "campaigns/campaign-1", retained.Path), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	a, e = OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = a.VerifyRetainedEvidence(ctx, retained); e == nil {
		t.Fatal("accepted corrupt archive")
	}
}

func TestLateEvidenceRetryPreservesEarlierRecords(t *testing.T) {
	root, w := newWriter(t)
	w.Close()
	a, e := OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	digest := contracts.RawDigest([]byte("selection"))
	if fresh, _, e := a.BeginEvidence("session-1", digest); e != nil || !fresh {
		t.Fatal(e)
	}
	failed := EvidenceResult{Outcome: EvidenceOutcome{SessionID: "session-1", State: "missing", Reason: "export_unavailable", LocalMaxBytes: 1024, NativeMaxBytes: 1024, Recorded: true}}
	if e = a.FinishEvidence(failed); e != nil {
		t.Fatal(e)
	}
	original := filepath.Join(root, "campaigns/campaign-1/evidence-recovery/session-1/result.json")
	before, e := os.ReadFile(original)
	if e != nil {
		t.Fatal(e)
	}
	if fresh, _, e := a.RetryEvidence("session-1", digest); e != nil || !fresh {
		t.Fatal(e)
	}
	failed.Outcome.Reason = "evidence_limit_exceeded"
	if e = a.FinishEvidence(failed); e != nil {
		t.Fatal(e)
	}
	fresh, latest, e := a.BeginEvidence("session-1", digest)
	if e != nil || fresh || latest == nil || latest.Outcome != failed.Outcome {
		t.Fatal(latest, e)
	}
	if fresh, _, e = a.RetryEvidence("session-1", digest); e != nil || fresh {
		t.Fatal("retried unchanged capacity", e)
	}
	after, e := os.ReadFile(original)
	if e != nil || string(before) != string(after) {
		t.Fatal("changed prior result", e)
	}
	if _, _, e = a.BeginEvidence("session-1", contracts.RawDigest([]byte("changed policy"))); e == nil {
		t.Fatal("changed frozen selection")
	}
}
