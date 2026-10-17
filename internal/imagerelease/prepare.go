//go:build linux || darwin

package imagerelease

import (
	"context"
	"github.com/intrusiveai/operator_sandbox/contracts"

	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

type ImageResolver interface {
	ResolveImage(context.Context, string, string, string) (dockercontrol.ImagePin, error)
}

// Prepared retains the local image pin and validated compatibility bytes.
// The launcher must still verify identity and enforce the installed runtime policy.
type Prepared struct {
	Image   dockercontrol.ImagePin
	Release Compatibility
}

// Prepare resolves and inspects only local Docker content. No registry, release
// service, or compatibility cache participates in admission.
func Prepare(ctx context.Context, docker interface {
	ImageResolver
	FileInspector
}, endpoint, selector string, h Requirements, protocol *contracts.Protocol) (Prepared, *Embedded, dockercontrol.ReleaseFiles, error) {
	var inspection dockercontrol.ReleaseFiles
	if err := ctx.Err(); err != nil {
		return Prepared{}, nil, inspection, err
	}
	if h.validate() != nil {
		return Prepared{}, nil, inspection, ErrCompatibility
	}
	if docker == nil {
		return Prepared{}, nil, inspection, dockercontrol.ErrImage
	}
	image, err := docker.ResolveImage(ctx, endpoint, selector, h.HostPlatform)
	if err != nil {
		return Prepared{}, nil, inspection, err
	}
	if image.Validate() != nil || image.Endpoint != endpoint || image.Selector != selector || image.HostPlatform != h.HostPlatform {
		return Prepared{}, nil, inspection, dockercontrol.ErrImageIdentity
	}
	embedded, inspection, err := InspectEmbedded(ctx, docker, image, h, protocol)
	if err != nil {
		return Prepared{}, nil, inspection, err
	}
	return Prepared{Image: image, Release: embedded.Compatibility()}, embedded, inspection, nil
}
