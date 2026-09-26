//go:build linux || darwin

package dockercontrol

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

var ErrEvents = errors.New("Docker event stream lost or target execution interrupted")

// WatchEvents is a single subscription, never reconnected. since precedes create,
// so an exit racing subscription setup is replayed. The caller must independently
// check current exact identity before admission. Cancellation is intentional only
// after the campaign has fenced; events cannot reopen a campaign.
func (c *Client) WatchEvents(ctx context.Context, b campaign.DockerBinding, since time.Time) error {
	if c == nil || c.executable == "" || b.Validate() != nil || since.IsZero() || c.daemon(ctx, b) != "" {
		return ErrEvents
	}
	cmd := exec.CommandContext(ctx, c.executable, "--host", b.Endpoint, "events", "--since", strconv.FormatInt(since.Unix(), 10), "--filter", "type=container", "--filter", "container="+b.DockerContainerID, "--format", `{"id":{{json .Actor.ID}},"action":{{json .Action}}}`)
	cmd.Env = dockerEnvironment(os.Environ())
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ErrEvents
	}
	if err = cmd.Start(); err != nil {
		return ErrEvents
	}
	defer func() { _ = cmd.Process.Kill(); _ = pipe.Close(); _ = cmd.Wait() }()
	return readEvents(ctx, pipe, b.DockerContainerID)
}
func readEvents(ctx context.Context, r io.Reader, id string) error {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), outputLimit)
	for scan.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v, err := contracts.Decode(scan.Bytes(), outputLimit)
		if err != nil {
			return ErrEvents
		}
		m, ok := v.(map[string]any)
		if !ok || len(m) != 2 || m["id"] != id {
			return ErrEvents
		}
		action, ok := m["action"].(string)
		if !ok {
			return ErrEvents
		}
		// Only the expected create/start lifecycle is benign. Exec, pause, kill,
		// rename, removal and resource changes all invalidate the launch assumptions.
		switch action {
		case "create", "start":
		default:
			return ErrEvents
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrEvents
}
