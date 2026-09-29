//go:build linux || darwin

package hostrelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/contractpublish"
)

func candidate(t *testing.T) (string, Manifest) {
	t.Helper()
	root := t.TempDir()
	source, _ := filepath.Abs("../..")
	report, err := contractpublish.Build(context.Background(), source, filepath.Join(root, "contract"), "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"bin/operatorctl": "binary fixture", "templates/operator-config.yaml": "engine: {image: local}", "sbom.spdx.json": "{}", "provenance.json": "{}"} {
		file := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if name == "bin/operatorctl" {
			mode = 0700
		}
		if err = os.WriteFile(file, []byte(raw), mode); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Inventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{ManifestVersion, "0.1.0", runtime.GOOS + "/" + runtime.GOARCH, strings.Repeat("a", 40), "go1.26.5", report.Package, files}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "release.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "release.sig"), []byte("signature fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, m
}
func TestArchiveRoundTripAndInventory(t *testing.T) {
	root, m := candidate(t)
	ctx := context.Background()
	temp := t.TempDir()
	one, two := filepath.Join(temp, "one.tgz"), filepath.Join(temp, "two.tgz")
	for _, out := range []string{one, two} {
		if err := Archive(ctx, root, out); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(one)
	b, _ := os.ReadFile(two)
	if !bytes.Equal(a, b) {
		t.Fatal("non-reproducible archive")
	}
	dest := filepath.Join(temp, "unpacked")
	if err := Extract(ctx, one, dest); err != nil {
		t.Fatal(err)
	}
	if err := CheckContents(ctx, dest, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "bin/operatorctl"), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckContents(ctx, dest, m); err == nil {
		t.Fatal("accepted changed executable")
	}
	if err := Archive(ctx, root, one); err == nil {
		t.Fatal("overwrote archive")
	}
}
func TestManifestRejectsAmbiguousInventory(t *testing.T) {
	_, m := candidate(t)
	tests := []func(*Manifest){
		func(m *Manifest) { m.Platform = "windows/amd64" }, func(m *Manifest) { m.Version = "01.2.3" },
		func(m *Manifest) { m.Files[0].Path = "../escape" }, func(m *Manifest) { m.Files[0].Mode = 04755 },
		func(m *Manifest) { m.Files[0].Size = MaxFileBytes + 1 }, func(m *Manifest) { m.Files[0].Digest = "sha256:bad" },
		func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) }, func(m *Manifest) { m.Files = m.Files[1:] },
	}
	for _, change := range tests {
		copy := m
		copy.Files = append([]File{}, m.Files...)
		change(&copy)
		raw, _ := json.Marshal(copy)
		if _, err := DecodeManifest(raw); err == nil {
			t.Fatal("accepted invalid manifest")
		}
	}
	raw, _ := json.Marshal(m)
	raw = append([]byte(`{"version":"0.1.0",`), raw[1:]...)
	if _, err := DecodeManifest(raw); err == nil {
		t.Fatal("accepted duplicate key")
	}
}
func TestExtractRejectsUnsafeAndPartialArchives(t *testing.T) {
	for _, h := range []*tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp", Mode: 0600},
		{Name: "pipe", Typeflag: tar.TypeFifo, Mode: 0600},
		{Name: "file", Typeflag: tar.TypeReg, Mode: 04755},
		{Name: "release.json", Typeflag: tar.TypeReg, Mode: 0600},
	} {
		dir := t.TempDir()
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Close()
		gz.Close()
		archive := filepath.Join(dir, "bad.tgz")
		os.WriteFile(archive, buf.Bytes(), 0600)
		dest := filepath.Join(dir, "out")
		if err := Extract(context.Background(), archive, dest); err == nil {
			t.Fatal("accepted unsafe/incomplete archive")
		}
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Fatal("partial extraction retained")
		}
	}
}
func TestOpenPGPSignatureUsesOnlyInstalledKeyring(t *testing.T) {
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg unavailable")
	}
	verifier, err := exec.LookPath("gpgv")
	if err != nil {
		t.Skip("gpgv unavailable")
	}
	// Short paths avoid macOS agent Unix-socket path limits.
	home, err := os.MkdirTemp("/tmp", "operator-sign-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	defer exec.Command("gpgconf", "--homedir", home, "--kill", "gpg-agent").Run()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gpg, append([]string{"--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase", ""}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture GPG: %v: %s", err, out)
		}
	}
	run("--quick-generate-key", "Operator Release Test <fixture@example.invalid>", "ed25519", "sign", "0")
	keyring := filepath.Join(home, "trusted.gpg")
	run("--output", keyring, "--export")
	raw := []byte("release manifest fixture\n")
	manifest := filepath.Join(home, "manifest.json")
	os.WriteFile(manifest, raw, 0600)
	sigfile := filepath.Join(home, "manifest.sig")
	run("--output", sigfile, "--detach-sign", manifest)
	sig, _ := os.ReadFile(sigfile)
	if err := VerifySignature(context.Background(), verifier, keyring, raw, sig); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(context.Background(), verifier, keyring, append(raw, 'x'), sig); err == nil {
		t.Fatal("accepted changed manifest")
	}
	wrong := filepath.Join(home, "wrong.gpg")
	os.WriteFile(wrong, []byte("not a trusted key"), 0600)
	if err := VerifySignature(context.Background(), verifier, wrong, raw, sig); err == nil {
		t.Fatal("used ambient trusted key")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := VerifySignature(ctx, verifier, keyring, raw, sig); err == nil {
		t.Fatal("ignored cancellation")
	}
}
