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

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

// Retained administration resolves the original run link without requiring any
// installed model, secret-store, Docker or native service connection.
func retainedSelection(ctx context.Context, run, id, root, config string, defaults hostconfig.Paths) (string, string, error) {
	if (run == "") == (id == "") || (run != "" && (root != "" || config != "")) {
		return "", "", errors.New("invalid selection")
	}
	if run != "" {
		dir, e := filepath.Abs(run)
		if e != nil {
			return "", "", e
		}
		link, _, e := startrequest.ObserveLink(ctx, dir)
		if e != nil {
			return "", "", e
		}
		return link.StateRoot, link.CampaignID, nil
	}
	if config != "" || root == "" {
		name := config
		if name == "" {
			name = defaults.ConfigFile
		}
		loaded, e := hostconfig.Load(name, defaults)
		if e != nil && (config != "" || !errors.Is(e, os.ErrNotExist)) {
			return "", "", e
		}
		if e == nil && root == "" {
			root = loaded.Config.State.Root
		}
	}
	if root == "" {
		root = defaults.StateRoot
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", "", errors.New("invalid root")
	}
	return root, id, nil
}

func importCampaignEvidence(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	flags := flag.NewFlagSet("campaign evidence import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("campaign", "", "campaign ID")
	run := flags.String("run", "", "saved run directory")
	root := flags.String("state-root", "", "private state root")
	config := flags.String("config", "", "administrator configuration")
	session := flags.String("session", "", "recorded native session ID")
	archive := flags.String("archive", "", "retained native tar archive")
	maximum := flags.Int64("max-archive-bytes", 0, "offline acceptance ceiling; defaults to saved campaign policy")
	if flags.Parse(args) != nil {
		return 2
	}
	empty := false
	flags.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			empty = true
		}
	})
	if empty || flags.NArg() != 0 || *session == "" || *archive == "" || *maximum < 0 {
		fmt.Fprintln(stderr, "invalid evidence import arguments")
		return 2
	}
	selected, campaignID, e := retainedSelection(ctx, *run, *id, *root, *config, defaults)
	if e != nil {
		fmt.Fprintln(stderr, "cannot resolve retained campaign")
		return 2
	}
	record, e := nativerecovery.ImportEvidence(ctx, selected, campaignID, *session, *archive, *maximum)
	if e != nil {
		fmt.Fprintln(stderr, "evidence import failed validation or publication; execution records are unchanged")
		return 1
	}
	if json.NewEncoder(stdout).Encode(record) != nil {
		return 1
	}
	return 0
}
