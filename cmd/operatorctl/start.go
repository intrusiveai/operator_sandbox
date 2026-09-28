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
	"reflect"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
	"github.com/intrusiveai/operator_sandbox/internal/supervisor"
	"github.com/intrusiveai/operator_sandbox/internal/workerjob"
)

type startReceipt struct {
	APIVersion     string                   `json:"api_version"`
	StartRequestID string                   `json:"start_request_id"`
	CampaignID     string                   `json:"campaign_id"`
	RequestDigest  string                   `json:"request_digest"`
	Phase          string                   `json:"phase"`
	Submission     string                   `json:"service_submission"`
	Accepted       *hostrun.Receipt         `json:"accepted,omitempty"`
	Completion     *startrequest.Completion `json:"completion,omitempty"`
	Retired        *startrequest.Retirement `json:"retired,omitempty"`
}

func describeStart(s startrequest.Snapshot, submission string) startReceipt {
	return startReceipt{"operator.dev/start-observation/v1alpha1", s.Request.Selection.StartRequestID, s.Request.Selection.CampaignID, s.Digest, s.Phase(), submission, s.Accepted, s.Completion, s.Retired}
}

type stringsFlag []string

func (f *stringsFlag) String() string         { return fmt.Sprint([]string(*f)) }
func (f *stringsFlag) Set(value string) error { *f = append(*f, value); return nil }

type startDependencies struct {
	freeze func(context.Context, string, hostconfig.Paths, hostrun.Selection) (startrequest.Request, error)
	submit func(context.Context, string, string, string) error
}

func startCampaign(ctx context.Context, action string, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	deps := startDependencies{freeze: func(ctx context.Context, file string, paths hostconfig.Paths, selection hostrun.Selection) (startrequest.Request, error) {
		inputs, err := hostrun.LoadInputs(ctx, file, paths, selection)
		if err != nil {
			return startrequest.Request{}, err
		}
		return startrequest.New(inputs)
	}, submit: func(ctx context.Context, root, id, digest string) error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		client, err := supervisor.New(executable)
		if err != nil {
			return err
		}
		return client.Submit(ctx, root, id, digest)
	}}
	return startCampaignWith(ctx, action, args, stdout, stderr, defaults, deps)
}

func startCampaignWith(ctx context.Context, action string, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths, deps startDependencies) int {
	f := flag.NewFlagSet("campaign "+action, flag.ContinueOnError)
	f.SetOutput(stderr)
	runDir := f.String("run", "", "submitted run directory")
	config := f.String("config", defaults.ConfigFile, "administrator configuration file")
	fresh := f.Bool("new-campaign", false, "select a fresh campaign instead of the saved start")
	replacement := f.String("system-prompt", "", "replacement prompt file")
	skillSet := f.String("skill-set", "", "frozen SkillSetManifest file")
	var skills, appends stringsFlag
	f.Var(&skills, "skill", "installed signed skill digest (repeatable)")
	f.Var(&appends, "system-prompt-append", "prompt extension file (repeatable)")
	timeout := f.Duration("timeout", workerjob.PreparationTimeout+15*time.Second, "maximum acceptance wait")
	if f.Parse(args) != nil {
		return 2
	}
	provided := map[string]bool{}
	f.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if f.NArg() != 0 || (provided["skill-set"] && (*skillSet == "" || provided["skill"])) || *runDir == "" || *config == "" || (provided["system-prompt"] && *replacement == "") || (*replacement != "" && len(appends) > 0) || *timeout <= 0 || *timeout > 10*time.Minute || len(skills) > 16 || len(appends) > 16 {
		fmt.Fprintln(stderr, "invalid campaign start arguments")
		return 2
	}
	directory, err := filepath.Abs(*runDir)
	if err != nil {
		return 2
	}
	lock, err := startrequest.LockRun(directory)
	if err != nil {
		fmt.Fprintln(stderr, "cannot lock submitted run:", err)
		return 1
	}
	defer lock.Close()
	_, saved, readErr := startrequest.ObserveLink(ctx, directory)
	if readErr != nil {
		// A dangling or damaged link must never silently become a new start.
		_, linkErr := os.Lstat(filepath.Join(directory, "start.json"))
		if !*fresh && (!errors.Is(readErr, os.ErrNotExist) || !errors.Is(linkErr, os.ErrNotExist)) {
			fmt.Fprintln(stderr, "cannot resolve saved campaign start:", readErr)
			return 1
		}
	}
	var selection hostrun.Selection
	paths := defaults
	if readErr == nil && !*fresh {
		selection = saved.Request.Selection
		paths = saved.Request.Defaults()
		if !provided["config"] {
			*config = saved.Request.ConfigurationFile
		}
	} else {
		selection = hostrun.NewSelection(directory)
	}
	if provided["skill-set"] {
		selection.SkillSetFile, err = filepath.Abs(*skillSet)
		if err != nil {
			return 2
		}
		selection.SkillDigests = []string{}
	}
	if provided["skill"] {
		selection.SkillSetFile = ""
		selection.SkillDigests = append([]string(nil), skills...)
	}
	if provided["system-prompt"] {
		selection.PromptMode = "replacement"
		selection.AppendFiles = nil
		selection.ReplacementFile, err = filepath.Abs(*replacement)
	}
	if len(appends) > 0 {
		selection.PromptMode = "extension"
		selection.ReplacementFile = ""
		selection.AppendFiles = nil
		for _, p := range appends {
			if p == "" {
				err = hostrun.ErrSession
				break
			}
			var abs string
			abs, err = filepath.Abs(p)
			if err != nil {
				break
			}
			selection.AppendFiles = append(selection.AppendFiles, abs)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "invalid prompt selection")
		return 2
	}
	request, err := deps.freeze(ctx, *config, paths, selection)
	if err != nil {
		fmt.Fprintln(stderr, "campaign input validation failed:", err)
		return 1
	}
	if readErr == nil && !*fresh && !reflect.DeepEqual(request, saved.Request) {
		fmt.Fprintln(stderr, "campaign inputs changed; use --new-campaign for a new execution")
		return 1
	}
	snapshot, err := startrequest.Save(ctx, request)
	if err != nil {
		fmt.Fprintln(stderr, "cannot save campaign start:", err)
		return 1
	}
	if err := lock.PublishLink(ctx, snapshot, *fresh); err != nil {
		fmt.Fprintln(stderr, "cannot publish campaign start link:", err)
		return 1
	}
	submission := "not_requested"
	if action == "start" && snapshot.Claim == nil && snapshot.Completion == nil && snapshot.Retired == nil {
		submission = "submitted"
		if err := deps.submit(ctx, request.StateRoot, request.Selection.StartRequestID, snapshot.Digest); err != nil {
			submission = "unconfirmed"
		}
	}
	if err := lock.Close(); err != nil {
		fmt.Fprintln(stderr, "cannot release run lock")
		return 1
	}
	if action == "prepare" {
		if json.NewEncoder(stdout).Encode(describeStart(snapshot, submission)) != nil {
			return 1
		}
		return 0
	}
	if submission == "unconfirmed" {
		// A lost submission reply may have launched the worker. Return its exact
		// lookup key, without claiming acceptance or starting a replacement.
		if latest, e := startrequest.Observe(ctx, request.StateRoot, request.Selection.StartRequestID); e == nil {
			snapshot = latest
		}
		_ = json.NewEncoder(stdout).Encode(describeStart(snapshot, submission))
		fmt.Fprintln(stderr, "worker submission unconfirmed; inspect campaign status using --run")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	for {
		snapshot, err = startrequest.Observe(ctx, request.StateRoot, request.Selection.StartRequestID)
		if err != nil {
			fmt.Fprintln(stderr, "cannot read campaign acceptance:", err)
			return 1
		}
		if snapshot.Accepted != nil || snapshot.Completion != nil || snapshot.Retired != nil {
			if json.NewEncoder(stdout).Encode(describeStart(snapshot, submission)) != nil {
				return 1
			}
			if snapshot.Retired != nil || (snapshot.Completion != nil && snapshot.Completion.Status == "failed") {
				return 1
			}
			return 0
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = json.NewEncoder(stdout).Encode(describeStart(snapshot, submission))
			fmt.Fprintln(stderr, "campaign acceptance wait stopped; worker lifetime is independent")
			return 1
		case <-timer.C:
		}
	}
}
