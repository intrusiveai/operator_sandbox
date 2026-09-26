//go:build linux || darwin

package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func verifiedFixture(t *testing.T, dir string) *interceptor.VerifiedEvidence {
	t.Helper()
	raw, err := os.ReadFile("../interceptor/testdata/native-evidence.tar")
	if err != nil {
		t.Fatal(err)
	}
	d, err := interceptor.StageEvidence(context.Background(), interceptor.EvidenceReceipt{CampaignID: "campaign-1", SessionID: "sess-1-000000000000000000000000", Bytes: int64(len(raw)), SHA256: contracts.RawDigest(raw), LocalMaxBytes: 4 << 30, InterceptorMaxBytes: 4 << 30}, bytes.NewReader(raw), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	a, err := d.InspectArchive(context.Background(), interceptor.DefaultArchiveLimits(4<<30))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := a.Reader("session.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta interceptor.Session
	if err = json.NewDecoder(reader).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	// Fixture identity only. Production obtains these from saved attachment/restore.
	v, err := a.VerifyProvenance(context.Background(), interceptor.EvidenceIdentity{CampaignID: meta.CampaignID, SessionID: meta.ID, EnvironmentDigest: meta.EnvironmentDigest, ApplicationDigest: meta.AppDigest, CapabilityDigest: meta.CapabilityManifestDigest, FeedbackProfile: meta.FeedbackProfile})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestRetainedNativeEvidencePublicationAndCorruption(t *testing.T) {
	root, w := newWriter(t)
	if err := w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	v := verifiedFixture(t, dir)
	_, err = w.AppendReserving(Entry{RunRevision: 3, Kind: "evidence.reserve", Metadata: json.RawMessage(`{}`)}, "export", 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	record, err := w.RetainEvidence(context.Background(), v, "export")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.VerifyRetainedEvidence(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "campaigns", "campaign-1", filepath.FromSlash(record.Path))
	if info, err := os.Stat(name); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if err = os.WriteFile(name, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = w.VerifyRetainedEvidence(context.Background(), record); err == nil {
		t.Fatal("changed retained archive accepted")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	adopted := false
	report, err := Inspect(root, "campaign-1", func(e Event) error {
		if e.Kind == "evidence.adopted" {
			adopted = true
		}
		return nil
	})
	// Journal integrity and external archive integrity are separate checks.
	if err != nil || !report.JournalIntact || !adopted {
		t.Fatal(report, err, adopted)
	}
}
func TestRetainedEvidenceFailedAdoptionIsNotPublished(t *testing.T) {
	root, w := newWriter(t)
	if err := w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	v := verifiedFixture(t, dir)
	if _, err = w.RetainEvidence(context.Background(), v, "missing-reservation"); err == nil {
		t.Fatal("unreserved archive adopted")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Inspect(root, "campaign-1", func(e Event) error {
		if e.Kind == "evidence.adopted" {
			t.Fatal("failed staging published")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestRetainedEvidenceRejectsCancellationAndDirectoryLink(t *testing.T) {
	root, w := newWriter(t)
	staging := privateRoot(t)
	v := verifiedFixture(t, staging)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.RetainEvidence(ctx, v, ""); err == nil {
		t.Fatal("cancelled import accepted")
	}
	if err := os.Symlink(staging, filepath.Join(root, "campaigns", "campaign-1", "native-evidence")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RetainEvidence(context.Background(), v, ""); err == nil {
		t.Fatal("symlinked storage accepted")
	}
}
