//go:build linux || darwin

package campaign

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func TestDerivedPublicationRefusesChangedBytesAndUnsafePaths(t *testing.T) {
	root, w := newWriter(t)
	w.Close()
	a, e := OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	d, e := a.NewDerived()
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	ctx := context.Background()
	raw := []byte("verified output")
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", "a/b/c", "generation.json"} {
		if e = d.Put(ctx, name, raw); e == nil {
			t.Fatal(name)
		}
	}
	if e = d.Copy(ctx, "bad.json", bytes.NewReader([]byte("changed")), int64(len(raw)), contracts.RawDigest(raw)); !errors.Is(e, ErrCorrupt) {
		t.Fatal(e)
	}
	// A failed staged file is never included in a published generation.
	if _, _, e = d.Commit(ctx); e == nil {
		t.Fatal("published failed staging")
	}
}

func TestDerivedSpaceAndInterruptedExport(t *testing.T) {
	root, w := newWriter(t)
	w.Close()
	a, e := OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	d, e := a.NewDerived()
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	available := a.available
	a.available = func(*os.Root) (int64, error) { return DerivedManifestLimit - 1, nil }
	if e = d.Put(context.Background(), "report.json", []byte("{}")); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	a.available = available
	if e = d.Put(context.Background(), "report.json", []byte("{}")); e != nil {
		t.Fatal(e)
	}
	g, digest, e := d.Commit(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(root, "campaigns/campaign-1/reports", digest[7:], "report.json")
	if e = os.WriteFile(name, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(t.TempDir(), "export")
	if e = a.ExportDerived(context.Background(), g, digest, output, nil); e == nil {
		t.Fatal("export accepted corruption")
	}
	if _, e = os.Stat(filepath.Join(output, "export.json")); !os.IsNotExist(e) {
		t.Fatal("partial export marked complete", e)
	}
	if e = a.ExportDerived(context.Background(), g, digest, output, nil); e == nil {
		t.Fatal("overwrote partial export")
	}
}
