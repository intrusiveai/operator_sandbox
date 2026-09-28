//go:build linux || darwin

package dockercontrol

import (
	"context"
	"errors"
	"strings"
	"time"

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
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
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
	args := append([]string{"--config", p.directory}, p.args...)
	raw, err := c.run(ctx, p.image.Endpoint, args...)
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 || !imageDigest.MatchString("sha256:"+id) {
		return campaign.DockerBinding{}, ErrLaunch
	}
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: p.manifest.CampaignID, LaunchID: p.manifest.LaunchID, ContainerID: p.manifest.ContainerID, RunManifestDigest: p.manifestDigest, Endpoint: p.image.Endpoint, DaemonID: p.image.DaemonID, DockerContainerID: id, ImageDigest: p.image.ImageID, Labels: p.manifest.DockerLabels()}
	if err != nil {
		// Retain a complete reply for cleanup, but never authorize StartCreated
		// after a failed command, even if the caller saves the returned binding.
		return b, ErrLaunch
	}
	p.actualID.Store(id)
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
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
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

// RemoveStopped removes only an exact, independently confirmed inactive container.
// It never force-removes execution or deletes volumes. Missing/failed replies are
// unknown; a later caller must not substitute a container selected by name.
func (c *Client) RemoveStopped(ctx context.Context, b campaign.DockerBinding) error {
	if c == nil || c.run == nil || b.Validate() != nil {
		return ErrLaunch
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	item, code := c.inspect(ctx, b)
	if code != "" || item.Running || item.Paused || item.Restarting || item.RestartPolicy != "no" || item.AutoRemove {
		return ErrLaunch
	}
	switch item.Status {
	case "created", "exited", "dead":
	default:
		return ErrLaunch
	}
	raw, err := c.run(ctx, b.Endpoint, "container", "rm", b.DockerContainerID)
	if err != nil || strings.TrimSpace(string(raw)) != b.DockerContainerID || c.daemon(ctx, b) != "" {
		return ErrLaunch
	}
	return ctx.Err()
}
