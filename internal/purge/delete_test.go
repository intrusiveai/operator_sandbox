//go:build linux || darwin

package purge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

type fakeServices struct {
	calls int
	fail  bool
}

func (s *fakeServices) ReconcileRetired(_ context.Context, lease *campaign.RetentionLease, root, id, digest string) error {
	s.calls++
	v, e := startrequest.Read(root, id)
	if e != nil || !lease.ExclusiveFor(root) || v.Retired == nil || v.Digest != digest {
		return ErrUnconfirmed
	}
	if s.fail {
		return ErrUnconfirmed
	}
	return nil
}
func TestPurgeCompleteInventoryPreservesReusableInputsAndExternalExports(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "campaign", true)
	s := savedStart(t, root, "campaign")
	lock, e := startrequest.LockRun(s.Request.Selection.RunDirectory)
	if e != nil {
		t.Fatal(e)
	}
	if e = lock.PublishLink(ctx, s, false); e != nil {
		t.Fatal(e)
	}
	lock.Close()
	for _, name := range []string{"config", "skills", "release-cache"} {
		if e = os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(root, name, "keep"), []byte("keep"), 0600)
	}
	output := privateRoot(t)
	copy, e := BeginManagedCopy(ctx, root, "campaign", output)
	if e != nil {
		t.Fatal(e)
	}
	copy.Close()
	external := filepath.Join(output, "explicit-export")
	os.Mkdir(external, 0700)
	os.WriteFile(filepath.Join(external, "keep"), []byte("external"), 0600)
	d := &fakeDocker{state: "exited"}
	services := &fakeServices{}
	got, e := Run(ctx, root, Selection{All: true}, d, services)
	if e != nil || got.Status != "complete" || len(got.Campaigns) != 1 || got.Campaigns[0].Status != "removed" {
		t.Fatal(got, e)
	}
	if services.calls != 1 || len(d.removed) != 1 {
		t.Fatal("cleanup skipped", services, d)
	}
	for _, path := range []string{filepath.Join(root, "campaigns", "campaign"), filepath.Join(root, "starts", s.Request.Selection.StartRequestID), copy.Directory, filepath.Join(root, "managed-copies", "campaign"), filepath.Join(root, "purges", "campaign")} {
		if _, e = os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("retained", path, e)
		}
	}
	for _, path := range []string{filepath.Join(root, "config", "keep"), filepath.Join(root, "skills", "keep"), filepath.Join(root, "release-cache", "keep"), filepath.Join(external, "keep"), filepath.Join(s.Request.Selection.RunDirectory, "start.json"), filepath.Join(root, "retention.lock")} {
		if _, e = os.Stat(path); e != nil {
			t.Fatal("unrelated input removed", path, e)
		}
	}
	if _, _, e = startrequest.ReadLink(s.Request.Selection.RunDirectory); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("dangling lookup became usable", e)
	}
	if _, e = startrequest.ClaimOnce(ctx, root, s.Request.Selection.StartRequestID, s.Digest); e == nil {
		t.Fatal("purged worker claimed")
	}
	again, e := Run(ctx, root, Selection{CampaignID: "campaign"}, nil, nil)
	if e != nil || again.Campaigns[0].Status != "already_absent" {
		t.Fatal(again, e)
	}
}
func TestPartialDeletionRetriesAfterStartAndJournalMetadataAreGone(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "campaign", true)
	s := savedStart(t, root, "campaign")
	d := &fakeDocker{state: "absent"}
	services := &fakeServices{}
	p, e := Prepare(ctx, root, Selection{All: true}, d)
	if e != nil {
		t.Fatal(e)
	}
	p.remove = func(ctx context.Context, g Group) error {
		if g.Kind == "starts" {
			if e := os.Remove(filepath.Join(g.Path, "request.json")); e != nil {
				return e
			}
			return errors.New("injected disk failure")
		}
		return removeGroup(ctx, g)
	}
	first, e := p.Execute(ctx, services)
	p.Close()
	if e == nil || first.Status != "partial" {
		t.Fatal(first, e)
	}
	if _, e = os.Stat(filepath.Join(root, "campaigns", "campaign")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("first group not removed", e)
	}
	if _, e = os.Stat(filepath.Join(root, "purges", "campaign", "plan.json")); e != nil {
		t.Fatal("retry identity lost", e)
	}
	second, e := Run(ctx, root, Selection{All: true}, d, services)
	if e != nil || second.Status != "complete" {
		t.Fatal(second, e)
	}
	if services.calls != 1 {
		t.Fatal("replayed service retirement", services.calls)
	}
	if _, e = os.Stat(filepath.Join(root, "starts", s.Request.Selection.StartRequestID)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if len(d.seen) < 3 || d.seen[len(d.seen)-1].DaemonID != "saved-daemon" {
		t.Fatal("retry lost Docker pin", d.seen)
	}
}
func TestRetirementFailureKeepsEvidenceAndClosesDelayedExecution(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "campaign", false)
	s := savedStart(t, root, "campaign")
	services := &fakeServices{fail: true}
	first, e := Run(ctx, root, Selection{All: true}, nil, services)
	if e == nil || first.Status != "refused" {
		t.Fatal(first, e)
	}
	if _, e = os.Stat(filepath.Join(root, "campaigns", "campaign", "campaign.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = startrequest.ClaimOnce(ctx, root, s.Request.Selection.StartRequestID, s.Digest); !errors.Is(e, startrequest.ErrRetired) {
		t.Fatal("late worker admitted", e)
	}
	services.fail = false
	if result, e := Run(ctx, root, Selection{All: true}, nil, services); e != nil || result.Status != "complete" {
		t.Fatal(result, e)
	}
}
func TestWholeSelectionPreflightDoesNotDeleteInactiveCampaignWhenAnotherIsBusy(t *testing.T) {
	root := privateRoot(t)
	fixture(t, root, "a", false)
	fixture(t, root, "b", true)
	if _, e := Run(context.Background(), root, Selection{All: true}, &fakeDocker{state: "active"}, nil); e == nil {
		t.Fatal("active selection accepted")
	}
	for _, id := range []string{"a", "b"} {
		if _, e := os.Stat(filepath.Join(root, "campaigns", id, "campaign.json")); e != nil {
			t.Fatal("preflight deleted evidence", e)
		}
	}
}
func TestRetryRejectsReplacedDirectoryAndChangedDockerState(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "campaign", true)
	d := &fakeDocker{state: "absent"}
	p, e := Prepare(ctx, root, Selection{All: true}, d)
	if e != nil {
		t.Fatal(e)
	}
	p.remove = func(context.Context, Group) error { return errors.New("injected") }
	if _, e = p.Execute(ctx, nil); e == nil {
		t.Fatal("failure ignored")
	}
	p.Close()
	d.state = "active"
	if _, e = Run(ctx, root, Selection{All: true}, d, nil); e == nil {
		t.Fatal("active retry accepted")
	}
	d.state = "absent"
	path := filepath.Join(root, "campaigns", "campaign")
	old := filepath.Join(privateRoot(t), "old")
	if e = os.Rename(path, old); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(path, "keep"), []byte("new"), 0600)
	if _, e = Run(ctx, root, Selection{All: true}, d, nil); e == nil {
		t.Fatal("replaced directory accepted")
	}
	if _, e = os.Stat(filepath.Join(path, "keep")); e != nil {
		t.Fatal(e)
	}
}
func TestIncompletePlanPublicationIsNotDeletionAuthority(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "campaign", false)
	path := filepath.Join(root, "purges", "campaign")
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "plan.json.pending"), []byte("partial"), 0600)
	out, e := Run(ctx, root, Selection{All: true}, nil, nil)
	if e != nil || out.Status != "complete" {
		t.Fatal(out, e)
	}
}
