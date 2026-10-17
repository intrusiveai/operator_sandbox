//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
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

// ImagePin separates the selected platform identity from a locally usable immutable
// reference. An absent Reference is a legacy pin: only its original bare ID is used.
type ImagePin struct {
	Reference     string   `json:"reference,omitempty"`
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
	if campaign.ValidateDockerEndpoint(p.Endpoint) != nil || ValidateImageSelector(p.Selector) != nil || !daemonID.MatchString(p.DaemonID) || !imageDigest.MatchString(p.ImageID) {
		return ErrImageIdentity
	}
	if p.Reference != "" && (!immutableReference(p.Reference) || (imageDigest.MatchString(p.Reference) && p.Reference != p.ImageID)) {
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

// ValidateImageSelector checks administrator input without Docker I/O.
func ValidateImageSelector(s string) error {
	if !validSelector(s) {
		return ErrImageSelector
	}
	if _, digest, ok := strings.Cut(s, "@"); ok && !imageDigest.MatchString(digest) {
		return ErrImageSelector
	}
	return nil
}

const imageFormat = `{"id":{{json .Id}},"os":{{json .Os}},"architecture":{{json .Architecture}},"repo_digests":{{json .RepoDigests}}}`

func (c *Client) imageDaemon(ctx context.Context, endpoint string) (string, error) {
	raw, err := c.run(ctx, endpoint, "info", "--format", "{{json .ID}}")
	if err != nil {
		return "", dockerFailure(ctx, "Docker daemon inspection", ErrImage, err)
	}
	v, err := contracts.Decode(raw, outputLimit)
	id, ok := v.(string)
	if err != nil || !ok || !daemonID.MatchString(id) {
		return "", ErrImageIdentity
	}
	return id, nil
}

// ReferenceForDocker preserves legacy bare-ID semantics; it never selects a tag.
func (p ImagePin) ReferenceForDocker() string {
	if p.Reference == "" {
		return p.ImageID
	}
	return p.Reference
}

func immutableReference(s string) bool {
	if imageDigest.MatchString(s) {
		return true
	}
	repo, digest, ok := strings.Cut(s, "@")
	return ok && validSelector(repo) && !strings.Contains(repo, "@") &&
		!strings.Contains(repo[strings.LastIndex(repo, "/")+1:], ":") && imageDigest.MatchString(digest)
}

// repository normalizes familiar Docker names without consulting a registry.
func repository(s string) string {
	s, _, _ = strings.Cut(s, "@")
	if i := strings.LastIndex(s, ":"); i > strings.LastIndex(s, "/") {
		s = s[:i]
	}
	first, _, slash := strings.Cut(s, "/")
	if !slash || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		s = "docker.io/" + s
	}
	if strings.HasPrefix(s, "index.docker.io/") {
		s = "docker.io/" + strings.TrimPrefix(s, "index.docker.io/")
	}
	if !strings.Contains(s, "/") {
		s = "docker.io/" + s
	}
	if strings.HasPrefix(s, "docker.io/") && strings.Count(s, "/") == 1 {
		s = "docker.io/library/" + strings.TrimPrefix(s, "docker.io/")
	}
	return s
}

// inspectImage inspects exactly one selector; it never changes selectors or pulls.
func (c *Client) inspectImage(ctx context.Context, endpoint, selector, platform, operation string) (ImagePin, error) {
	raw, err := c.run(ctx, endpoint, "image", "inspect", "--platform", platform, "--format", imageFormat, "--", selector)
	if err != nil {
		return ImagePin{}, dockerFailure(ctx, operation, ErrImage, err)
	}
	v, err := contracts.Decode(raw, outputLimit)
	m, ok := v.(map[string]any)
	if err != nil || !ok || len(m) != 4 {
		return ImagePin{}, ErrImageIdentity
	}
	var item struct {
		ID           string   `json:"id"`
		OS           string   `json:"os"`
		Architecture string   `json:"architecture"`
		RepoDigests  []string `json:"repo_digests"`
	}
	if json.Unmarshal(raw, &item) != nil || !imageDigest.MatchString(item.ID) {
		return ImagePin{}, ErrImageIdentity
	}
	if _, ok := m["repo_digests"]; !ok {
		return ImagePin{}, ErrImageIdentity
	}
	if item.OS+"/"+item.Architecture != platform {
		return ImagePin{}, ErrPlatform
	}
	if imageDigest.MatchString(selector) && selector != item.ID {
		return ImagePin{}, ErrImageIdentity
	}
	if item.RepoDigests == nil {
		item.RepoDigests = []string{}
	}
	return ImagePin{ImageID: item.ID, ImagePlatform: platform, RepoDigests: item.RepoDigests}, nil
}

// ResolveImage selects and verifies an immutable local reference before returning
// the pin. Classic stores use their bare config ID. Containerd stores may require
// a repository-qualified index digest plus the explicit platform.
func (c *Client) ResolveImage(ctx context.Context, endpoint, selector, host string) (ImagePin, error) {
	platform, err := ImagePlatform(host)
	if err != nil {
		return ImagePin{}, err
	}
	if c == nil || c.run == nil || campaign.ValidateDockerEndpoint(endpoint) != nil || ValidateImageSelector(selector) != nil {
		return ImagePin{}, ErrImageSelector
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	daemon, err := c.imageDaemon(ctx, endpoint)
	if err != nil {
		return ImagePin{}, err
	}
	pin, err := c.inspectImage(ctx, endpoint, selector, platform, "selected image inspection")
	if err != nil {
		return ImagePin{}, err
	}
	pin.Selector, pin.Endpoint, pin.DaemonID, pin.HostPlatform = selector, endpoint, daemon, host
	if err = pin.Validate(); err != nil {
		return ImagePin{}, err
	}
	candidates := []string{pin.ImageID}
	// Prefer an explicitly selected immutable reference. Never choose another repo
	// just because it is first in Docker's inventory.
	if immutableReference(selector) && selector != pin.ImageID {
		candidates = []string{selector, pin.ImageID}
	}
	repos := []string{}
	if !imageDigest.MatchString(selector) {
		for _, ref := range pin.RepoDigests {
			if immutableReference(ref) && !imageDigest.MatchString(ref) && repository(ref) == repository(selector) {
				repos = append(repos, ref)
			}
		}
	}
	sort.Strings(repos)
	candidates = append(candidates, repos...)
	seen := map[string]bool{}
	var last error
	for _, ref := range candidates {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		actual, e := c.inspectImage(ctx, endpoint, ref, platform, "immutable image reference inspection")
		if e != nil {
			last = e
			// Only an absent local selector permits trying another immutable candidate.
			if !isMissingImage(e) {
				return ImagePin{}, e
			}
			continue
		}
		if actual.ImageID != pin.ImageID {
			return ImagePin{}, ErrImageIdentity
		}
		pin.Reference = ref
		break
	}
	if pin.Reference == "" {
		return ImagePin{}, last
	}
	again, err := c.imageDaemon(ctx, endpoint)
	if err != nil {
		return ImagePin{}, err
	}
	if again != daemon {
		return ImagePin{}, ErrImageIdentity
	}
	if ctx.Err() != nil {
		return ImagePin{}, dockerFailure(ctx, "image selection", ErrImage, ctx.Err())
	}
	return pin, nil
}

// VerifyImage never consults the original tag or switches a saved reference.
func (c *Client) VerifyImage(ctx context.Context, pin ImagePin) error {
	if c == nil || c.run == nil {
		return ErrImageSelector
	}
	if err := pin.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	daemon, err := c.imageDaemon(ctx, pin.Endpoint)
	if err != nil {
		return err
	}
	if daemon != pin.DaemonID {
		return ErrImageIdentity
	}
	actual, err := c.inspectImage(ctx, pin.Endpoint, pin.ReferenceForDocker(), pin.ImagePlatform, "pinned image reference inspection")
	if err != nil {
		return err
	}
	if actual.ImageID != pin.ImageID {
		return ErrImageIdentity
	}
	daemon, err = c.imageDaemon(ctx, pin.Endpoint)
	if err != nil {
		return err
	}
	if daemon != pin.DaemonID {
		return ErrImageIdentity
	}
	return ctx.Err()
}

// VerifySelection detects changes to the administrator's tag before admission.
func (c *Client) VerifySelection(ctx context.Context, pin ImagePin) error {
	if c == nil || c.run == nil {
		return ErrImageSelector
	}
	if err := pin.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, ConfirmationTimeout)
	defer cancel()
	daemon, err := c.imageDaemon(ctx, pin.Endpoint)
	if err != nil {
		return err
	}
	if daemon != pin.DaemonID {
		return ErrImageIdentity
	}
	actual, err := c.inspectImage(ctx, pin.Endpoint, pin.Selector, pin.ImagePlatform, "selected image reinspection")
	if err != nil {
		return err
	}
	if actual.ImageID != pin.ImageID {
		return ErrImageIdentity
	}
	return c.VerifyImage(ctx, pin)
}
