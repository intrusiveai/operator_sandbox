//go:build darwin

package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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
