//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/purge"
	"github.com/intrusiveai/operator_sandbox/internal/supervisor"
)

type purgeRunner func(context.Context, string, purge.Selection, string) (purge.Result, error)

func purgeCampaign(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	return purgeCampaignWith(ctx, args, stdout, stderr, defaults, func(ctx context.Context, root string, selection purge.Selection, bin string) (purge.Result, error) {
		docker := &purgeDocker{bin: bin}
		executable, e := os.Executable()
		if e != nil {
			return purge.Result{}, e
		}
		manager, e := supervisor.New(executable)
		if e != nil {
			return purge.Result{}, e
		}
		return purge.Run(ctx, root, selection, docker, manager)
	})
}
func purgeCampaignWith(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths, run purgeRunner) int {
	f := flag.NewFlagSet("purge", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("campaign", "", "campaign ID")
	all := f.Bool("all", false, "all managed campaigns")
	root := f.String("state-root", "", "private state root")
	config := f.String("config", "", "administrator configuration")
	docker := f.String("docker-bin", "", "Docker executable")
	if f.Parse(args) != nil {
		return 2
	}
	empty := false
	f.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			empty = true
		}
	})
	if empty || f.NArg() != 0 || *all == (*id != "") || (*id != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`).MatchString(*id)) {
		fmt.Fprintln(stderr, "purge requires exactly one of --campaign ID or --all")
		return 2
	}
	selection := purge.Selection{CampaignID: *id, All: *all}
	lookup := *id
	if *all {
		lookup = "all"
	}
	selected, _, e := retainedSelection(ctx, "", lookup, *root, *config, defaults)
	if e != nil {
		fmt.Fprintln(stderr, "cannot resolve purge state root")
		return 2
	}
	result, e := run(ctx, selected, selection, *docker)
	if result.APIVersion != "" {
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
	}
	if e != nil {
		fmt.Fprintln(stderr, "purge refused or incomplete; resolve the reported condition and retry; no interrupted campaign resumes")
		return 1
	}
	return 0
}

// Resolve Docker only when the retained inventory contains a Docker binding.
// Attachment-only and absent campaigns do not require a working Docker CLI.
type purgeDocker struct {
	bin    string
	client *dockercontrol.Client
	err    error
}

func (d *purgeDocker) load() error {
	if d.client == nil && d.err == nil {
		d.client, d.err = dockercontrol.New(d.bin)
	}
	return d.err
}
func (d *purgeDocker) CheckInactive(ctx context.Context, b campaign.DockerBinding) dockercontrol.Inactivity {
	if d.load() != nil {
		return dockercontrol.Inactivity{State: "unknown", Code: "docker_unavailable"}
	}
	return d.client.CheckInactive(ctx, b)
}
func (d *purgeDocker) RemoveStopped(ctx context.Context, b campaign.DockerBinding) error {
	if e := d.load(); e != nil {
		return e
	}
	return d.client.RemoveStopped(ctx, b)
}
