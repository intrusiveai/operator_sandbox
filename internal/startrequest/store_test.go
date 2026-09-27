//go:build linux || darwin

package startrequest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
)

func requestFixture(t *testing.T) Request {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return Request{APIVersion: Version, ConfigurationFile: "/installed/config.yaml", StateRoot: root, DockerEndpoint: "unix:///saved/docker.sock", InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: hostrun.NewSelection("/submitted/run")}
}
func accepted(r Request) hostrun.Receipt {
	return hostrun.Receipt{APIVersion: "operator.dev/campaign-start/v1alpha1", CampaignID: r.Selection.CampaignID, LaunchID: r.Selection.LaunchID, StartRequestID: r.Selection.StartRequestID, ManifestDigest: contracts.RawDigest([]byte("manifest")), Status: "accepted"}
}
func TestStartRequestReplayConflictAndWorkerLifecycle(t *testing.T) {
	ctx := context.Background()
	r := requestFixture(t)
	first, err := Save(ctx, r)
	if err != nil || first.Phase() != "submitted" {
		t.Fatal(first, err)
	}
	again, err := Save(ctx, r)
	if err != nil || again.Digest != first.Digest {
		t.Fatal("repeat changed", err)
	}
	changed := r
	changed.InputsFingerprint = contracts.RawDigest([]byte("changed"))
	if _, err := Save(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed key accepted", err)
	}
	if _, err := ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, changed.InputsFingerprint); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong identity claimed", err)
	}
	o, err := ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if _, err := ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, first.Digest); !errors.Is(err, ErrClaimed) {
		t.Fatal("second worker claimed", err)
	}
	receipt := accepted(r)
	bad := receipt
	bad.CampaignID = "wrong-campaign"
	if err := o.Accept(ctx, bad); err == nil {
		t.Fatal("wrong receipt accepted")
	}
	if err := o.Finish(ctx, "finished", "execution_finished"); err == nil {
		t.Fatal("finished before acceptance")
	}
	for i := 0; i < 2; i++ {
		if err := o.Accept(ctx, receipt); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Read(r.StateRoot, r.Selection.StartRequestID)
	if err != nil || snapshot.Phase() != "accepted" || snapshot.Accepted == nil {
		t.Fatal(snapshot, err)
	}
	bad = receipt
	bad.ManifestDigest = contracts.RawDigest([]byte("changed manifest"))
	if err := o.Accept(ctx, bad); !errors.Is(err, ErrConflict) {
		t.Fatal("acceptance changed", err)
	}
	for i := 0; i < 2; i++ {
		if err := o.Finish(ctx, "finished", "execution_finished"); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.Finish(ctx, "failed", "changed"); !errors.Is(err, ErrConflict) {
		t.Fatal("completion changed", err)
	}
	if err := o.Accept(ctx, receipt); err == nil {
		t.Fatal("accepted after completion")
	}
	o.Close()
	snapshot, err = Save(ctx, r)
	if err != nil || snapshot.Phase() != "finished" {
		t.Fatal("lost acknowledgement lost status", snapshot, err)
	}
	if _, err := ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, first.Digest); !errors.Is(err, ErrClaimed) {
		t.Fatal("completed execution resumed", err)
	}
}
func TestStartRequestConcurrentClaimsHaveOneWinner(t *testing.T) {
	r := requestFixture(t)
	saved, err := Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	owners := make(chan *Owner, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner, err := ClaimOnce(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest)
			if err == nil {
				owners <- owner
			}
		}()
	}
	wg.Wait()
	close(owners)
	count := 0
	for owner := range owners {
		count++
		owner.Close()
	}
	if count != 1 {
		t.Fatal("claim winners", count)
	}
	if snapshot, err := Read(r.StateRoot, r.Selection.StartRequestID); err != nil || snapshot.Phase() != "claimed" {
		t.Fatal("losing claimant damaged record", snapshot, err)
	}
}
func TestCompetingPublisherDoesNotLeavePendingFile(t *testing.T) {
	r := requestFixture(t)
	saved, err := Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := open(r.StateRoot, r.Selection.StartRequestID, false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	raw, _, _ := encode(r)
	if err := put(context.Background(), root, "request.json", raw); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	got, err := Read(r.StateRoot, r.Selection.StartRequestID)
	if err != nil || got.Digest != saved.Digest {
		t.Fatal("competing temporary damaged committed record", err)
	}
}
func TestStartRequestFailureAndCorruptionStayClosed(t *testing.T) {
	for _, mode := range []string{"preparation failed", "pending owner", "tampered request", "symlink", "public file"} {
		t.Run(mode, func(t *testing.T) {
			r := requestFixture(t)
			saved, err := Save(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(r.StateRoot, "starts", r.Selection.StartRequestID)
			switch mode {
			case "preparation failed":
				owner, err := ClaimOnce(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest)
				if err != nil {
					t.Fatal(err)
				}
				if err := owner.Finish(context.Background(), "failed", "preparation_failed"); err != nil {
					t.Fatal(err)
				}
				owner.Close()
				snapshot, err := Read(r.StateRoot, r.Selection.StartRequestID)
				if err != nil || snapshot.Phase() != "failed" || snapshot.Accepted != nil {
					t.Fatal(snapshot, err)
				}
			case "pending owner":
				if err := os.WriteFile(filepath.Join(dir, "owner.json.pending"), []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
			case "tampered request":
				name := filepath.Join(dir, "request.json")
				raw, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.Replace(string(raw), "/submitted/run", "/submitted/new", 1))
				if err := os.WriteFile(name, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				name := filepath.Join(dir, "request.json")
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../outside", name); err != nil {
					t.Fatal(err)
				}
			case "public file":
				if err := os.Chmod(filepath.Join(dir, "request.json"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if owner, err := ClaimOnce(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest); err == nil || owner != nil {
				t.Fatal("uncertain/completed request reopened")
			}
		})
	}
}
func TestWorkerLossDoesNotReleaseStartClaim(t *testing.T) {
	r := requestFixture(t)
	saved, err := Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClaimChildProcess$")
	child.Env = append(os.Environ(), "OPERATOR_START_TEST_ROOT="+r.StateRoot, "OPERATOR_START_TEST_ID="+r.Selection.StartRequestID, "OPERATOR_START_TEST_DIGEST="+saved.Digest)
	pipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	ready, err := bufio.NewReader(pipe).ReadString('\n')
	if err != nil || ready != "claimed\n" {
		t.Fatal(ready, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	if _, err := ClaimOnce(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest); !errors.Is(err, ErrClaimed) {
		t.Fatal("dead worker made request executable again", err)
	}
}
func TestClaimChildProcess(t *testing.T) {
	root := os.Getenv("OPERATOR_START_TEST_ROOT")
	if root == "" {
		return
	}
	owner, err := ClaimOnce(context.Background(), root, os.Getenv("OPERATOR_START_TEST_ID"), os.Getenv("OPERATOR_START_TEST_DIGEST"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	fmt.Println("claimed")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
