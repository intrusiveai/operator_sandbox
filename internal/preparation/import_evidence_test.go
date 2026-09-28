package preparation_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
)

func TestEvidenceImportOfflineAndImmutable(t *testing.T) {
	root, w, p, _, target := lateFixture(t)
	id := w.Manifest().CampaignID
	source := filepath.Join(t.TempDir(), "archive.tar")
	if e := os.WriteFile(source, p.export.data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := nativerecovery.ImportEvidence(context.Background(), root, id, target.Identity.SessionID, source, 0); e == nil {
		t.Fatal("active writer accepted")
	}
	w.Close()
	head := read(t, filepath.Join(root, "campaigns", id, "journal-head.json"))
	first, e := nativerecovery.ImportEvidence(context.Background(), root, id, target.Identity.SessionID, source, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	second, e := nativerecovery.ImportEvidence(context.Background(), root, id, target.Identity.SessionID, source, 2<<20)
	if e != nil || first.Archive.Path != second.Archive.Path {
		t.Fatal(second, e)
	}
	if string(head) != string(read(t, filepath.Join(root, "campaigns", id, "journal-head.json"))) {
		t.Fatal("journal changed")
	}
	a, e := campaign.OpenNativeRecovery(root, id)
	if e != nil {
		t.Fatal(e)
	}
	records, e := a.ImportedEvidence()
	a.Close()
	if e != nil || len(records) != 1 {
		t.Fatal(records, e)
	}
	if e = os.WriteFile(filepath.Join(root, "campaigns", id, first.Archive.Path), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = nativerecovery.ImportEvidence(context.Background(), root, id, target.Identity.SessionID, source, 0); e == nil {
		t.Fatal("corrupt retained archive reused")
	}
}

func TestEvidenceImportRejectsUntrustedInputs(t *testing.T) {
	for _, mode := range []string{"foreign-session", "size", "symlink", "malformed", "cancelled", "wrong-lineage"} {
		t.Run(mode, func(t *testing.T) {
			root, w, p, _, target := lateFixture(t)
			w.Close()
			source := filepath.Join(t.TempDir(), "archive.tar")
			raw := p.export.data
			if mode == "malformed" {
				raw = []byte("not a tar archive")
			}
			if mode == "wrong-lineage" {
				raw = read(t, "../interceptor/testdata/native-evidence-restored.tar")
			}
			if e := os.WriteFile(source, raw, 0600); e != nil {
				t.Fatal(e)
			}
			session := target.Identity.SessionID
			maximum := int64(1 << 20)
			if mode == "foreign-session" {
				session = "not-selected"
			}
			if mode == "size" {
				maximum = 1
			}
			if mode == "symlink" {
				link := source + ".link"
				if e := os.Symlink(source, link); e != nil {
					t.Fatal(e)
				}
				source = link
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if _, e := nativerecovery.ImportEvidence(ctx, root, "campaign-1", session, source, maximum); e == nil {
				t.Fatal("accepted", mode)
			}
			a, e := campaign.OpenNativeRecovery(root, "campaign-1")
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			records, e := a.ImportedEvidence()
			if e != nil || len(records) != 0 {
				t.Fatal(records, e)
			}
		})
	}
}
