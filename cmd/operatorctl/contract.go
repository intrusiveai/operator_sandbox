//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
)

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
