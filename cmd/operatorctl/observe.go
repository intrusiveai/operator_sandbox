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
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

type observationReceipt struct {
	Start            *startReceipt `json:"start,omitempty"`
	APIVersion       string        `json:"api_version"`
	CampaignID       string        `json:"campaign_id"`
	LaunchID         string        `json:"launch_id"`
	ManifestDigest   string        `json:"run_manifest_digest"`
	Verified         bool          `json:"prefix_verified"`
	Sequence         int64         `json:"sequence"`
	Bytes            int64         `json:"journal_bytes"`
	Revision         int64         `json:"run_revision"`
	LastKind         string        `json:"last_event_kind"`
	TerminalRecorded bool          `json:"terminal_recorded"`
	ExecutionState   string        `json:"execution_state"`
	Emitted          int64         `json:"emitted_events"`
	NextAfter        int64         `json:"next_after"`
	More             bool          `json:"more_events"`
}

// These commands only read retained records. Neither polling nor cancellation
// owns the worker, target, Docker process, or their termination controls.
func observeCampaign(ctx context.Context, action string, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	flags := flag.NewFlagSet("campaign "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("campaign", "", "campaign ID")
	runDir := flags.String("run", "", "submitted run directory with a saved start")
	config := flags.String("config", "", "administrator configuration path")
	root := flags.String("state-root", "", "private state root")
	var after, limit int64
	var timeout time.Duration
	if action == "logs" {
		flags.Int64Var(&after, "after", 0, "exclusive event sequence cursor")
		flags.Int64Var(&limit, "limit", 1000, "maximum events to emit (1..10000)")
	}
	if action == "wait" {
		flags.DurationVar(&timeout, "timeout", 35*time.Minute, "maximum observer wait (positive, at most 24h)")
	}
	if flags.Parse(args) != nil {
		return 2
	}
	invalidEmpty := false
	flags.Visit(func(f *flag.Flag) {
		if (f.Name == "config" && *config == "") || (f.Name == "state-root" && *root == "") {
			invalidEmpty = true
		}
	})
	if flags.NArg() != 0 || (*id == "") == (*runDir == "") || (*runDir != "" && (*config != "" || *root != "")) || invalidEmpty || after < 0 || after > contracts.MaxSafeInteger || (action == "logs" && (limit < 1 || limit > 10000)) || (action == "wait" && (timeout <= 0 || timeout > 24*time.Hour)) {
		fmt.Fprintln(stderr, "invalid campaign observation arguments")
		return 2
	}
	var linked *startrequest.RunLink
	if *runDir != "" {
		directory, err := filepath.Abs(*runDir)
		if err != nil {
			return 2
		}
		link, _, err := startrequest.ObserveLink(ctx, directory)
		if err != nil {
			fmt.Fprintln(stderr, "cannot resolve saved campaign start:", err)
			return 1
		}
		linked = &link
		*root, *id = link.StateRoot, link.CampaignID
	}
	if *config != "" || *root == "" {
		name := *config
		if name == "" {
			name = defaults.ConfigFile
		}
		loaded, err := hostconfig.Load(name, defaults)
		if err != nil && (*config != "" || !errors.Is(err, os.ErrNotExist)) {
			fmt.Fprintln(stderr, "cannot load host configuration:", err)
			return 2
		}
		if err == nil && *root == "" {
			*root = loaded.Config.State.Root
		}
	}
	if *root == "" {
		*root = defaults.StateRoot
	}
	if !filepath.IsAbs(*root) {
		fmt.Fprintln(stderr, "state root must be absolute")
		return 2
	}
	if action == "wait" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	encoder := json.NewEncoder(stdout)
	for {
		result := observationReceipt{APIVersion: "operator.dev/campaign-observation/v1alpha1", CampaignID: *id, ExecutionState: "unknown", NextAfter: after}
		var start *startrequest.Snapshot
		if linked != nil {
			value, err := startrequest.Observe(ctx, linked.StateRoot, linked.StartRequestID)
			if err != nil || value.Digest != linked.RequestDigest {
				fmt.Fprintln(stderr, "cannot verify saved campaign start")
				return 1
			}
			start = &value
			receipt := describeStart(value, "not_requested")
			result.Start = &receipt
		}
		snapshot, err := campaign.Observe(ctx, *root, *id, func(e campaign.Event) error {
			result.Revision, result.LastKind = e.RunRevision, e.Kind
			if e.Kind == "launch.terminal" {
				result.TerminalRecorded = true
			}
			if action == "logs" && e.Sequence > after && result.Emitted < limit {
				if err := encoder.Encode(struct {
					Type  string         `json:"type"`
					Event campaign.Event `json:"event"`
				}{"event", e}); err != nil {
					return err
				}
				result.Emitted++
				result.NextAfter = e.Sequence
			}
			return nil
		})
		result.LaunchID, result.ManifestDigest = snapshot.Manifest.LaunchID, snapshot.ManifestDigest
		result.Sequence, result.Bytes = snapshot.VerifiedEvents, snapshot.VerifiedBytes
		if result.Revision == 0 {
			result.Revision = snapshot.Manifest.InitialRevision
		}
		result.Verified = err == nil
		result.More = action == "logs" && result.NextAfter < result.Sequence
		// Preparation can fail before a journal exists. Report the immutable job
		// record without implying that execution is healthy or evidence verified.
		if err != nil && errors.Is(err, os.ErrNotExist) && start != nil && start.Accepted == nil {
			if _, statErr := os.Lstat(filepath.Join(*root, "campaigns", *id)); errors.Is(statErr, os.ErrNotExist) {
				err = nil
			}
		}
		if result.Verified && result.TerminalRecorded {
			result.ExecutionState = "closed"
		}
		if err != nil {
			// A final receipt distinguishes a failed scan from a complete log page.
			_ = encoder.Encode(result)
			fmt.Fprintln(stderr, "campaign observation failed:", err)
			return 1
		}
		if action != "wait" || result.TerminalRecorded || (start != nil && start.Completion != nil) {
			if err := encoder.Encode(result); err != nil {
				return 1
			}
			if action == "wait" && start != nil && start.Completion != nil && start.Completion.Status == "failed" {
				return 1
			}
			return 0
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = encoder.Encode(result)
			fmt.Fprintln(stderr, "campaign observer stopped:", ctx.Err())
			return 1
		case <-timer.C:
		}
	}
}
