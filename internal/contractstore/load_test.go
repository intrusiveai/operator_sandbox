//go:build linux || darwin

package contractstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func fixture(t *testing.T, change func(map[string][]byte)) (string, []byte, contracts.PackageIdentity) {
	t.Helper()
	p, err := bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	names, err := fs.ReadDir(schemas.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := schemas.Files.ReadFile(name.Name())
		if err != nil {
			t.Fatal(err)
		}
		files[name.Name()] = raw
	}
	profiles := map[string]string{"jcs-v1": "semantics/digests.md", "manifest-paths-v1": "semantics/paths.md", "harness-loop-v1": "semantics/loop.md"}
	for _, name := range profiles {
		files[name] = []byte("Test fixture; not a published release.\n")
	}
	// Payload source is inert even when installed executable bits are present.
	files["python/probe.py"] = []byte("raise RuntimeError('MUST NOT EXECUTE')\n")
	if change != nil {
		change(files)
	}
	manifest, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	files["package.json"] = manifest
	for name, raw := range files {
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, manifest, pin
}

func TestLoadExactInstalledPackageAndFreeze(t *testing.T) {
	dir, manifest, pin := fixture(t, nil)
	loaded, err := Load(context.Background(), dir, pin)
	if err != nil {
		t.Fatal(err)
	}
	report := loaded.Report()
	p, err := bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if report.Package != pin || report.ManifestDigest != contracts.RawDigest(manifest) || report.CatalogDigest != p.RegistryDigests()["catalog_digest"] || report.OperationsDigest != p.RegistryDigests()["operations_digest"] || report.FileCount < 56 || report.ContentBytes <= 0 || report.Status != "verified" {
		t.Fatal(report)
	}
	if actual, ok := loaded.Protocol().PackageIdentity(); !ok || actual != pin {
		t.Fatal(actual, ok)
	}
	if !bytes.Equal(loaded.Manifest(), manifest) {
		t.Fatal("manifest differs")
	}
	loaded.Manifest()[0] = 'x'
	report.Package.Version = "changed"
	if loaded.Report().Package != pin || !bytes.Equal(loaded.Manifest(), manifest) {
		t.Fatal("caller mutated frozen metadata")
	}
	// Later file modifications must not replace the loaded protocol's resources.
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/fixtures/startup-example.json")
	if err != nil {
		t.Fatal(err)
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.Protocol().ValidateControl("host", messages[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), dir, pin); err == nil {
		t.Fatal("modified installation accepted")
	}
}

func TestRejectFilesystemAndContentChanges(t *testing.T) {
	cases := map[string]func(string) error{
		"missing":         func(d string) error { return os.Remove(filepath.Join(d, "catalog.json")) },
		"extra file":      func(d string) error { return os.WriteFile(filepath.Join(d, "extra"), nil, 0644) },
		"extra empty dir": func(d string) error { return os.Mkdir(filepath.Join(d, "extra"), 0755) },
		"extra nested":    func(d string) error { return os.WriteFile(filepath.Join(d, "semantics", "extra"), nil, 0644) },
		"payload link": func(d string) error {
			p := filepath.Join(d, "catalog.json")
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.Symlink("operations.json", p)
		},
		"parent link": func(d string) error {
			p := filepath.Join(d, "semantics")
			if err := os.Rename(p, p+"-old"); err != nil {
				return err
			}
			return os.Symlink("semantics-old", p)
		},
		"hardlink": func(d string) error {
			return os.Link(filepath.Join(d, "catalog.json"), filepath.Join(t.TempDir(), "copy"))
		},
		"FIFO": func(d string) error {
			p := filepath.Join(d, "catalog.json")
			if err := os.Remove(p); err != nil {
				return err
			}
			return syscall.Mkfifo(p, 0600)
		},
		"directory for file": func(d string) error {
			p := filepath.Join(d, "catalog.json")
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.Mkdir(p, 0755)
		},
		"writable file":   func(d string) error { return os.Chmod(filepath.Join(d, "catalog.json"), 0664) },
		"writable parent": func(d string) error { return os.Chmod(filepath.Join(d, "semantics"), 0775) },
		"writable root":   func(d string) error { return os.Chmod(d, 0777) },
		"wrong size": func(d string) error {
			f, err := os.OpenFile(filepath.Join(d, "catalog.json"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString(" ")
			return err
		},
		"same size tamper": func(d string) error {
			p := filepath.Join(d, "semantics", "paths.md")
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			b[0] = '!'
			return os.WriteFile(p, b, 0644)
		},
		"manifest link": func(d string) error {
			p := filepath.Join(d, "package.json")
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.Symlink("catalog.json", p)
		},
		"manifest too large": func(d string) error {
			return os.Truncate(filepath.Join(d, "package.json"), contracts.PackageManifestLimit+1)
		},
		"payload too large": func(d string) error {
			return os.Truncate(filepath.Join(d, "catalog.json"), contracts.PackageFileLimit+1)
		},
		"reserved directory": func(d string) error { return os.Mkdir(filepath.Join(d, "signatures"), 0755) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _, pin := fixture(t, nil)
			if err := mutate(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(context.Background(), dir, pin); err == nil {
				t.Fatal("invalid installation accepted")
			}
		})
	}
}

func TestTrustedPinPrecedesPayloadReads(t *testing.T) {
	dir, _, pin := fixture(t, nil)
	if err := os.Remove(filepath.Join(dir, "catalog.json")); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []contracts.PackageIdentity{{}, {Version: "0.1.0", Digest: pin.Digest}, {Version: pin.Version, Digest: "sha256:" + strings.Repeat("0", 64)}} {
		if _, err := Load(context.Background(), dir, expected); !errors.Is(err, ErrPin) {
			t.Fatal(err)
		}
	}
}

func TestMalformedManifestAndOfflineSchemaRejection(t *testing.T) {
	dir, _, pin := fixture(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"password-secret": true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), dir, pin); !errors.Is(err, ErrManifest) || strings.Contains(err.Error(), "password-secret") {
		t.Fatal(err)
	}
	dir, _, pin = fixture(t, func(files map[string][]byte) {
		files["wire-common.schema.json"] = []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:operator:schema:wire-common:v1alpha1","$ref":"https://invalid.example/schema"}`)
	})
	if _, err := Load(context.Background(), dir, pin); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
}

func TestPathsCancellationAndManifestByteIdentity(t *testing.T) {
	dir, manifest, pin := fixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, dir, pin); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, p := range []string{"relative", "/", "/a/../b"} {
		if _, err := Load(context.Background(), p, pin); !errors.Is(err, ErrPath) {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "package")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), link, pin); !errors.Is(err, ErrInstallation) {
		t.Fatal(err)
	}
	// Canonical identity remains unchanged by manifest formatting; provenance does not.
	manifest = append(manifest, '\n')
	if err := os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(context.Background(), dir, pin)
	if err != nil || loaded.Report().ManifestDigest != contracts.RawDigest(manifest) {
		t.Fatal(loaded, err)
	}
}

func TestConcurrentLoads(t *testing.T) {
	dir, _, pin := fixture(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loaded, err := Load(context.Background(), dir, pin)
			if err != nil {
				t.Error(err)
				return
			}
			if loaded.Report().Package != pin {
				t.Error("wrong loaded identity")
			}
		}()
	}
	wg.Wait()
}

func TestEquivalentIntegerSpellings(t *testing.T) {
	dir, manifest, pin := fixture(t, nil)
	numbers := regexp.MustCompile(`("size_bytes":)([0-9]+)`)
	for _, suffix := range []string{".0", "e0"} {
		raw := numbers.ReplaceAll(manifest, []byte("${1}${2}"+suffix))
		if bytes.Equal(raw, manifest) {
			t.Fatal("fixture has no numeric fields")
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), raw, 0644); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(context.Background(), dir, pin)
		if err != nil || loaded.Report().Package != pin {
			t.Fatal(suffix, err)
		}
	}
}
