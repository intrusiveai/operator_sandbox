//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"unicode/utf8"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

const ManifestPath = "/opt/operator/engine/share/engine-manifest.json"
const PromptPath = "/opt/operator/engine/share/default-system-prompt.txt"
const LoaderPath = "/opt/operator/engine/lib/attack_harness/skill_loader.py"
const ToolCatalogPath = "/opt/operator/engine/share/tool-catalog.json"

var ErrEmbedded = errors.New("embedded engine metadata does not match approved image and installed contract")
var embeddedCodecs = []string{"anthropic-messages-text-tools-v1", "bedrock-converse-text-tools-v1", "gemini-text-tools-v1", "openai-chat-text-tools-v1", "openai-responses-text-tools-v1"}

type fileIdentity struct {
	Size   int    `json:"size_bytes"`
	Digest string `json:"digest"`
}
type embeddedManifest struct {
	APIVersion     string                    `json:"api_version"`
	Contract       contracts.PackageIdentity `json:"contract"`
	RuntimeProfile string                    `json:"runtime_profile"`
	Platform       string                    `json:"platform"`
	Entrypoint     []string                  `json:"entrypoint"`
	Transports     []string                  `json:"transports"`
	Prompt         fileIdentity              `json:"prompt"`
	SkillLoader    fileIdentity              `json:"skill_loader"`
	ToolCatalog    fileIdentity              `json:"tool_catalog"`
}

// Embedded contains checked immutable image data. Accessors return independent
// copies; the image cannot select host commands, paths or policy settings.
type Embedded struct {
	image         dockercontrol.ImagePin
	releaseDigest string
	manifest      embeddedManifest
	files         map[string][]byte
}

// Matches binds checked bytes to the same approved image and local daemon.
func (e *Embedded) Matches(p Prepared) bool {
	return e != nil && e.releaseDigest != "" && e.releaseDigest == p.Release.Digest() &&
		e.image.ImageID == p.Image.ImageID && e.image.Endpoint == p.Image.Endpoint &&
		e.image.DaemonID == p.Image.DaemonID && e.image.HostPlatform == p.Image.HostPlatform &&
		e.image.ImagePlatform == p.Image.ImagePlatform
}
func (e *Embedded) DefaultPrompt() []byte { return bytes.Clone(e.files[PromptPath]) }
func (e *Embedded) LoaderDigest() string  { return e.manifest.SkillLoader.Digest }
func (e *Embedded) ManifestBytes() []byte { return bytes.Clone(e.files[ManifestPath]) }
func (e *Embedded) Files() map[string][]byte {
	result := map[string][]byte{}
	for name, raw := range e.files {
		result[name] = bytes.Clone(raw)
	}
	return result
}

type FileInspector interface {
	ReadReleaseFiles(context.Context, dockercontrol.ImagePin) (dockercontrol.ReleaseFiles, error)
}

// InspectEmbedded only reads an already approved local image. The returned
// inspection identity remains available when Docker cleanup is unconfirmed.
func InspectEmbedded(ctx context.Context, reader FileInspector, prepared Prepared, h Requirements, protocol *contracts.Protocol) (*Embedded, dockercontrol.ReleaseFiles, error) {
	var inspection dockercontrol.ReleaseFiles
	if reader == nil || protocol == nil || prepared.Image.Validate() != nil || prepared.Release.Digest() == "" || prepared.Image.HostPlatform != h.HostPlatform || prepared.Release.Record().Compatible(prepared.Image.ImageID, h) != nil {
		return nil, inspection, ErrEmbedded
	}
	pin, ok := protocol.PackageIdentity()
	if !ok || pin != h.Contract {
		return nil, inspection, ErrEmbedded
	}
	inspection, err := reader.ReadReleaseFiles(ctx, prepared.Image)
	if err != nil {
		return nil, inspection, err
	}
	if !inspection.Removed || !digestPattern.MatchString("sha256:"+inspection.ContainerID) || inspection.Endpoint != prepared.Image.Endpoint || inspection.DaemonID != prepared.Image.DaemonID || len(inspection.Files) != 4 {
		return nil, inspection, ErrEmbedded
	}
	embedded, err := parseEmbedded(inspection.Files, prepared.Image.ImagePlatform, h, protocol)
	if err != nil {
		return nil, inspection, err
	}
	embedded.image = prepared.Image
	embedded.image.RepoDigests = nil
	embedded.releaseDigest = prepared.Release.Digest()
	return embedded, inspection, nil
}
func parseEmbedded(files map[string][]byte, platform string, h Requirements, p *contracts.Protocol) (*Embedded, error) {
	var manifest embeddedManifest
	if strict(files[ManifestPath], &manifest, ResponseLimit) != nil || manifest.APIVersion != "operator.dev/engine-manifest/v1alpha1" || manifest.Contract != h.Contract || manifest.Platform != platform || manifest.RuntimeProfile != h.RuntimeProfile || !reflect.DeepEqual(manifest.Entrypoint, []string{"/usr/bin/python3", "-I", "-S", "-B", "/opt/operator/engine/bootstrap.py"}) || !reflect.DeepEqual(manifest.Transports, []string{"fifo", "spool"}) {
		return nil, ErrEmbedded
	}
	for file, expected := range map[string]fileIdentity{PromptPath: manifest.Prompt, LoaderPath: manifest.SkillLoader, ToolCatalogPath: manifest.ToolCatalog} {
		raw := files[file]
		if expected.Size < 1 || expected.Size > 1<<20 || len(raw) != expected.Size || contracts.RawDigest(raw) != expected.Digest {
			return nil, ErrEmbedded
		}
	}
	if contracts.ValidatePrompt(files[PromptPath]) != nil || !utf8.Valid(files[LoaderPath]) || bytes.ContainsRune(files[LoaderPath], 0) {
		return nil, ErrEmbedded
	}
	var catalog struct {
		APIVersion string                     `json:"api_version"`
		Codecs     map[string]json.RawMessage `json:"codecs"`
	}
	if strict(files[ToolCatalogPath], &catalog, 1<<20) != nil || catalog.APIVersion != "operator.dev/model-tool-catalog/v1alpha1" || len(catalog.Codecs) != len(embeddedCodecs) {
		return nil, ErrEmbedded
	}
	operations := []string{}
	for _, op := range p.Operations() {
		operations = append(operations, op.Name)
	}
	for _, codec := range embeddedCodecs {
		expected, err := p.ModelTools(codec, operations)
		if err != nil {
			return nil, ErrEmbedded
		}
		actual, err := contracts.Canonicalize(catalog.Codecs[codec], contracts.OrdinaryLimit)
		if err != nil || !bytes.Equal(actual, expected) {
			return nil, ErrEmbedded
		}
	}
	result := &Embedded{manifest: manifest, files: map[string][]byte{}}
	for _, name := range []string{ManifestPath, PromptPath, LoaderPath, ToolCatalogPath} {
		result.files[name] = bytes.Clone(files[name])
	}
	return result, nil
}
