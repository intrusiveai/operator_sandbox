//go:build linux || darwin

// Package submission freezes reusable campaign inputs without contacting a target,
// model or Docker. Successful validation is not authorization to execute a campaign.
package submission

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

const Version = "operator.dev/submission/v1alpha1"

var ErrSubmission = errors.New("invalid, incomplete or changed submission")

type Receipt struct {
	APIVersion       string                    `json:"api_version"`
	Status           string                    `json:"status"`
	Contract         contracts.PackageIdentity `json:"contract"`
	BundleDigest     string                    `json:"bundle_digest"`
	CapabilityDigest string                    `json:"capability_digest"`
	NativeDigest     string                    `json:"native_digest"`
	ArtifactCount    int                       `json:"artifact_count"`
	ArtifactBytes    int64                     `json:"artifact_bytes"`
	OptionalGaps     []capabilities.Gap        `json:"optional_gaps"`
}

type Prepared struct {
	receipt   Receipt
	bundle    []byte
	authoring *capabilities.Export
	artifacts map[string][]byte
}

func (p *Prepared) Receipt() Receipt {
	r := p.receipt
	r.OptionalGaps = append([]capabilities.Gap{}, r.OptionalGaps...)
	return r
}
func ArtifactName(digest string) string { return "sha256-" + strings.TrimPrefix(digest, "sha256:") }

// Read captures exact bytes once. Artifact paths are derived exclusively from
// validated digests; omissions must be explicit in the submitted bundle.
func Read(ctx context.Context, protocol *contracts.Protocol, bundlePath, capabilityPath, artifactDirectory string) (*Prepared, error) {
	if protocol == nil {
		return nil, ErrSubmission
	}
	pin, ok := protocol.PackageIdentity()
	if !ok {
		return nil, ErrSubmission
	}
	raw, err := staging.Capture(ctx, bundlePath, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	bundle, err := capabilities.ParseBundle(protocol.Catalog(), raw)
	if err != nil {
		return nil, ErrSubmission
	}
	// Decode only after the schema and semantic validator have accepted the bytes.
	v, _ := contracts.Decode(raw, contracts.OrdinaryLimit)
	b := v.(map[string]any)
	targetID := b["target_requirements"].(map[string]any)["target_id"].(string)
	authoring, err := capabilities.LoadExport(ctx, protocol.Catalog(), capabilityPath, targetID)
	if err != nil {
		return nil, ErrSubmission
	}
	gaps, err := capabilities.CheckAuthoring(bundle, authoring)
	if err != nil {
		return nil, err
	}
	p := &Prepared{bundle: raw, authoring: authoring, artifacts: map[string][]byte{}, receipt: Receipt{APIVersion: Version, Status: "validated-offline", Contract: pin, BundleDigest: contracts.RawDigest(raw), CapabilityDigest: contracts.RawDigest(authoring.PublicJSON()), NativeDigest: contracts.RawDigest(authoring.NativeJSON()), OptionalGaps: gaps}}
	sizes := map[string]int64{}
	for _, value := range b["artifacts"].([]any) {
		a := value.(map[string]any)
		if a["omission_reason"] != nil {
			continue
		}
		size, _ := a["size_bytes"].(json.Number).Float64()
		sizes[ArtifactName(a["digest"].(string))] = int64(size)
	}
	contents := map[string][]byte{}
	if artifactDirectory != "" {
		contents, err = staging.CaptureInventory(ctx, artifactDirectory, sizes)
		if err != nil {
			return nil, ErrSubmission
		}
	} else if len(sizes) != 0 {
		return nil, ErrSubmission
	}
	for _, value := range b["artifacts"].([]any) {
		a := value.(map[string]any)
		if a["omission_reason"] != nil {
			continue
		}
		digest := a["digest"].(string)
		if _, exists := p.artifacts[digest]; exists {
			continue
		}
		content := contents[ArtifactName(digest)]
		if contracts.RawDigest(content) != digest {
			return nil, ErrSubmission
		}
		if schema, ok := a["schema_id"].(string); ok {
			if _, err := protocol.Catalog().Validate(schema, content, 1<<20); err != nil {
				return nil, ErrSubmission
			}
		}
		p.artifacts[digest] = content
		p.receipt.ArtifactBytes += int64(len(content))
	}
	p.receipt.ArtifactCount = len(p.artifacts)
	report, err := json.Marshal(p.receipt)
	if err != nil || len(report) > contracts.OrdinaryLimit {
		return nil, ErrSubmission
	}
	return p, nil
}

// Save claims a new output directory exclusively; run.json is the completion
// marker, written after every input is fsynced. Existing runs are never replaced.
// Failed output remains visibly incomplete for inspection; Load rejects it.
func (p *Prepared) Save(ctx context.Context, directory string) error {
	if p == nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return ErrSubmission
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{"input", "input/artifacts"} {
		if err := root.Mkdir(name, 0700); err != nil {
			return err
		}
	}
	files := map[string][]byte{"input/scenario-bundle.json": p.bundle, "input/capabilities.json": p.authoring.PublicJSON(), "input/" + ArtifactName(p.receipt.NativeDigest) + ".json": p.authoring.NativeJSON()}
	for digest, raw := range p.artifacts {
		files["input/artifacts/"+ArtifactName(digest)] = raw
	}
	receipt, err := json.Marshal(p.receipt)
	if err != nil {
		return err
	}
	files["input/validation.json"] = receipt
	provenance, err := json.Marshal(map[string]any{"contract": p.receipt.Contract, "bundle_digest": p.receipt.BundleDigest, "capability_digest": p.receipt.CapabilityDigest, "native_digest": p.receipt.NativeDigest})
	if err != nil {
		return err
	}
	files["input/provenance.json"] = provenance
	for name, raw := range files {
		if err := write(ctx, root, name, raw); err != nil {
			return err
		}
	}
	for _, name := range []string{"input/artifacts", "input", "."} {
		if err := syncDir(root, name); err != nil {
			return err
		}
	}
	if err := write(ctx, root, "run.json", receipt); err != nil {
		return err
	}
	if err := syncDir(root, "."); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(directory))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func write(ctx context.Context, root *os.Root, name string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func syncDir(root *os.Root, name string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// Load revalidates every input, the independent installed contract pin and all
// recorded digests. It does not trust a previous "valid" status or modify files.
func Load(ctx context.Context, protocol *contracts.Protocol, directory string) (*Prepared, error) {
	raw, err := staging.Capture(ctx, filepath.Join(directory, "run.json"), contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	var expected Receipt
	if interceptor.DecodeTypedBody(raw, &expected, contracts.OrdinaryLimit) != nil {
		return nil, ErrSubmission
	}
	p, err := Read(ctx, protocol, filepath.Join(directory, "input/scenario-bundle.json"), filepath.Join(directory, "input/capabilities.json"), filepath.Join(directory, "input/artifacts"))
	if err != nil {
		return nil, err
	}
	want, _ := json.Marshal(p.receipt)
	got, _ := json.Marshal(expected)
	if string(want) != string(got) {
		return nil, ErrSubmission
	}
	return p, nil
}
