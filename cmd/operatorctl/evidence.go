//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
)

type evidenceCollector func(context.Context, string, string, string) (nativerecovery.EvidenceReport, error)

func collectCampaignEvidence(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	return collectCampaignEvidenceWith(ctx, args, stdout, stderr, defaults, func(ctx context.Context, root, id, executable string) (nativerecovery.EvidenceReport, error) {
		docker, e := dockercontrol.New(executable)
		if e != nil {
			return nativerecovery.EvidenceReport{}, e
		}
		peer := interceptor.New()
		defer peer.Close()
		return nativerecovery.CollectEvidence(ctx, root, id, docker, peer)
	})
}
func collectCampaignEvidenceWith(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths, collect evidenceCollector) int {
	flags := flag.NewFlagSet("campaign evidence collect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("campaign", "", "campaign ID")
	config := flags.String("config", "", "administrator configuration file")
	root := flags.String("state-root", "", "private state root")
	executable := flags.String("docker-bin", "", "absolute Docker CLI executable")
	if flags.Parse(args) != nil {
		return 2
	}
	empty := false
	flags.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			empty = true
		}
	})
	if flags.NArg() != 0 || *id == "" || empty {
		fmt.Fprintln(stderr, "invalid evidence collection arguments")
		return 2
	}
	// Like administrative termination, explicit recovery paths bypass current
	// configuration. Collection limits always come from the old campaign journal.
	if *config != "" || *root == "" {
		name := *config
		if name == "" {
			name = defaults.ConfigFile
		}
		loaded, e := hostconfig.Load(name, defaults)
		if e != nil && (*config != "" || !errors.Is(e, os.ErrNotExist)) {
			fmt.Fprintln(stderr, "cannot load host configuration")
			return 2
		}
		if e == nil {
			if *root == "" {
				*root = loaded.Config.State.Root
			}
			if *executable == "" {
				*executable = loaded.Config.Docker.Executable
			}
		}
	}
	if *root == "" {
		*root = defaults.StateRoot
	}
	if !filepath.IsAbs(*root) || filepath.Clean(*root) != *root {
		fmt.Fprintln(stderr, "state root must be a clean absolute path")
		return 2
	}
	result, e := collect(ctx, *root, *id, *executable)
	if result.APIVersion != "" {
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
	}
	if e != nil {
		fmt.Fprintln(stderr, "evidence collection could not be verified; retained execution records are unchanged")
		return 1
	}
	if !result.Complete() {
		return 1
	}
	return 0
}
