//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractpublish"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
)

func buildContract(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("contract build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", "", "trusted source checkout")
	output := flags.String("output", "", "new content-only package directory")
	version := flags.String("package-version", "", "explicit contract version")
	if flags.Parse(args) != nil {
		return 2
	}
	if flags.NArg() != 0 || *source == "" || *output == "" || *version == "" {
		fmt.Fprintln(stderr, "contract build requires --source, --output and --package-version")
		return 2
	}
	src, err := filepath.Abs(*source)
	if err != nil {
		return 2
	}
	dest, err := filepath.Abs(*output)
	if err != nil {
		return 2
	}
	report, err := contractpublish.Build(ctx, src, dest, *version)
	if err != nil {
		fmt.Fprintln(stderr, "contract build failed:", err)
		return 1
	}
	if json.NewEncoder(stdout).Encode(report) != nil {
		return 1
	}
	return 0
}

func checkContract(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("contract check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("package-dir", "", "absolute installed content-only package directory")
	version := flags.String("package-version", "", "expected version from trusted installation metadata")
	digest := flags.String("package-digest", "", "expected SHA-256 package digest from trusted installation metadata")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *directory == "" || *version == "" || *digest == "" {
		fmt.Fprintln(stderr, "contract check requires --package-dir, --package-version and --package-digest")
		return 2
	}
	loaded, err := contractstore.Load(ctx, *directory, contracts.PackageIdentity{Version: *version, Digest: *digest})
	if err != nil {
		fmt.Fprintln(stderr, "cannot verify installed contract:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(loaded.Report()); err != nil {
		return 1
	}
	return 0
}
