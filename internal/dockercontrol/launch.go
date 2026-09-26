//go:build linux || darwin

package dockercontrol

import (
	"context"
	"errors"
	"strings"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

var ErrLaunch = errors.New("Docker launch failed or its outcome is unconfirmed")

// Create creates a stopped container from a frozen host plan. The caller MUST
// persist its start intent first and save every returned nonzero binding, even
// alongside an error, before any Start call. A failed create is never retried.
func (c *Client) Create(ctx context.Context, p *LaunchPlan) (campaign.DockerBinding, error) {
	if p == nil || c == nil || c.run == nil || !p.created.CompareAndSwap(false, true) {
		return campaign.DockerBinding{}, ErrLaunch
	}
	if err := p.verify(ctx); err != nil {
		return campaign.DockerBinding{}, err
	}
	if err := c.VerifyImage(ctx, p.image); err != nil {
		return campaign.DockerBinding{}, err
	}
	// Reject image-declared volumes: Docker would silently add writable mounts.
	volumes, err := c.run(ctx, p.image.Endpoint, "image", "inspect", "--platform", p.image.ImagePlatform, "--format", "{{json .Config.Volumes}}", "--", p.image.ImageID)
	if err != nil || (strings.TrimSpace(string(volumes)) != "null" && strings.TrimSpace(string(volumes)) != "{}") {
		return campaign.DockerBinding{}, ErrLaunch
	}
	raw, err := c.run(ctx, p.image.Endpoint, p.args...)
	if err != nil {
		return campaign.DockerBinding{}, ErrLaunch
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 || !imageDigest.MatchString("sha256:"+id) {
		return campaign.DockerBinding{}, ErrLaunch
	}
	p.actualID.Store(id)
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: p.manifest.CampaignID, LaunchID: p.manifest.LaunchID, ContainerID: p.manifest.ContainerID, RunManifestDigest: p.manifestDigest, Endpoint: p.image.Endpoint, DaemonID: p.image.DaemonID, DockerContainerID: id, ImageDigest: p.image.ImageID, Labels: p.manifest.DockerLabels()}
	item, code := c.inspect(ctx, b)
	if code != "" || item.Status != "created" || item.Running || item.Paused || item.Restarting || item.RestartPolicy != "no" || item.AutoRemove {
		return b, ErrLaunch
	}
	if code = c.daemon(ctx, b); code != "" {
		return b, ErrLaunch
	}
	return b, ctx.Err()
}

// StartCreated addresses an already persisted exact binding. It refuses an
// already-started container; retries/recovery never restart campaign execution.
func (c *Client) StartCreated(ctx context.Context, stateRoot string, b campaign.DockerBinding, p *LaunchPlan) error {
	if c == nil || c.run == nil || b.Validate() != nil || p == nil || b.RunManifestDigest != p.manifestDigest || b.Endpoint != p.image.Endpoint || b.DaemonID != p.image.DaemonID || p.actualID.Load() != b.DockerContainerID {
		return ErrLaunch
	}
	saved, err := campaign.ReadDockerBinding(stateRoot, b.CampaignID)
	if err != nil || !sameBinding(saved, b) {
		return ErrLaunch
	}
	item, code := c.inspect(ctx, b)
	if code != "" || item.Status != "created" || item.Running || item.Paused || item.Restarting || item.RestartPolicy != "no" || item.AutoRemove {
		return ErrLaunch
	}
	if err := p.verify(ctx); err != nil {
		return err
	}
	if err := c.VerifyImage(ctx, p.image); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !p.started.CompareAndSwap(false, true) {
		return ErrLaunch
	}
	if _, err = c.run(ctx, b.Endpoint, "container", "start", b.DockerContainerID); err != nil {
		return ErrLaunch
	}
	return c.CheckRunning(ctx, b)
}
func sameBinding(a, b campaign.DockerBinding) bool {
	if a.APIVersion != b.APIVersion || a.CampaignID != b.CampaignID || a.LaunchID != b.LaunchID || a.ContainerID != b.ContainerID || a.RunManifestDigest != b.RunManifestDigest || a.Endpoint != b.Endpoint || a.DaemonID != b.DaemonID || a.DockerContainerID != b.DockerContainerID || a.ImageDigest != b.ImageDigest || len(a.Labels) != len(b.Labels) {
		return false
	}
	for k, v := range a.Labels {
		if b.Labels[k] != v {
			return false
		}
	}
	return true
}
