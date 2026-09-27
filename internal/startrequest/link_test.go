//go:build linux || darwin

package startrequest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
)

func linkedRequest(t *testing.T) (Request, Snapshot, *RunLock) {
	t.Helper()
	r := requestFixture(t)
	run := t.TempDir()
	if err := os.Chmod(run, 0700); err != nil {
		t.Fatal(err)
	}
	r.Selection.RunDirectory = run
	saved, err := Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := LockRun(run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	return r, saved, lock
}
func TestRunLinkSelectionAndExplicitNewCampaign(t *testing.T) {
	ctx := context.Background()
	r, saved, lock := linkedRequest(t)
	if _, err := LockRun(r.Selection.RunDirectory); !errors.Is(err, campaign.ErrActive) {
		t.Fatal("concurrent start selection accepted", err)
	}
	if err := lock.PublishLink(ctx, saved, false); err != nil {
		t.Fatal(err)
	}
	if err := lock.PublishLink(ctx, saved, false); err != nil {
		t.Fatal("exact pointer replay", err)
	}
	link, got, err := ReadLink(r.Selection.RunDirectory)
	if err != nil || got.Digest != saved.Digest || link.RequestDigest != saved.Digest {
		t.Fatal(link, got, err)
	}
	next := r
	next.Selection = hostrun.NewSelection(r.Selection.RunDirectory)
	fresh, err := Save(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.PublishLink(ctx, fresh, false); !errors.Is(err, ErrConflict) {
		t.Fatal("implicit campaign replacement", err)
	}
	if err := lock.PublishLink(ctx, fresh, true); err != nil {
		t.Fatal(err)
	}
	_, got, err = ReadLink(r.Selection.RunDirectory)
	if err != nil || got.Digest != fresh.Digest {
		t.Fatal(got, err)
	}
	if _, err := Read(r.StateRoot, r.Selection.StartRequestID); err != nil {
		t.Fatal("historical start lost", err)
	}
	lock.Close()
	if err := lock.PublishLink(ctx, saved, true); err == nil {
		t.Fatal("closed lock reused")
	}
	again, err := LockRun(r.Selection.RunDirectory)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}
func TestRunLinkCannotBeCopiedToAnotherSubmission(t *testing.T) {
	r, saved, lock := linkedRequest(t)
	if err := lock.PublishLink(context.Background(), saved, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(r.Selection.RunDirectory, "start.json"))
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	os.Chmod(other, 0700)
	if err := os.WriteFile(filepath.Join(other, "start.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadLink(other); !errors.Is(err, ErrConflict) {
		t.Fatal("copied run pointer accepted", err)
	}
}
func TestExplicitNewCampaignCanReplacePurgedLookup(t *testing.T) {
	r, saved, lock := linkedRequest(t)
	if err := lock.PublishLink(context.Background(), saved, false); err != nil {
		t.Fatal(err)
	}
	// Remove this test's completed metadata to model an administrative purge.
	if err := os.Remove(filepath.Join(r.StateRoot, "starts", r.Selection.StartRequestID, "request.json")); err != nil {
		t.Fatal(err)
	}
	next := r
	next.Selection = hostrun.NewSelection(r.Selection.RunDirectory)
	fresh, err := Save(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.PublishLink(context.Background(), fresh, false); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced missing-history pointer implicitly", err)
	}
	if err := lock.PublishLink(context.Background(), fresh, true); err != nil {
		t.Fatal(err)
	}
}
func TestRunLockRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "public"} {
		t.Run(kind, func(t *testing.T) {
			run := t.TempDir()
			os.Chmod(run, 0700)
			name := filepath.Join(run, "start.lock")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink("outside", name)
			case "hardlink":
				source := filepath.Join(run, "source")
				if err := os.WriteFile(source, nil, 0600); err != nil {
					t.Fatal(err)
				}
				err = os.Link(source, name)
			case "directory":
				err = os.Mkdir(name, 0700)
			case "public":
				err = os.WriteFile(name, nil, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if lock, err := LockRun(run); err == nil || lock != nil {
				t.Fatal("unsafe start lock accepted")
			}
		})
	}
}
