//go:build linux || darwin

// Package dockercontrol provides the narrow, exact-identity administrative kill
// path. It never selects containers by name, changes context, pulls, or starts one.
package dockercontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
)

const ConfirmationTimeout = 5 * time.Second
const outputLimit = 64 << 10

type Outcome = campaign.TerminationOutcome

type command func(context.Context, string, ...string) ([]byte, error)

type Client struct{ run command }

// New resolves the administrator's Docker executable once. An explicit path must
// be absolute. Neither the executable nor arguments come from campaign content.
func New(executable string) (*Client, error) {
	if executable == "" {
		var err error
		executable, err = exec.LookPath("docker")
		if err != nil {
			return nil, errors.New("Docker CLI unavailable")
		}
	}
	if !filepath.IsAbs(executable) {
		return nil, errors.New("Docker executable must be absolute")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, errors.New("Docker executable unavailable")
	}
	return &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, executable, append([]string{"--host", endpoint}, args...)...)
		cmd.Env = dockerEnvironment(os.Environ())
		// Context cancellation kills the CLI child. Bound pipe draining too, in case
		// an unexpected child inherited a descriptor. Docker failure stays unconfirmed.
		cmd.WaitDelay = 100 * time.Millisecond
		out := &boundedOutput{}
		cmd.Stdout, cmd.Stderr = out, io.Discard
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		if out.overflow {
			return nil, errors.New("Docker output limit")
		}
		return out.Bytes(), nil
	}}, nil
}

func dockerEnvironment(env []string) []string {
	result := make([]string, 0, len(env))
	for _, v := range env {
		key, _, _ := strings.Cut(v, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "DOCKER_") || upper == "HTTP_PROXY" || upper == "HTTPS_PROXY" || upper == "ALL_PROXY" || upper == "NO_PROXY" {
			continue
		}
		result = append(result, v)
	}
	return result
}

type boundedOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	available := outputLimit - b.buffer.Len()
	if len(p) > available {
		b.overflow = true
		p = p[:available]
	}
	b.buffer.Write(p)
	return n, nil // Keep draining without unbounded allocation or inherited-pipe stalls.
}

// Project only the required identity/state fields, never container environment or
// mounts. CLI defaults and user-configured output templates cannot change this.
const inspectFormat = `{"id":{{json .Id}},"image":{{json .Image}},"labels":{{json .Config.Labels}},"status":{{json .State.Status}},"running":{{json .State.Running}},"paused":{{json .State.Paused}},"restarting":{{json .State.Restarting}},"restart_policy":{{json .HostConfig.RestartPolicy.Name}},"auto_remove":{{json .HostConfig.AutoRemove}}}`

type identity struct {
	ID            string            `json:"id"`
	Image         string            `json:"image"`
	Labels        map[string]string `json:"labels"`
	Status        string            `json:"status"`
	Running       bool              `json:"running"`
	Paused        bool              `json:"paused"`
	Restarting    bool              `json:"restarting"`
	RestartPolicy string            `json:"restart_policy"`
	AutoRemove    bool              `json:"auto_remove"`
}

func (c *Client) daemon(ctx context.Context, b campaign.DockerBinding) string {
	raw, err := c.run(ctx, b.Endpoint, "info", "--format", "{{json .ID}}")
	if err != nil {
		return "docker_unavailable"
	}
	v, err := contracts.Decode(raw, outputLimit)
	if err != nil {
		return "invalid_response"
	}
	id, ok := v.(string)
	if !ok || id == "" {
		return "invalid_response"
	}
	if id != b.DaemonID {
		return "identity_mismatch"
	}
	return ""
}

func (c *Client) inspect(ctx context.Context, b campaign.DockerBinding) (identity, string) {
	var item identity
	if code := c.daemon(ctx, b); code != "" {
		return item, code
	}
	raw, err := c.run(ctx, b.Endpoint, "container", "inspect", "--format", inspectFormat, b.DockerContainerID)
	if err != nil {
		return item, "container_unconfirmed"
	}
	// A strict round trip rejects absent/null booleans, duplicate/unknown fields,
	// and wrong scalar types while allowing arbitrary required-label ordering.
	canonical, err := contracts.Canonicalize(raw, outputLimit)
	if err != nil || json.Unmarshal(raw, &item) != nil {
		return item, "invalid_response"
	}
	roundTrip, _ := json.Marshal(item)
	roundTrip, err = contracts.Canonicalize(roundTrip, outputLimit)
	if err != nil || !bytes.Equal(canonical, roundTrip) {
		return item, "invalid_response"
	}
	if item.ID != b.DockerContainerID || item.Image != b.ImageDigest {
		return item, "identity_mismatch"
	}
	for k, v := range b.Labels {
		if item.Labels[k] != v {
			return item, "identity_mismatch"
		}
	}
	return item, ""
}

// Terminate attempts at most one SIGKILL and confirms by inspect, never by kill's
// exit code. Missing containers have no removal provenance and remain unconfirmed.
// The caller may pass a shorter context; no request extends the five-second bound.
func (c *Client) Terminate(ctx context.Context, b campaign.DockerBinding) (out Outcome) {
	out = Outcome{State: "unknown", Code: "invalid_binding"}
	if b.Validate() != nil || c == nil || c.run == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			out.Confirmed = false
			out.Code = "deadline_or_cancellation"
		}
	}()
	policyChanged := false
	for {
		if ctx.Err() != nil {
			return out
		}
		item, code := c.inspect(ctx, b)
		if code != "" {
			out.Code = code
			return out
		}
		policyChanged = policyChanged || item.RestartPolicy != "no" || item.AutoRemove
		if !item.Running && !item.Paused && !item.Restarting && (item.Status == "exited" || item.Status == "dead" || item.Status == "created") {
			// Recheck the daemon after observing stopped state, including the no-kill
			// path. A changed/unavailable daemon cannot establish exit provenance.
			if code := c.daemon(ctx, b); code != "" {
				out.Code = code
				return out
			}
			out.State = item.Status
			if policyChanged {
				out.Code = "lifecycle_policy_mismatch"
				return out
			}
			out.Confirmed, out.Code = true, "confirmed_stopped"
			return out
		}
		if !out.KillAttempted {
			// Even a failed kill command may have taken effect; inspect within the
			// original deadline. Never retry against another ID or daemon.
			out.KillAttempted = true
			_, _ = c.run(ctx, b.Endpoint, "container", "kill", "--signal", "SIGKILL", b.DockerContainerID)
			continue
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out
		case <-timer.C:
		}
	}
}
