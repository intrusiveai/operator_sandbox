//go:build linux || darwin

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func transientFixture(t *testing.T, retained bool) (string, *Writer, string, string) {
	t.Helper()
	root, w := newWriter(t)
	dir := filepath.Join(root, "campaigns", w.manifest.CampaignID)
	stage := "launch/stage-" + strings.Repeat("a", 26)
	for _, p := range []string{"runtime/transport/out/nested", stage + "/input", "launch/policies/launch-policy-123", "artifacts/keep"} {
		if e := os.MkdirAll(filepath.Join(dir, p), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []string{"runtime/transport/out/nested/data", stage + "/input/data", "launch/policies/launch-policy-123/config.json", "artifacts/keep/data"} {
		if e := os.WriteFile(filepath.Join(dir, p), []byte("retained"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if retained {
		meta, _ := json.Marshal(map[string]any{"input_tree_digest": w.manifest.InputTreeDigest, "skill_set_digest": w.manifest.SkillSetDigest, "files": 1, "size_bytes": 8})
		appendOK(t, w, Entry{RunRevision: 3, Kind: "campaign.launch-inputs-retained", Metadata: meta, Content: []Content{{Role: "input-part", MediaType: "application/octet-stream", Bytes: []byte("retained")}}})
	}
	return root, w, dir, stage
}
func TestTransientCleanupPreservesEvidenceAndUnlinksHostileEntries(t *testing.T) {
	root, w, dir, stage := transientFixture(t, true)
	if e := os.Symlink("../../../artifacts/keep", filepath.Join(dir, "runtime/transport/out/link")); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(filepath.Join(dir, "runtime/transport/out/fifo"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{stage + "/input", stage} {
		if e := os.Chmod(filepath.Join(dir, p), 0555); e != nil {
			t.Fatal(e)
		}
	}
	w.Close()
	before, e := Inspect(root, "campaign-1", nil)
	if e != nil {
		t.Fatal(e)
	}
	out, e := CleanupTransient(context.Background(), root, "campaign-1", true)
	if e != nil || out.Transport != "removed" || out.Inputs != "removed" || out.Policies != "removed" {
		t.Fatal(out, e)
	}
	if raw, e := os.ReadFile(filepath.Join(dir, "artifacts/keep/data")); e != nil || string(raw) != "retained" {
		t.Fatal("evidence changed", e)
	}
	after, e := Inspect(root, "campaign-1", nil)
	if e != nil || before.VerifiedEvents != after.VerifiedEvents || before.VerifiedBytes != after.VerifiedBytes {
		t.Fatal(after, e)
	}
	out, e = CleanupTransient(context.Background(), root, "campaign-1", true)
	if e != nil || out.Transport != "absent" || out.Inputs != "absent" {
		t.Fatal(out, e)
	}
}
func TestTransientCleanupRetainsUnarchivedInputs(t *testing.T) {
	for _, kind := range []string{"missing retention", "damaged journal"} {
		t.Run(kind, func(t *testing.T) {
			root, w, dir, stage := transientFixture(t, kind == "damaged journal")
			w.Close()
			if kind == "damaged journal" {
				os.WriteFile(filepath.Join(dir, "journal-head.json"), []byte("bad"), 0600)
			}
			out, e := CleanupTransient(context.Background(), root, "campaign-1", true)
			if e != nil || out.Inputs != "retained-unarchived" || out.Transport != "removed" {
				t.Fatal(out, e)
			}
			if _, e := os.Stat(filepath.Join(dir, stage, "input/data")); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestTransientCleanupRejectsUnsafeAdmissionAndRoots(t *testing.T) {
	for _, kind := range []string{"active writer", "unknown container", "cancelled", "symlink root", "partial intent"} {
		t.Run(kind, func(t *testing.T) {
			root, w, dir, _ := transientFixture(t, false)
			if kind != "active writer" {
				w.Close()
			}
			ctx := context.Background()
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if kind == "symlink root" {
				os.Rename(filepath.Join(dir, "runtime/transport"), filepath.Join(dir, "runtime/saved"))
				os.Symlink("saved", filepath.Join(dir, "runtime/transport"))
			}
			if kind == "partial intent" {
				os.WriteFile(filepath.Join(dir, "transient-cleanup-intent.json.pending"), []byte("partial"), 0600)
			}
			_, e := CleanupTransient(ctx, root, "campaign-1", kind != "unknown container")
			if e == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			name := "runtime/transport/out/nested/data"
			if kind == "symlink root" {
				name = "runtime/saved/out/nested/data"
			}
			if _, e := os.Stat(filepath.Join(dir, name)); e != nil {
				t.Fatal("removed unsafe root", e)
			}
		})
	}
}
func TestTransientTraversalBound(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "tree"), 0700)
	os.WriteFile(filepath.Join(dir, "tree/data"), nil, 0600)
	r, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	i, _ := r.Stat(".")
	budget := 1
	if e := removeTransientTree(context.Background(), r, "tree", uint64(i.Sys().(*syscall.Stat_t).Dev), 0, &budget); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
}
