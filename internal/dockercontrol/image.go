//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
)

var (
	ErrImage         = errors.New("cannot resolve local image; install the selected native-platform image and ensure Docker supports platform-specific image inspection")
	ErrImageIdentity = errors.New("Docker image or daemon identity changed")
	ErrPlatform      = errors.New("unsupported or mismatched host/image platform")
	ErrImageSelector = errors.New("invalid local Docker image selector or endpoint")
)

var imageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var daemonID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var imageSelector = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,511}$`)

// ImagePin is host preparation metadata. Repository digests are attribution only;
// ImageID (the config digest) is the immutable create/release identity.
type ImagePin struct {
	Selector      string   `json:"selector"`
	Endpoint      string   `json:"endpoint"`
	DaemonID      string   `json:"daemon_id"`
	ImageID       string   `json:"image_id"`
	HostPlatform  string   `json:"host_platform"`
	ImagePlatform string   `json:"image_platform"`
	RepoDigests   []string `json:"repo_digests"`
}

func ImagePlatform(host string) (string, error) {
	switch host {
	case "linux/amd64", "darwin/amd64":
		return "linux/amd64", nil
	case "linux/arm64", "darwin/arm64":
		return "linux/arm64", nil
	default:
		return "", ErrPlatform
	}
}

func (p ImagePin) Validate() error {
	platform, err := ImagePlatform(p.HostPlatform)
	if err != nil || p.ImagePlatform != platform {
		return ErrPlatform
	}
	if campaign.ValidateDockerEndpoint(p.Endpoint) != nil || !validSelector(p.Selector) || !daemonID.MatchString(p.DaemonID) || !imageDigest.MatchString(p.ImageID) {
		return ErrImageIdentity
	}
	if len(p.RepoDigests) > 128 {
		return ErrImageIdentity
	}
	for _, d := range p.RepoDigests {
		if len(d) > 1024 || strings.ContainsAny(d, "\x00\r\n") {
			return ErrImageIdentity
		}
	}
	return nil
}

func validSelector(s string) bool { return imageSelector.MatchString(s) && !strings.Contains(s, "://") }

const imageFormat = `{"id":{{json .Id}},"os":{{json .Os}},"architecture":{{json .Architecture}},"repo_digests":{{json .RepoDigests}}}`

func (c *Client) imageDaemon(ctx context.Context, endpoint string) (string, error) {
	raw, err := c.run(ctx, endpoint, "info", "--format", "{{json .ID}}")
	if err != nil {
		return "", ErrImage
	}
	v, err := contracts.Decode(raw, outputLimit)
	id, ok := v.(string)
	if err != nil || !ok || !daemonID.MatchString(id) {
		return "", ErrImageIdentity
	}
	return id, nil
}

// ResolveImage performs read-only Docker inspection. Explicit --platform selects
// a native variant from multi-platform local storage; no registry is contacted.
func (c *Client) ResolveImage(ctx context.Context, endpoint, selector, host string) (ImagePin, error) {
	var pin ImagePin
	platform, err := ImagePlatform(host)
	if err != nil {
		return pin, err
	}
	if c == nil || c.run == nil || campaign.ValidateDockerEndpoint(endpoint) != nil || !validSelector(selector) {
		return pin, ErrImageSelector
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	id, err := c.imageDaemon(ctx, endpoint)
	if err != nil {
		return pin, err
	}
	raw, err := c.run(ctx, endpoint, "image", "inspect", "--platform", platform, "--format", imageFormat, "--", selector)
	if err != nil {
		return pin, ErrImage
	}
	v, err := contracts.Decode(raw, outputLimit)
	if err != nil {
		return pin, ErrImageIdentity
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 4 {
		return pin, ErrImageIdentity
	}
	var item struct {
		ID           string   `json:"id"`
		OS           string   `json:"os"`
		Architecture string   `json:"architecture"`
		RepoDigests  []string `json:"repo_digests"`
	}
	if json.Unmarshal(raw, &item) != nil || !imageDigest.MatchString(item.ID) {
		return pin, ErrImageIdentity
	}
	if _, ok := m["repo_digests"]; !ok {
		return pin, ErrImageIdentity
	}
	if item.OS+"/"+item.Architecture != platform {
		return pin, ErrPlatform
	}
	// A request by immutable image ID must not be silently resolved to other bytes.
	if imageDigest.MatchString(selector) && selector != item.ID {
		return pin, ErrImageIdentity
	}
	again, err := c.imageDaemon(ctx, endpoint)
	if err != nil || again != id || ctx.Err() != nil {
		return pin, ErrImageIdentity
	}
	if item.RepoDigests == nil {
		item.RepoDigests = []string{}
	}
	pin = ImagePin{selector, endpoint, id, item.ID, host, platform, item.RepoDigests}
	if err := pin.Validate(); err != nil {
		return ImagePin{}, err
	}
	return pin, nil
}

// VerifyImage checks continued availability by the accepted immutable ID, not its
// original mutable tag. A changed daemon or missing pin requires failure, not pull.
func (c *Client) VerifyImage(ctx context.Context, pin ImagePin) error {
	if err := pin.Validate(); err != nil {
		return err
	}
	actual, err := c.ResolveImage(ctx, pin.Endpoint, pin.ImageID, pin.HostPlatform)
	if err != nil {
		return err
	}
	if actual.DaemonID != pin.DaemonID || actual.ImageID != pin.ImageID {
		return ErrImageIdentity
	}
	return nil
}

// VerifySelection is the pre-acceptance check for prepared inputs. A changed
// selector requires rebuilding image-dependent inputs and revalidating approval.
// Accepted retries use VerifyImage instead, preserving their original image pin.
func (c *Client) VerifySelection(ctx context.Context, pin ImagePin) error {
	if err := pin.Validate(); err != nil {
		return err
	}
	actual, err := c.ResolveImage(ctx, pin.Endpoint, pin.Selector, pin.HostPlatform)
	if err != nil {
		return err
	}
	if actual.DaemonID != pin.DaemonID || actual.ImageID != pin.ImageID {
		return ErrImageIdentity
	}
	return nil
}
