package preparation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/purge"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

type purgeDocker struct{ lateDocker }

func (d *purgeDocker) RemoveStopped(context.Context, campaign.DockerBinding) error {
	return errors.New("fixture container is already absent")
}
func TestPurgeRetainedEvidenceReportsAndInterruptedOutputs(t *testing.T) {
	ctx := context.Background()
	root, w, p, _, target := lateFixture(t)
	d := &purgeDocker{}
	if _, e := purge.Run(ctx, root, purge.Selection{All: true}, d, nil); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("active campaign removed", e)
	}
	w.Close()
	archive := filepath.Join(t.TempDir(), "native.tar")
	if e := os.WriteFile(archive, p.export.data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := nativerecovery.ImportEvidence(ctx, root, "campaign-1", target.Identity.SessionID, archive, 0); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(t.TempDir(), "complete-export")
	if _, e := reporting.Generate(ctx, root, "campaign-1", output); e != nil {
		t.Fatal(e)
	}
	complete := read(t, filepath.Join(output, "export.json"))
	a, e := campaign.OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	staged, e := a.NewDerived()
	if e != nil {
		t.Fatal(e)
	}
	if e = staged.Put(ctx, "report.json", []byte("{}")); e != nil {
		t.Fatal(e)
	}
	if _, e = purge.Run(ctx, root, purge.Selection{All: true}, d, nil); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("report generation removed", e)
	}
	g, digest, e := staged.Commit(ctx)
	if e != nil {
		t.Fatal(e)
	}
	// Exercise a real incomplete external export. It remains an administrator copy.
	os.WriteFile(filepath.Join(root, "campaigns/campaign-1/reports", digest[7:], "report.json"), []byte("changed"), 0600)
	partial := filepath.Join(t.TempDir(), "partial-export")
	if e = a.ExportDerived(ctx, g, digest, partial, nil); e == nil {
		t.Fatal("corrupt export accepted")
	}
	// A process loss can also leave unpublished managed report bytes behind.
	unfinished, e := a.NewDerived()
	if e != nil {
		t.Fatal(e)
	}
	if e = unfinished.Put(ctx, "partial.json", []byte("{}")); e != nil {
		t.Fatal(e)
	}
	a.Close()
	out, e := purge.Run(ctx, root, purge.Selection{All: true}, d, nil)
	if e != nil || out.Status != "complete" {
		t.Fatal(out, e)
	}
	if _, e = os.Stat(filepath.Join(root, "campaigns", "campaign-1")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("campaign retained", e)
	}
	if string(read(t, filepath.Join(output, "export.json"))) != string(complete) {
		t.Fatal("external export changed")
	}
	if _, e = os.Stat(partial); e != nil {
		t.Fatal("incomplete administrator export removed", e)
	}
	if _, e = os.Stat(archive); e != nil {
		t.Fatal("source archive removed", e)
	}
	if _, e = reporting.Generate(ctx, root, "campaign-1", ""); e == nil {
		t.Fatal("report reconstructed after purge")
	}
	if len(p.native.calls) != 0 || p.statusCalls != 0 || len(p.export.calls) != 0 {
		t.Fatal("purge contacted target")
	}
}
