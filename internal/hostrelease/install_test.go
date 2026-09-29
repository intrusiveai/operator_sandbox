//go:build linux || darwin

package hostrelease

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func installFixture(t *testing.T) (InstallOptions, string, Manifest) {
	t.Helper()
	source, m := candidate(t)
	base := t.TempDir()
	verifier := filepath.Join(base, "gpgv-fixture")
	os.WriteFile(verifier, []byte("#!/bin/sh\nexit 0\n"), 0700)
	keyring := filepath.Join(base, "trusted.gpg")
	os.WriteFile(keyring, []byte("test trust boundary"), 0600)
	opts := InstallOptions{Root: filepath.Join(base, "installed"), Archive: filepath.Join(base, "one.tgz"), Verifier: verifier, Keyring: keyring, ConfigFile: filepath.Join(base, "config", "config.yaml"), StateRoot: filepath.Join(base, "state"), Image: "local/harness:fixture"}
	if err := Archive(context.Background(), source, opts.Archive); err != nil {
		t.Fatal(err)
	}
	return opts, source, m
}
func repack(t *testing.T, o InstallOptions, source string, m Manifest, version string) InstallOptions {
	t.Helper()
	m.Version = version
	raw, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(source, "release.json"), raw, 0600)
	o.Archive = filepath.Join(t.TempDir(), "next.tgz")
	if err := Archive(context.Background(), source, o.Archive); err != nil {
		t.Fatal(err)
	}
	return o
}
func TestInstallUpdateRetryAndExplicitDowngrade(t *testing.T) {
	o, source, m := installFixture(t)
	ctx := context.Background()
	first, err := Install(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(o.ConfigFile)
	evidence := filepath.Join(o.StateRoot, "retained-evidence")
	os.WriteFile(evidence, []byte("keep"), 0600)
	next := repack(t, o, source, m, "0.2.0")
	second, err := Install(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if second.Configuration != "preserved" {
		t.Fatal(second)
	}
	after, _ := os.ReadFile(o.ConfigFile)
	if string(after) != string(config) {
		t.Fatal("configuration overwritten")
	}
	if raw, _ := os.ReadFile(evidence); string(raw) != "keep" {
		t.Fatal("evidence removed")
	}
	if _, err := os.Stat(filepath.Join(first.ReleaseDirectory, "bin/operatorctl")); err != nil {
		t.Fatal("old executable removed", err)
	}
	if _, err := Install(ctx, next); err != nil {
		t.Fatal("idempotent install failed", err)
	}
	if _, err := Install(ctx, o); !errors.Is(err, ErrDowngrade) {
		t.Fatal("silent downgrade", err)
	}
	o.AllowDowngrade = true
	if _, err := Install(ctx, o); err != nil {
		t.Fatal(err)
	}
}
func TestInterruptedActivationPreservesPriorSelection(t *testing.T) {
	o, source, m := installFixture(t)
	ctx := context.Background()
	if _, err := Install(ctx, o); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Readlink(filepath.Join(o.Root, "current"))
	next := repack(t, o, source, m, "0.2.0")
	for _, stop := range []string{"release-published", "before-activation"} {
		_, err := install(ctx, next, func(point string) error {
			if point == stop {
				return errors.New("interrupted")
			}
			return nil
		})
		if err == nil {
			t.Fatal("missing interruption")
		}
		after, _ := os.Readlink(filepath.Join(o.Root, "current"))
		if after != before {
			t.Fatal("premature activation")
		}
	}
	if _, err := install(ctx, next, func(point string) error {
		if point == "activated" {
			return errors.New("lost acknowledgement")
		}
		return nil
	}); err == nil {
		t.Fatal("missing acknowledgement failure")
	}
	if _, err := Install(ctx, next); err != nil {
		t.Fatal("activation retry", err)
	}
}
func TestInstallRejectsUntrustedAndIncompatibleRelease(t *testing.T) {
	o, source, m := installFixture(t)
	ctx := context.Background()
	// Signature boundary is separately exercised with real GPG in signature tests.
	os.WriteFile(o.Verifier, []byte("#!/bin/sh\nexit 1\n"), 0700)
	if _, err := Install(ctx, o); !errors.Is(err, ErrSignature) {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(o.Root, "current")); !os.IsNotExist(err) {
		t.Fatal("untrusted activation")
	}
	os.WriteFile(o.Verifier, []byte("#!/bin/sh\nexit 0\n"), 0700)
	if m.Platform == "linux/amd64" {
		m.Platform = "darwin/arm64"
	} else {
		m.Platform = "linux/amd64"
	}
	o = repack(t, o, source, m, "0.1.0")
	if _, err := Install(ctx, o); !errors.Is(err, ErrRelease) {
		t.Fatal("wrong platform accepted", err)
	}
}

func TestNewFilePublicationDoesNotReplaceAndReclaimsInterruptedLinks(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "config.yaml")
	if err := writeNew(name, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(name, []byte("replacement"), 0600); !os.IsExist(err) {
		t.Fatal("overwrote existing configuration", err)
	}
	temp := filepath.Join(root, ".operator-new-interrupted")
	if err := os.Link(name, temp); err != nil {
		t.Fatal(err)
	}
	if err := reclaimNewFiles(root); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(name)
	if err != nil || string(raw) != "original" {
		t.Fatal("lost published configuration", err)
	}
}
