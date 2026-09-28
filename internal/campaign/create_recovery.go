//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"os"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// CreateIntent supplies discovery identity only. It never permits create/start.
type CreateIntent struct {
	Manifest       RunManifest
	ManifestDigest string
	Endpoint       string
	DaemonID       string
}

func (i CreateIntent) Validate() error {
	raw, err := i.Manifest.Bytes()
	if err != nil || contracts.RawDigest(raw) != i.ManifestDigest || !validID(i.DaemonID) {
		return ErrInvalid
	}
	return ValidateDockerEndpoint(i.Endpoint)
}

func (i CreateIntent) Binding(id string) (DockerBinding, error) {
	if err := i.Validate(); err != nil {
		return DockerBinding{}, err
	}
	m := i.Manifest
	b := DockerBinding{BindingVersion, m.CampaignID, m.LaunchID, m.ContainerID, i.ManifestDigest,
		i.Endpoint, i.DaemonID, id, m.ImageDigest, m.DockerLabels()}
	return b, b.validate(m, i.ManifestDigest)
}

// ReadCreateIntent requires a complete journal and exactly one valid start intent.
// A verified prefix of a damaged journal cannot authorize identity adoption.
func ReadCreateIntent(ctx context.Context, stateRoot, campaignID string) (CreateIntent, error) {
	r, err := openCampaign(stateRoot, campaignID)
	if err != nil {
		return CreateIntent{}, err
	}
	defer r.Close()
	locked, err := lock(r, false)
	if err != nil {
		return CreateIntent{}, err
	}
	defer locked.Close()
	return readCreateIntent(ctx, r, campaignID)
}

func readCreateIntent(ctx context.Context, r *os.Root, campaignID string) (CreateIntent, error) {
	var intent CreateIntent
	var image string
	var revision int64
	count := 0
	report, err := inspectRoot(ctx, r, campaignID, func(e Event) error {
		if e.Kind != "launch.start-intent" {
			return nil
		}
		count++
		v, err := contracts.Decode(e.Metadata, MaxMetadataBytes)
		if err != nil {
			return ErrCorrupt
		}
		fields, ok := v.(map[string]any)
		if !ok {
			return ErrCorrupt
		}
		intent.Endpoint, _ = fields["endpoint"].(string)
		intent.DaemonID, _ = fields["daemon_id"].(string)
		image, _ = fields["image_id"].(string)
		revision = e.RunRevision
		return nil
	}, false)
	if err != nil {
		return CreateIntent{}, err
	}
	intent.Manifest, intent.ManifestDigest = report.Manifest, report.ManifestDigest
	if !report.JournalIntact || count != 1 || image != report.Manifest.ImageDigest || revision != report.Manifest.InitialRevision || intent.Validate() != nil {
		return CreateIntent{}, ErrCorrupt
	}
	return intent, ctx.Err()
}

// SaveRecoveredDockerBinding publishes only an absent binding, under the campaign
// writer lock, after rechecking all original evidence. The caller MUST hold the
// installation lease and independently verify this exact Docker identity first.
// Existing, partial or conflicting files are never repaired or overwritten.
func SaveRecoveredDockerBinding(ctx context.Context, stateRoot string, b DockerBinding) error {
	if err := b.Validate(); err != nil {
		return err
	}
	r, err := openCampaign(stateRoot, b.CampaignID)
	if err != nil {
		return err
	}
	defer r.Close()
	locked, err := lock(r, false)
	if err != nil {
		return err
	}
	defer locked.Close()
	i, err := readCreateIntent(ctx, r, b.CampaignID)
	if err != nil {
		return err
	}
	if b.validate(i.Manifest, i.ManifestDigest) != nil || b.Endpoint != i.Endpoint || b.DaemonID != i.DaemonID {
		return ErrInvalid
	}
	if err := privateDir(r, "launch"); err != nil {
		return err
	}
	raw, err := encode(b, ManifestLimit)
	if err != nil {
		return err
	}
	existing, err := readFile(r, "launch/docker-binding.json", ManifestLimit)
	if err == nil {
		if contracts.RawDigest(existing) == contracts.RawDigest(raw) {
			return ctx.Err()
		}
		return ErrInvalid
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	hooks := diskHooks()
	return publish(r, "launch/docker-binding.json", raw, false, &hooks)
}
