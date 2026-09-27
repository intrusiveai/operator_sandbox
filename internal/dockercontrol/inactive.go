//go:build linux || darwin

package dockercontrol

import (
	"bytes"
	"context"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// Inactivity is a current-daemon observation, not proof of historical cleanup,
// successful effects, journal integrity, or permission to resume the campaign.
type Inactivity struct {
	Confirmed bool   `json:"confirmed"`
	State     string `json:"state"` // absent, created, exited, dead, active or unknown.
	Code      string `json:"code"`
}

// CheckInactive establishes absence separately from an inspect error. It queries
// all states by full immutable ID, requires exact results, and verifies the saved
// daemon before and after. It never kills, removes, starts or selects by name.
func (c *Client) CheckInactive(ctx context.Context, b campaign.DockerBinding) (out Inactivity) {
	out = Inactivity{State: "unknown", Code: "invalid_binding"}
	if c == nil || c.run == nil || b.Validate() != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			out = Inactivity{State: "unknown", Code: "deadline_or_cancellation"}
		}
	}()
	if code := c.daemon(ctx, b); code != "" {
		out.Code = code
		return out
	}
	raw, err := c.run(ctx, b.Endpoint, "container", "ls", "--all", "--no-trunc", "--filter", "id="+b.DockerContainerID, "--format", "{{json .ID}}")
	if err != nil {
		out.Code = "container_unconfirmed"
		return out
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		if code := c.daemon(ctx, b); code != "" {
			out.Code = code
			return out
		}
		return Inactivity{Confirmed: true, State: "absent", Code: "confirmed_absent"}
	}
	value, err := contracts.Decode(raw, outputLimit)
	if err != nil || value != b.DockerContainerID {
		out.Code = "invalid_response"
		return out
	}
	item, code := c.inspect(ctx, b)
	if code != "" {
		out.Code = code
		return out
	}
	if code := c.daemon(ctx, b); code != "" {
		out.Code = code
		return out
	}
	if item.RestartPolicy != "no" || item.AutoRemove {
		out.Code = "lifecycle_policy_mismatch"
		return out
	}
	if item.Running || item.Paused || item.Restarting {
		return Inactivity{State: "active", Code: "execution_present"}
	}
	switch item.Status {
	case "created", "exited", "dead":
		return Inactivity{Confirmed: true, State: item.Status, Code: "confirmed_inactive"}
	default:
		out.Code = "container_unconfirmed"
		return out
	}
}
