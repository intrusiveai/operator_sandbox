//go:build linux || darwin

package purge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

func TestPurgeAfterWorkerLossBeforePreparation(t *testing.T) {
	root := privateRoot(t)
	s := savedStart(t, root, "interrupted")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPurgeClaimChild$")
	child.Env = append(os.Environ(), "OPERATOR_PURGE_ROOT="+root, "OPERATOR_PURGE_START="+s.Request.Selection.StartRequestID, "OPERATOR_PURGE_DIGEST="+s.Digest)
	ready, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	input, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	line, e := bufio.NewReader(ready).ReadString('\n')
	if e != nil || line != "claimed\n" {
		t.Fatal(line, e)
	}
	if _, e = Run(ctx, root, Selection{All: true}, nil, &fakeServices{}); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("live preparation removed", e)
	}
	if e = child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	child.Wait()
	out, e := Run(ctx, root, Selection{All: true}, nil, &fakeServices{})
	if e != nil || out.Status != "complete" {
		t.Fatal(out, e)
	}
	if _, e = startrequest.ClaimOnce(ctx, root, s.Request.Selection.StartRequestID, s.Digest); e == nil {
		t.Fatal("lost worker resumed")
	}
}
func TestPurgeClaimChild(t *testing.T) {
	root := os.Getenv("OPERATOR_PURGE_ROOT")
	if root == "" {
		return
	}
	o, e := startrequest.ClaimOnce(context.Background(), root, os.Getenv("OPERATOR_PURGE_START"), os.Getenv("OPERATOR_PURGE_DIGEST"))
	if e != nil {
		t.Fatal(e)
	}
	defer o.Close()
	fmt.Println("claimed")
	var b [1]byte
	os.Stdin.Read(b[:])
}
func TestPurgeInterruptedPreparationAndPendingRetirement(t *testing.T) {
	root := privateRoot(t)
	s := savedStart(t, root, "early")
	// A native attach intent survives a worker dying before the attach reply;
	// local purge does not contact or delete the native target/session.
	a, e := campaign.CreateAttachment(root, campaign.AttachmentIntent{CampaignID: "early", LaunchID: "launch-1", StartRequestID: s.Request.Selection.StartRequestID, WorkerInstanceID: "worker-1", InputsFingerprint: s.Request.InputsFingerprint})
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	file := filepath.Join(root, "starts", s.Request.Selection.StartRequestID, "retired.json.pending")
	if e = os.WriteFile(file, []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	out, e := Run(context.Background(), root, Selection{CampaignID: "early"}, nil, &fakeServices{})
	if e != nil || out.Status != "complete" || len(out.Campaigns[0].Groups) != 2 {
		t.Fatal(out, e)
	}
}
func TestPurgeNeverUsesMissingDockerIdentityAsAbsence(t *testing.T) {
	root := privateRoot(t)
	fixture(t, root, "campaign", true)
	// Losing both independent binding and journal integrity cannot authorize purge.
	os.Remove(filepath.Join(root, "campaigns", "campaign", "launch", "docker-binding.json"))
	os.WriteFile(filepath.Join(root, "campaigns", "campaign", "journal-head.json"), []byte("damaged"), 0600)
	d := &fakeDocker{state: "absent"}
	out, e := Run(context.Background(), root, Selection{All: true}, d, nil)
	if e == nil || out.Status != "refused" || out.SelectionComplete || len(out.Selected) != 1 || len(d.seen) != 0 {
		t.Fatal(out, e, d)
	}
	if _, e = os.Stat(filepath.Join(root, "campaigns", "campaign", "campaign.json")); e != nil {
		t.Fatal("unknown launch deleted", e)
	}
}

func TestPurgeAbsentInstallationIsAlreadyAbsentWithoutCreatingIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-state")
	for _, selection := range []Selection{{CampaignID: "absent"}, {All: true}} {
		out, e := Run(context.Background(), root, selection, nil, nil)
		if e != nil || out.Status != "complete" || !out.SelectionComplete {
			t.Fatal(out, e)
		}
		if !selection.All && out.Campaigns[0].Status != "already_absent" {
			t.Fatal(out)
		}
		if _, e = os.Lstat(root); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("absent root created", e)
		}
	}
}

func TestPendingPurgePreventsNewRegistrationsOutsideItsPlan(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "campaign", false)
	s := savedStart(t, root, "campaign")
	p, e := Prepare(ctx, root, Selection{All: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	p.remove = func(context.Context, Group) error { return errors.New("injected") }
	if _, e = p.Execute(ctx, &fakeServices{}); e == nil {
		t.Fatal("injected failure ignored")
	}
	p.Close()
	if _, e = BeginManagedCopy(ctx, root, "campaign", privateRoot(t)); !errors.Is(e, campaign.ErrClosed) {
		t.Fatal("new managed copy escaped saved plan", e)
	}
	if _, e = startrequest.Save(ctx, s.Request); !errors.Is(e, campaign.ErrClosed) {
		t.Fatal("new start escaped saved plan", e)
	}
	if _, e = campaign.CreateAttachment(root, campaign.AttachmentIntent{CampaignID: "campaign", LaunchID: "new-launch", StartRequestID: s.Request.Selection.StartRequestID, WorkerInstanceID: "new-worker", InputsFingerprint: s.Request.InputsFingerprint}); !errors.Is(e, campaign.ErrClosed) {
		t.Fatal("new preparation escaped saved plan", e)
	}
	// Existing evidence remains readable while an administrator resolves the purge.
	a, e := campaign.OpenNativeRecovery(root, "campaign")
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	out, e := Run(ctx, root, Selection{All: true}, nil, &fakeServices{})
	if e != nil || out.Status != "complete" {
		t.Fatal(out, e)
	}
}
