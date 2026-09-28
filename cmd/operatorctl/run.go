//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"path/filepath"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
	"github.com/intrusiveai/operator_sandbox/internal/workerjob"
)

type runReceipt struct {
	APIVersion   string              `json:"api_version"`
	RunDirectory string              `json:"run_directory"`
	Status       string              `json:"status"`
	Submission   *submission.Receipt `json:"submission,omitempty"`
	Start        *startReceipt       `json:"start,omitempty"`
	Observation  *observationReceipt `json:"observation,omitempty"`
}

type runStart func(context.Context, string, []string, io.Writer, io.Writer, hostconfig.Paths) int

func combinedRun(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	return combinedRunWith(ctx, args, stdout, stderr, defaults, startCampaign)
}
func combinedRunWith(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths, start runStart) int {
	f := flag.NewFlagSet("run", flag.ContinueOnError)
	f.SetOutput(stderr)
	config := f.String("config", defaults.ConfigFile, "private installation configuration")
	bundle := f.String("bundle", "", "ScenarioBundle JSON")
	artifacts := f.String("artifacts", "", "bundle artifact directory")
	output := f.String("output", "", "new or identical submitted run directory")
	wait := f.Bool("wait", false, "observe completion without owning the worker")
	timeout := f.Duration("timeout", workerjob.PreparationTimeout+15*time.Second, "acceptance timeout")
	waitTimeout := f.Duration("wait-timeout", 35*time.Minute, "completion observer timeout")
	fresh := f.Bool("new-campaign", false, "explicitly select a fresh campaign")
	prompt := f.String("system-prompt", "", "replacement prompt")
	var skills, appends stringsFlag
	f.Var(&skills, "skill", "installed skill digest (repeatable)")
	f.Var(&appends, "system-prompt-append", "prompt extension (repeatable)")
	var selection targetFlags
	selection.bind(f)
	if f.Parse(args) != nil || f.NArg() != 0 || *config == "" || *bundle == "" || *output == "" || !selection.valid() || (selection.capabilities == "" && selection.environment == "") || (*prompt != "" && len(appends) > 0) || len(skills) > 16 || len(appends) > 16 || *timeout <= 0 || *timeout > 10*time.Minute || *waitTimeout <= 0 || *waitTimeout > 24*time.Hour {
		return 2
	}
	empty := false
	f.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			empty = true
		}
	})
	if empty {
		return 2
	}
	directory, err := filepath.Abs(*output)
	if err != nil {
		return 2
	}
	result := runReceipt{APIVersion: "operator.dev/run-receipt/v1alpha1", RunDirectory: directory, Status: "submission_failed"}
	finish := func(code int) int {
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
		return code
	}
	submitArgs := []string{"submit", "--config", *config, "--bundle", *bundle, "--output", directory}
	for _, pair := range [][2]string{{"--environment", selection.environment}, {"--target-profile", selection.profile}, {"--capabilities", selection.capabilities}, {"--artifacts", *artifacts}} {
		if pair[1] != "" {
			submitArgs = append(submitArgs, pair[0], pair[1])
		}
	}
	var captured bytes.Buffer
	code := submissionCommandWith(ctx, submitArgs, &captured, stderr, defaults, true)
	if code != 0 {
		return finish(code)
	}
	var submitted submission.Receipt
	if json.Unmarshal(captured.Bytes(), &submitted) != nil {
		return finish(1)
	}
	result.Submission = &submitted
	result.Status = "start_failed"
	startArgs := []string{"--run", directory, "--config", *config, "--timeout", timeout.String()}
	if *fresh {
		startArgs = append(startArgs, "--new-campaign")
	}
	if *prompt != "" {
		startArgs = append(startArgs, "--system-prompt", *prompt)
	}
	for _, s := range skills {
		startArgs = append(startArgs, "--skill", s)
	}
	for _, s := range appends {
		startArgs = append(startArgs, "--system-prompt-append", s)
	}
	captured.Reset()
	code = start(ctx, "start", startArgs, &captured, stderr, defaults)
	if captured.Len() > 0 {
		var saved startReceipt
		if json.Unmarshal(captured.Bytes(), &saved) != nil {
			return finish(1)
		}
		result.Start = &saved
	}
	if code != 0 {
		return finish(code)
	}
	if result.Start == nil {
		return finish(1)
	}
	result.Status = "accepted"
	if !*wait {
		return finish(0)
	}
	captured.Reset()
	code = observeCampaign(ctx, "wait", []string{"--run", directory, "--timeout", waitTimeout.String()}, &captured, stderr, defaults)
	if captured.Len() > 0 {
		var observed observationReceipt
		if json.Unmarshal(captured.Bytes(), &observed) != nil {
			return finish(1)
		}
		result.Observation = &observed
	}
	result.Status = "observation_stopped"
	if code == 0 {
		result.Status = "execution_closed"
	}
	return finish(code)
}
