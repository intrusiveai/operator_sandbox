//go:build linux || darwin

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

var ErrRetirement = errors.New("worker service retirement unconfirmed; retained records must not be purged")

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256<<10 {
		b.exceeded = true
		return 0, ErrRetirement
	}
	return b.Buffer.Write(p)
}
func queryCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = 100 * time.Millisecond
	e := cmd.Run()
	if out.exceeded {
		return nil, ErrRetirement
	}
	return out.Bytes(), e
}

// ReconcileRetired removes only the exact deterministic worker registration.
// Retirement is durable before this call; a late worker cannot claim execution.
// Manager errors never count as absence unless an explicit follow-up probe proves it.
func (c *Client) ReconcileRetired(ctx context.Context, lease *campaign.RetentionLease, root, id, digest string) error {
	if !lease.ExclusiveFor(root) {
		return campaign.ErrActive
	}
	s, e := startrequest.Read(root, id)
	if e != nil || s.Digest != digest || s.Retired == nil {
		return startrequest.ErrRecord
	}
	if s.Service != nil && (s.Service.Platform != c.goos || s.Service.UID != c.uid) {
		return ErrRetirement
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	query := c.query
	if query == nil {
		query = queryCommand
	}
	switch c.goos {
	case "linux":
		scope := "--user"
		if c.uid == 0 {
			scope = "--system"
		}
		unit := "operator-campaign-" + id + ".service"
		absent := func() bool {
			raw, e := query(ctx, "/usr/bin/systemctl", scope, "--no-ask-password", "show", "--property=LoadState", "--value", unit)
			// systemctl may return exit 4 for an unloaded unit. Require its explicit
			// state, never merely a nonzero exit or an empty response.
			return ctx.Err() == nil && strings.TrimSpace(string(raw)) == "not-found" && (e == nil || exitCode(e) == 4)
		}
		if absent() {
			return nil
		}
		_ = c.run(ctx, "/usr/bin/systemctl", scope, "--no-ask-password", "stop", unit)
		_ = c.run(ctx, "/usr/bin/systemctl", scope, "--no-ask-password", "reset-failed", unit)
		if absent() {
			return nil
		}
	case "darwin":
		if c.uid <= 0 {
			return ErrInstallation
		}
		label := "ai.intrusive.operator.campaign." + id
		target := "gui/" + strconv.Itoa(c.uid) + "/" + label
		absent := func() bool {
			raw, e := query(ctx, "/bin/launchctl", "print", target)
			want := "Bad request.\nCould not find service \"" + label + "\" in domain for user gui: " + strconv.Itoa(c.uid)
			return ctx.Err() == nil && exitCode(e) == 113 && strings.TrimSpace(string(raw)) == want
		}
		if absent() {
			return nil
		}
		_ = c.run(ctx, "/bin/launchctl", "bootout", target)
		if absent() {
			return nil
		}
	default:
		return ErrInstallation
	}
	return ErrRetirement
}
func exitCode(err error) int {
	var e interface{ ExitCode() int }
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return -1
}
