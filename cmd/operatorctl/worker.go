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

	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/workerjob"
)

// Set by the release build with -ldflags '-X main.operatorVersion=VERSION'.
// A version string is not evidence of platform/provider qualification.
var operatorVersion = "0.1.0"

// Release builds set these independently of administrator configuration.
var releaseSourceCommit string

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
		printWorkerFailure(stderr, result.Code, result.Diagnostic, result.CompletionRecording)
		return 1
	}
	return 0
}

// Both execution entrypoints render the same safe, retained diagnostic. Never
// print the worker error: its chain can include private backend/filesystem details.
func printWorkerFailure(w io.Writer, code string, d *hostrun.PreparationDiagnostic, recording string) {
	if d != nil {
		phase := "preparation"
		if code == "execution_failed" {
			phase = "execution"
		}
		fmt.Fprintf(w, "Campaign %s failed during %s (%s):\n%s\n", phase, d.Stage, code, d.Message)
		if d.Model.Valid() {
			fmt.Fprintln(w, "Operation:", d.Model.OperationID)
			if d.Model.Model != "" {
				fmt.Fprintln(w, "Model:", d.Model.Model)
			}
			if d.Model.RequestID != "" {
				fmt.Fprintln(w, "Provider request ID:", d.Model.RequestID)
			}
			if details := d.Model.ProviderDetailsText(); details != "" {
				fmt.Fprintln(w, details)
			}
		}
		if d.CleanupFailed {
			fmt.Fprintf(w, "Secondary failure: %s cleanup did not finish successfully; inspect retained campaign state.\n", phase)
		}
	} else {
		fmt.Fprintln(w, "campaign worker stopped:", code)
	}
	if recording == "unconfirmed" {
		fmt.Fprintln(w, "Secondary failure: saving the start completion is unconfirmed; this diagnostic may not be available through inspection.")
	}
	fmt.Fprintln(w, "Execution will not resume.")
}
