//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/contractstore"
	"github.com/intrusive-ai/operator-sandbox/schemas"
)

func installedContract(t *testing.T) (string, contracts.PackageIdentity) {
	t.Helper()
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	entries, err := fs.ReadDir(schemas.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := schemas.Files.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = raw
	}
	profiles := map[string]string{"jcs-v1": "semantics/jcs", "manifest-paths-v1": "semantics/paths", "harness-loop-v1": "semantics/loop"}
	for _, name := range profiles {
		files[name] = []byte("Test-only semantic resource.")
	}
	manifest, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	files["package.json"] = manifest
	dir := t.TempDir()
	for name, raw := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, pin
}

func TestContractCheckCommand(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, "broken config")
	args := []string{"contract", "check", "--package-dir", dir, "--package-version", pin.Version, "--package-digest", pin.Digest}
	var out, stderr bytes.Buffer
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	code := runWithDefaults(context.Background(), args, &out, &stderr, paths)
	var report contractstore.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || code != 0 || report.Package != pin || report.Status != "verified" || report.APIVersion != contractstore.ReportVersion {
		t.Fatal(code, out.String(), stderr.String(), err)
	}
	after, err := os.Stat(dir)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("check modified package directory", err)
	}
	if _, err := os.Stat(paths.StateRoot); !os.IsNotExist(err) {
		t.Fatal("check created state directory", err)
	}
	// This command uses explicit installation metadata, never ambient host config.
	if stderr.Len() != 0 {
		t.Fatal(stderr.String())
	}
	args[len(args)-1] = "sha256:" + strings.Repeat("0", 64)
	out.Reset()
	stderr.Reset()
	if code := runWithDefaults(context.Background(), args, &out, &stderr, paths); code != 1 || out.Len() != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
}

func TestContractCheckRequiresIndependentExpectedPin(t *testing.T) {
	dir, pin := installedContract(t)
	for _, args := range [][]string{
		{"contract", "check"},
		{"contract", "check", "--package-dir", dir},
		{"contract", "check", "--package-dir", dir, "--package-version", pin.Version},
		{"contract", "check", "--package-dir", dir, "--package-version", pin.Version, "--package-digest", pin.Digest, "extra"},
	} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
}
