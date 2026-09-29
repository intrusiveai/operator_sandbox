//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostrelease"
)

// releaseCommand operates on explicit administrator-selected distribution files.
// It never fetches releases or treats a signature as native runtime qualification.
func releaseCommand(ctx context.Context, action string, args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("release "+action, flag.ContinueOnError)
	f.SetOutput(stderr)
	directory := f.String("directory", "", "release directory")
	output := f.String("output", "", "new output archive")
	keyring := f.String("keyring", "", "independently installed OpenPGP public keyring")
	verifier := f.String("gpgv", "", "absolute gpgv path; default administrator PATH")
	version := f.String("version", "", "host release version")
	platform := f.String("platform", "", "host OS/architecture")
	commit := f.String("source-commit", "", "source Git commit")
	contractVersion := f.String("contract-version", "", "approved contract version")
	contractDigest := f.String("contract-digest", "", "approved contract digest")
	if f.Parse(args) != nil || f.NArg() != 0 || *directory == "" {
		return 2
	}
	dir, err := filepath.Abs(*directory)
	if err != nil {
		return 2
	}
	var result any
	switch action {
	case "manifest":
		files, e := hostrelease.Inventory(ctx, dir)
		err = e
		m := hostrelease.Manifest{APIVersion: hostrelease.ManifestVersion, Version: *version, Platform: *platform, SourceCommit: *commit, GoToolchain: "go1.26.5", Contract: contracts.PackageIdentity{Version: *contractVersion, Digest: *contractDigest}, Files: files}
		if err == nil {
			err = hostrelease.CheckContents(ctx, dir, m)
		}
		if err == nil {
			var raw []byte
			raw, err = json.MarshalIndent(m, "", "  ")
			if err == nil {
				var file *os.File
				file, err = os.OpenFile(filepath.Join(dir, "release.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err == nil {
					_, err = file.Write(append(raw, '\n'))
					if e = file.Sync(); err == nil {
						err = e
					}
					if e = file.Close(); err == nil {
						err = e
					}
				}
			}
		}
		result = m
	case "check":
		if *keyring == "" {
			return 2
		}
		if *verifier == "" {
			*verifier, err = exec.LookPath("gpgv")
		}
		if err == nil {
			result, err = hostrelease.VerifyDirectory(ctx, dir, *verifier, *keyring)
		}
	case "archive":
		if *output == "" {
			return 2
		}
		err = hostrelease.Archive(ctx, dir, *output)
		result = map[string]string{"status": "archived", "output": *output}
	default:
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "release operation failed:", err)
		return 1
	}
	if json.NewEncoder(stdout).Encode(result) != nil {
		return 1
	}
	return 0
}
