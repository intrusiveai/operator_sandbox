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
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
)

func submissionCommand(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	return submissionCommandWith(ctx, args, stdout, stderr, defaults, false)
}

func submissionCommandWith(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths, reuse bool) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := flags.String("config", defaults.ConfigFile, "administrator configuration with installed contract pin")
	var bundle, artifacts, output, run *string
	var selection targetFlags
	if args[0] == "submit" {
		bundle = flags.String("bundle", "", "ScenarioBundle JSON file")
		selection.bind(flags)
		artifacts = flags.String("artifacts", "", "hash-named bundle artifact directory")
		output = flags.String("output", "", "new run directory (parent must exist)")
	} else {
		run = flags.String("run", "", "submitted run directory")
	}
	if flags.Parse(args[1:]) != nil {
		return 2
	}
	if flags.NArg() != 0 || !selection.valid() || *config == "" || (run != nil && *run == "") || (bundle != nil && (*bundle == "" || (selection.capabilities == "" && selection.environment == "") || *output == "")) {
		fmt.Fprintln(stderr, "submit requires --bundle, --capabilities and --output; validate requires --run")
		return 2
	}
	loaded, err := hostconfig.Load(*config, defaults)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load host configuration:", err)
		return 1
	}
	c := loaded.Config.Contract
	installed, err := contractstore.LoadRuntime(ctx, c.Directory, contracts.PackageIdentity{Version: c.Version, Digest: c.Digest})
	if err != nil {
		fmt.Fprintln(stderr, "cannot verify installed contract:", err)
		return 1
	}
	// CLI paths may be relative; no path is read from a submitted descriptor.
	absolute := func(s string) string {
		if s == "" {
			return ""
		}
		p, err := filepath.Abs(s)
		if err != nil {
			return ""
		}
		return p
	}
	var prepared *submission.Prepared
	if run != nil {
		prepared, err = submission.Load(ctx, installed.Protocol(), absolute(*run))
	} else {
		profile, public, e := selection.resolve()
		if e != nil {
			fmt.Fprintln(stderr, "invalid target selection")
			return 1
		}
		prepared, err = submission.Read(ctx, installed.Protocol(), absolute(*bundle), public, absolute(*artifacts))
		if err == nil && profile != "" {
			err = prepared.SelectTarget(profile)
		}
		if err == nil {
			if reuse {
				err = prepared.SaveOrVerify(ctx, installed.Protocol(), absolute(*output))
			} else {
				err = prepared.Save(ctx, absolute(*output))
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "submission failed:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(prepared.Receipt()); err != nil {
		return 1
	}
	return 0
}
