//go:build linux || darwin

package dockercontrol

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// ObserveExit reads the exact stopped container before removal. Unknown is never zero.
func (c *Client) ObserveExit(ctx context.Context, b campaign.DockerBinding) campaign.HarnessExit {
	h := campaign.HarnessExit{ExitUnavailable: "invalid_binding", StderrState: "unavailable", Details: "campaign journal event launch.exit"}
	if c == nil || c.run == nil || b.Validate() != nil {
		return h
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	item, code := c.inspect(ctx, b)
	if code != "" {
		h.ExitUnavailable = code
		return h
	}
	if item.Status != "exited" || item.Running || item.Paused || item.Restarting || item.RestartPolicy != "no" || item.AutoRemove {
		h.ExitUnavailable = "container_not_confirmed_exited"
		return h
	}
	raw, err := c.run(ctx, b.Endpoint, "container", "inspect", "--format", "{{.State.ExitCode}}", b.DockerContainerID)
	n, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	h.ExitUnavailable = "exit_status_unavailable"
	if err == nil && parseErr == nil && n >= 0 && n <= 255 && c.daemon(ctx, b) == "" && ctx.Err() == nil {
		h.ExitObserved = true
		h.ExitCode = n
		h.ExitUnavailable = ""
	}
	if c.stderr != nil {
		raw, err = c.stderr(ctx, b.Endpoint, "container", "logs", "--tail", "200", b.DockerContainerID)
		if err == nil && c.daemon(ctx, b) == "" && ctx.Err() == nil {
			h.Stderr = string(raw)
			h.StderrTruncated = len(raw) > 8<<10
			h.StderrState = "retained"
			if h.Stderr == "" {
				h.StderrState = "empty"
			}
		}
	}

	return h
}

// Docker logs demultiplexes non-TTY container stderr onto the CLI's stderr.
// Failed commands are not interpreted as guest diagnostics. Both daemon storage
// and this reader are bounded; stdout is discarded.
func dockerStderr(executable string) command {
	return func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, executable, append([]string{"--host", endpoint}, args...)...)
		cmd.Env = dockerEnvironment(os.Environ())
		cmd.WaitDelay = 100 * time.Millisecond
		out := &boundedOutput{limit: (8 << 10) + 1}
		cmd.Stdout = io.Discard
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
}
