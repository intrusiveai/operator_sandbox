//go:build linux || darwin

package imagerelease

import (
	"context"

	"github.com/intrusive-ai/operator-sandbox/internal/dockercontrol"
)

type ImageResolver interface {
	ResolveImage(context.Context, string, string, string) (dockercontrol.ImagePin, error)
}

// Prepared retains selection provenance and immutable metadata for the later
// launcher. The embedded engine files, host policy and runtime still need checking.
type Prepared struct {
	Image   dockercontrol.ImagePin
	Release Approval
}

// Prepare inspects local Docker even when approval is cached. Missing local image
// content cannot be replaced by an offline release record. It creates no container.
func (c *Client) Prepare(ctx context.Context, docker ImageResolver, endpoint, selector string, h Requirements) (Prepared, error) {
	if h.validate() != nil {
		return Prepared{}, ErrCompatibility
	}
	if docker == nil {
		return Prepared{}, dockercontrol.ErrImage
	}
	image, err := docker.ResolveImage(ctx, endpoint, selector, h.HostPlatform)
	if err != nil {
		return Prepared{}, err
	}
	if image.Validate() != nil || image.Endpoint != endpoint || image.Selector != selector || image.HostPlatform != h.HostPlatform {
		return Prepared{}, dockercontrol.ErrImageIdentity
	}
	release, err := c.Resolve(ctx, image.ImageID, h)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{image, release}, nil
}
