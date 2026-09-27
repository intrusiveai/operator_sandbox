//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/internal/workerjob"
)

// Set by the release build with -ldflags '-X main.operatorVersion=VERSION'.
// A version string is not evidence of platform/provider qualification.
var operatorVersion = "0.1.0"

// workerCommand is a fixed supervisor entrypoint. The only work selector is a
// private durable request; it accepts no command, environment or guest payload.
func workerCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("_worker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("state-root", "", "installed state root")
	id := flags.String("request-id", "", "durable start request ID")
	digest := flags.String("request-digest", "", "durable request digest")
	if flags.Parse(args) != nil {
		return 2
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*root) || filepath.Clean(*root) != *root || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(*id) || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(*digest) {
		fmt.Fprintln(stderr, "invalid worker request binding")
		return 2
	}
	result, err := workerjob.Run(ctx, *root, *id, *digest, operatorVersion)
	if json.NewEncoder(stdout).Encode(result) != nil {
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "campaign worker stopped:", result.Code)
		return 1
	}
	return 0
}
