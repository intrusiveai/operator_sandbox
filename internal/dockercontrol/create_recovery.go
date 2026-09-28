//go:build linux || darwin

package dockercontrol

import (
	"context"
	"sort"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// ResolveCreate discovers one lost create result on the original daemon. Label
// filters only find candidates: full ID, image, labels and lifecycle policy must
// pass inspection before a binding is returned. Zero or multiple candidates,
// disappearing containers and failed replies remain unresolved. No mutation or
// retry is performed, and absence is not proof that a lost create has completed.
func (c *Client) ResolveCreate(ctx context.Context, intent campaign.CreateIntent) (campaign.DockerBinding, error) {
	if c == nil || c.run == nil || ctx.Err() != nil || intent.Validate() != nil {
		return campaign.DockerBinding{}, ErrLaunch
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	daemon := campaign.DockerBinding{Endpoint: intent.Endpoint, DaemonID: intent.DaemonID}
	if c.daemon(ctx, daemon) != "" {
		return campaign.DockerBinding{}, ErrLaunch
	}
	args := []string{"container", "ls", "--all", "--no-trunc"}
	labels := intent.Manifest.DockerLabels()
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--filter", "label="+key+"="+labels[key])
	}
	args = append(args, "--format", "{{json .ID}}")
	raw, err := c.run(ctx, intent.Endpoint, args...)
	if err != nil {
		return campaign.DockerBinding{}, ErrLaunch
	}
	v, err := contracts.Decode(raw, outputLimit)
	id, ok := v.(string)
	if err != nil || !ok {
		return campaign.DockerBinding{}, ErrLaunch
	}
	b, err := intent.Binding(id)
	if err != nil {
		return campaign.DockerBinding{}, ErrLaunch
	}
	item, code := c.inspect(ctx, b)
	if code != "" || item.RestartPolicy != "no" || item.AutoRemove {
		return campaign.DockerBinding{}, ErrLaunch
	}
	if c.daemon(ctx, b) != "" || ctx.Err() != nil {
		return campaign.DockerBinding{}, ErrLaunch
	}
	return b, nil
}
