//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func embeddedProtocol(t *testing.T) *contracts.Protocol {
	t.Helper()
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	entries, _ := fs.ReadDir(schemas.Files, ".")
	for _, e := range entries {
		if !e.IsDir() {
			raw, err := fs.ReadFile(schemas.Files, e.Name())
			if err != nil {
				t.Fatal(err)
			}
			files[e.Name()] = raw
		}
	}
	profiles := map[string]string{"jcs-v1": "semantics/digests.md", "manifest-paths-v1": "semantics/paths.md", "harness-loop-v1": "semantics/limits.md"}
	for _, name := range profiles {
		files[name] = []byte("test semantic profile")
	}
	manifest, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.LoadVerifiedProtocol(manifest, files, pin)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func embeddedFiles(t *testing.T, p *contracts.Protocol, h Requirements) (map[string][]byte, embeddedManifest) {
	t.Helper()
	operations := []string{}
	for _, o := range p.Operations() {
		operations = append(operations, o.Name)
	}
	tools := map[string]json.RawMessage{}
	for _, codec := range embeddedCodecs {
		raw, err := p.ModelTools(codec, operations)
		if err != nil {
			t.Fatal(err)
		}
		tools[codec] = raw
	}
	raw, err := json.Marshal(map[string]any{"api_version": "operator.dev/model-tool-catalog/v1alpha1", "codecs": tools})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{PromptPath: []byte("Conduct an authorized campaign.\n"), LoaderPath: []byte("# Static loader fixture; never executed.\n"), ToolCatalogPath: raw}
	identity := func(name string) fileIdentity {
		return fileIdentity{len(files[name]), contracts.RawDigest(files[name])}
	}
	m := embeddedManifest{APIVersion: "operator.dev/engine-manifest/v1alpha1", Contract: h.Contract, RuntimeProfile: h.RuntimeProfile, Platform: "linux/arm64", Entrypoint: []string{"/usr/bin/python3", "-I", "-S", "-B", "/opt/operator/engine/bootstrap.py"}, Transports: []string{"fifo", "spool"}, Prompt: identity(PromptPath), SkillLoader: identity(LoaderPath), ToolCatalog: identity(ToolCatalogPath)}
	files[ManifestPath], err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return files, m
}

type fileInspector func(context.Context, dockercontrol.ImagePin) (dockercontrol.ReleaseFiles, error)

func (f fileInspector) ReadReleaseFiles(c context.Context, p dockercontrol.ImagePin) (dockercontrol.ReleaseFiles, error) {
	return f(c, p)
}

func TestEmbeddedReleaseAdmission(t *testing.T) {
	p := embeddedProtocol(t)
	h := requirements()
	h.Contract, _ = p.PackageIdentity()
	record := release()
	record.ContractPackageVersion = h.Contract.Version
	record.ContractPackageDigest = h.Contract.Digest
	prepared := Prepared{Image: dockercontrol.ImagePin{Selector: "harness:latest", Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", ImageID: record.ImageDigest, HostPlatform: h.HostPlatform, ImagePlatform: record.Platform}, Release: Approval{record: record, raw: wire(record), digest: contracts.RawDigest(wire(record))}}
	base, manifest := embeddedFiles(t, p, h)
	for _, mode := range []string{"success", "no approval", "unverified package", "cleanup failed", "wrong daemon", "wrong endpoint", "read failure", "missing file", "extra file", "prompt changed", "loader changed", "catalog changed", "different contract", "different platform", "different runtime", "entrypoint", "transports", "blank prompt", "loader NUL", "catalog tool changed", "missing codec", "manifest extra", "manifest duplicate"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string][]byte{}
			for k, v := range base {
				files[k] = bytes.Clone(v)
			}
			current := prepared
			protocol := p
			called := false
			m := manifest
			switch mode {
			case "no approval":
				current.Release = Approval{}
			case "unverified package":
				protocol, _ = contracts.LoadProtocol(schemas.Files)
			case "missing file":
				delete(files, LoaderPath)
			case "extra file":
				files["unexpected"] = []byte("x")
			case "prompt changed":
				files[PromptPath] = []byte("changed")
			case "loader changed":
				files[LoaderPath] = []byte("changed")
			case "catalog changed":
				files[ToolCatalogPath] = []byte("changed")
			case "different contract":
				m.Contract.Digest = contracts.RawDigest([]byte("other"))
			case "different platform":
				m.Platform = "linux/amd64"
			case "different runtime":
				m.RuntimeProfile = "other/v1"
			case "entrypoint":
				m.Entrypoint = []string{"/bin/sh"}
			case "transports":
				m.Transports = []string{"spool"}
			case "blank prompt":
				files[PromptPath] = []byte(" \n")
				m.Prompt = fileIdentity{len(files[PromptPath]), contracts.RawDigest(files[PromptPath])}
			case "loader NUL":
				files[LoaderPath] = []byte{0}
				m.SkillLoader = fileIdentity{1, contracts.RawDigest(files[LoaderPath])}
			case "catalog tool changed", "missing codec":
				var catalog map[string]any
				json.Unmarshal(files[ToolCatalogPath], &catalog)
				codecs := catalog["codecs"].(map[string]any)
				if mode == "missing codec" {
					delete(codecs, embeddedCodecs[0])
				} else {
					codecs[embeddedCodecs[0]] = []any{}
				}
				files[ToolCatalogPath], _ = json.Marshal(catalog)
				m.ToolCatalog = fileIdentity{len(files[ToolCatalogPath]), contracts.RawDigest(files[ToolCatalogPath])}
			}
			files[ManifestPath], _ = json.Marshal(m)
			if mode == "manifest extra" {
				files[ManifestPath] = append([]byte(`{"extra":true,`), files[ManifestPath][1:]...)
			}
			if mode == "manifest duplicate" {
				files[ManifestPath] = append([]byte(`{"platform":"linux/arm64",`), files[ManifestPath][1:]...)
			}
			inspector := fileInspector(func(_ context.Context, pin dockercontrol.ImagePin) (dockercontrol.ReleaseFiles, error) {
				called = true
				if pin.ImageID != prepared.Image.ImageID {
					t.Fatal("different image")
				}
				result := dockercontrol.ReleaseFiles{Endpoint: pin.Endpoint, DaemonID: pin.DaemonID, ContainerID: strings.Repeat("a", 64), Removed: true, Files: files}
				switch mode {
				case "cleanup failed":
					result.Removed = false
				case "wrong daemon":
					result.DaemonID = "other"
				case "wrong endpoint":
					result.Endpoint = "unix:///other.sock"
				case "read failure":
					return result, errors.New("unavailable")
				}
				return result, nil
			})
			got, _, err := InspectEmbedded(context.Background(), inspector, current, h, protocol)
			if (err == nil) != (mode == "success") {
				t.Fatalf("err=%v", err)
			}
			if (mode == "no approval" || mode == "unverified package") && called {
				t.Fatal("unapproved image inspected")
			}
			if err == nil {
				if !bytes.Equal(got.DefaultPrompt(), files[PromptPath]) || got.LoaderDigest() != m.SkillLoader.Digest {
					t.Fatal("wrong embedded bytes")
				}
				prompt := got.DefaultPrompt()
				prompt[0] = 'X'
				files[PromptPath][0] = 'Y'
				if bytes.Equal(got.DefaultPrompt(), prompt) || bytes.Equal(got.DefaultPrompt(), files[PromptPath]) {
					t.Fatal("mutable embedded bytes")
				}
				copy := got.Files()
				copy[ManifestPath][0] = 'X'
				if bytes.Equal(copy[ManifestPath], got.ManifestBytes()) {
					t.Fatal("mutable inventory")
				}
			}
		})
	}
}
