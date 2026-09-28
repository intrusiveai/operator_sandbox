//go:build darwin

package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

// This opt-in test registers only its own one-shot job. The request has a missing
// configuration, so the real worker must fail before Docker/native/model access.
func TestLiveLaunchAgentRunsFixedWorker(t *testing.T) {
	if os.Getenv("OPERATOR_LIVE_SERVICE_TEST") != "1" {
		t.Skip("set OPERATOR_LIVE_SERVICE_TEST=1 to exercise the logged-in launchd domain")
	}
	if os.Geteuid() == 0 {
		t.Skip("requires the logged-in non-root account")
	}
	bin := filepath.Join(t.TempDir(), "operatorctl")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/operatorctl")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	s := saved(t)
	c, err := New(bin)
	if err != nil {
		t.Fatal(err)
	}
	id := s.Request.Selection.StartRequestID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := runCommand(ctx, "/bin/launchctl", "bootout", "gui/"+strconv.Itoa(os.Geteuid())+"/ai.intrusive.operator.campaign."+id); err != nil {
			t.Errorf("test LaunchAgent cleanup unconfirmed: %v", err)
		}
	})
	if err := c.Submit(context.Background(), s.Request.StateRoot, id, s.Digest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		value, err := startrequest.Observe(ctx, s.Request.StateRoot, id)
		if err != nil {
			t.Fatal(err)
		}
		if value.Completion != nil {
			if value.Completion.Status != "failed" || value.Completion.Code != "preparation_failed" || value.Accepted != nil {
				t.Fatal(value)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker did not publish completion")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestLiveLaunchAgentRetirementBeforeClaim(t *testing.T) {
	if os.Getenv("OPERATOR_LIVE_SERVICE_TEST") != "1" {
		t.Skip("set OPERATOR_LIVE_SERVICE_TEST=1 for native launchd retirement")
	}
	if os.Geteuid() == 0 {
		t.Skip("requires logged-in non-root account")
	}
	bin := filepath.Join(t.TempDir(), "operatorctl")
	if out, e := exec.Command("go", "build", "-o", bin, "../../cmd/operatorctl").CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, out)
	}
	s := saved(t)
	c, e := New(bin)
	if e != nil {
		t.Fatal(e)
	}
	id, root := s.Request.Selection.StartRequestID, s.Request.StateRoot
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = runCommand(ctx, "/bin/launchctl", "bootout", "gui/"+strconv.Itoa(os.Geteuid())+"/ai.intrusive.operator.campaign."+id)
	})
	// Register without kickstarting, reproducing loss between the manager calls.
	c.run = func(ctx context.Context, bin string, args ...string) error {
		if args[0] == "kickstart" {
			return ErrSubmission
		}
		return runCommand(ctx, bin, args...)
	}
	if e = c.Submit(context.Background(), root, id, s.Digest); !errors.Is(e, ErrSubmission) {
		t.Fatal(e)
	}
	current, e := startrequest.Read(root, id)
	if e != nil || current.Claim != nil {
		t.Fatal(current, e)
	}
	lease, e := campaign.AcquireRetentionLease(root, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = startrequest.Retire(context.Background(), lease, root, id, s.Digest); e != nil {
		t.Fatal(e)
	}
	if e = c.ReconcileRetired(context.Background(), lease, root, id, s.Digest); e != nil {
		t.Fatal(e)
	}
	lease.Close()
	delayed := exec.Command(bin, "_worker", "--state-root", root, "--request-id", id, "--request-digest", s.Digest)
	if e = delayed.Run(); e == nil {
		t.Fatal("late worker executed")
	}
	current, e = startrequest.Read(root, id)
	if e != nil || current.Claim != nil || current.Retired == nil {
		t.Fatal(current, e)
	}
}
