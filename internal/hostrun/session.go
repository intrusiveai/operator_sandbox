//go:build linux || darwin

// Package hostrun composes verified launch inputs and owns their worker lifetime.
package hostrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostlifetime"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/startup"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

var ErrSession = errors.New("campaign launch session is invalid or already consumed")

type Config struct {
	HTTPS            *httpstarget.Client
	StateRoot        string
	Target           *preparation.Target
	Launch           preparation.LaunchConfig
	Requirements     imagerelease.Requirements
	Peer             campaignservice.Peer
	Docker           hostworker.Docker
	Provider         campaignservice.ModelProvider
	MinimumFreeBytes int64
	SpoolMaxBytes    int64
	EvidenceMaxBytes int64
	StartRequestID   string
	Gate             *startup.Gate // Ownership transfers after successful Claim, including later failures.
	Recovery         []startup.Prior
	Lifetime         *hostlifetime.Manager
}

type Receipt struct {
	APIVersion     string `json:"api_version"`
	CampaignID     string `json:"campaign_id"`
	LaunchID       string `json:"launch_id"`
	StartRequestID string `json:"start_request_id"`
	ManifestDigest string `json:"run_manifest_digest"`
	Status         string `json:"status"`
}

// Session holds a fresh worker and its private resources. Run and Cancel are
// mutually exclusive, single-use operations; neither opens a prior campaign.
type Session struct {
	worker   *hostworker.Worker
	writer   *campaign.Writer
	channel  *transport.Session
	tree     *staging.Tree
	gate     *startup.Gate
	receipt  Receipt
	consumed atomic.Bool
}

func (s *Session) Receipt() Receipt { return s.receipt }

// Prepare persists the complete immutable launch and fresh worker before returning
// an accepted receipt. It does not create/start the harness container or synthesize
// a guest handshake. The caller owns the peer/provider through Run completion.
func Prepare(ctx context.Context, c Config) (session *Session, err error) {
	if !c.Gate.Claim(c.StateRoot) {
		return nil, ErrSession
	}
	s := &Session{gate: c.Gate}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.discard())
		}
	}()
	if c.Gate == nil || c.Target == nil || c.Docker == nil || (c.Peer == nil && c.HTTPS == nil) || c.Provider == nil || c.Launch.Model == nil || !filepath.IsAbs(c.StateRoot) || !campaign.ValidStopRequest(c.StartRequestID, "start") || c.Requirements.HostPlatform != runtime.GOOS+"/"+runtime.GOARCH {
		return nil, ErrSession
	}
	if c.MinimumFreeBytes == 0 {
		c.MinimumFreeBytes = hostconfig.DefaultMinimumFreeBytes
	}
	if c.MinimumFreeBytes < 1 || c.MinimumFreeBytes > contracts.MaxSafeInteger {
		return nil, ErrSession
	}
	if c.EvidenceMaxBytes == 0 {
		c.EvidenceMaxBytes = hostconfig.DefaultEvidenceBytes
	}
	if c.EvidenceMaxBytes < 1 || c.EvidenceMaxBytes > contracts.MaxSafeInteger || c.SpoolMaxBytes < 0 || c.SpoolMaxBytes > contracts.MaxSafeInteger {
		return nil, ErrSession
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	launch, err := c.Target.BuildLaunch(c.Launch)
	if err != nil {
		return nil, err
	}
	var limits struct {
		Milliseconds int64 `json:"campaign_time_ms"`
	}
	if json.Unmarshal(launch.Manifest.RemainingLimits, &limits) != nil || limits.Milliseconds < 1 || limits.Milliseconds > 1800000 {
		return nil, ErrSession
	}
	deadline := time.Now().Add(time.Duration(limits.Milliseconds) * time.Millisecond)
	s.writer, err = campaign.Create(c.StateRoot, launch.Manifest)
	if err != nil {
		return nil, err
	}
	if err = s.writer.ConfigureFreeSpace(c.MinimumFreeBytes); err != nil {
		return nil, err
	}
	campaignDir := filepath.Join(c.StateRoot, "campaigns", launch.Manifest.CampaignID)
	root, err := os.OpenRoot(campaignDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, dir := range []string{"runtime", "runtime/transport", "launch/policies"} {
		if err := root.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	}
	s.tree, err = staging.Create(ctx, c.Target.Protocol(), filepath.Join(campaignDir, "launch"), staging.Manifests{InputTree: launch.Inputs.InputTree, SkillSet: launch.Inputs.SkillSet, Skills: launch.Inputs.Skills}, launch.Contents)
	if err != nil {
		return nil, err
	}
	if err = launch.RetainInputs(ctx, s.writer, s.tree); err != nil {
		return nil, err
	}
	stored, err := c.Target.Persist(s.writer, launch.Inputs.EngineContext)
	if err != nil {
		return nil, err
	}
	// Each recovery result stays bounded by the ordinary event metadata ceiling.
	for _, prior := range c.Recovery {
		raw, e := json.Marshal(prior)
		if e != nil {
			return nil, e
		}
		if _, err = s.writer.Append(campaign.Entry{RunRevision: launch.Manifest.InitialRevision, Kind: "campaign.prior-reconciled", Metadata: raw}); err != nil {
			return nil, err
		}
	}
	transportConfig := transport.Config{Protocol: c.Target.Protocol(), CampaignID: launch.Manifest.CampaignID, LaunchID: launch.Manifest.LaunchID, Fence: s.writer.Fence(), CampaignDeadline: deadline, SpoolMaxBytes: c.SpoolMaxBytes}
	path := filepath.Join(campaignDir, "runtime/transport")
	if launch.Manifest.Transport == "fifo" {
		s.channel, err = transport.NewFIFO(path, transportConfig)
	} else {
		s.channel, err = transport.NewSpool(path, transportConfig)
	}
	if err != nil {
		return nil, err
	}
	s.worker, err = hostworker.New(ctx, hostworker.Config{
		Service: campaignservice.Config{Prepared: stored, Peer: c.Peer, HTTPS: c.HTTPS, StateRoot: c.StateRoot, Deadline: deadline,
			Model:    &campaignservice.ModelConfig{Provider: c.Provider, ProfileDigest: launch.Manifest.ModelProfileDigest, Tools: launch.ModelTools, MaximumPromptTokens: c.Launch.Model.Settings().MaximumPromptTokens},
			Evidence: &campaignservice.EvidenceConfig{MaxArchiveBytes: c.EvidenceMaxBytes, Timeout: 2 * time.Minute, TotalTimeout: 5 * time.Minute}},
		Docker: c.Docker, Image: c.Launch.Image, Requirements: c.Requirements, Tree: s.tree, Channel: s.channel, Inputs: launch.Inputs, PolicyDirectory: filepath.Join(campaignDir, "launch/policies"), StartRequestID: c.StartRequestID, Lifetime: c.Lifetime,
	})
	if err != nil {
		return nil, err
	}
	s.receipt = Receipt{APIVersion: "operator.dev/campaign-start/v1alpha1", CampaignID: launch.Manifest.CampaignID, LaunchID: launch.Manifest.LaunchID, StartRequestID: c.StartRequestID, ManifestDigest: s.writer.ManifestDigest(), Status: "accepted"}
	raw, err := json.Marshal(s.receipt)
	if err != nil {
		return nil, err
	}
	if _, err = s.writer.Append(campaign.Entry{RunRevision: launch.Manifest.InitialRevision, Kind: "campaign.start-accepted", Metadata: raw}); err != nil {
		return nil, err
	}
	return s, nil
}

// Run owns finalization once entered. In particular it preserves uncertain
// container/mount state according to hostworker's independently confirmed result.
func (s *Session) Run(ctx context.Context) (result hostworker.Result, err error) {
	if s == nil || !s.consumed.CompareAndSwap(false, true) {
		return hostworker.Result{}, ErrSession
	}
	defer func() { err = errors.Join(err, s.writer.Close(), s.gate.Close()) }()
	return s.worker.Run(ctx)
}

// Cancel only discards a prepared session that never entered Run. It cannot be
// used to clean up a container whose launch or termination outcome is uncertain.
func (s *Session) Cancel() error {
	if s == nil || !s.consumed.CompareAndSwap(false, true) {
		return ErrSession
	}
	return s.discard()
}
func (s *Session) discard() error {
	var err error
	if s.worker != nil {
		err = errors.Join(err, s.worker.Discard())
	}
	if s.writer != nil {
		s.writer.Fence().Stop(ErrSession)
	}
	if s.channel != nil {
		err = errors.Join(err, s.channel.Close(), s.channel.CleanupAfterExit(true))
	}
	if s.tree != nil {
		err = errors.Join(err, s.tree.Discard())
	}
	if s.writer != nil && s.receipt.Status == "accepted" {
		result := hostworker.Result{LaunchPhase: "prepare", ContainerCleanup: "not-created", TransportCleanup: "removed", InputCleanup: "removed"}
		if err != nil {
			result.TransportCleanup = "unconfirmed"
			result.InputCleanup = "unconfirmed"
		}
		raw, _ := json.Marshal(result)
		_, recordErr := s.writer.Append(campaign.Entry{RunRevision: s.writer.Revision(), Kind: "launch.terminal", Metadata: json.RawMessage(`{"execution_admission":"closed"}`), Content: []campaign.Content{{Role: "launch-result", MediaType: "application/json", Bytes: raw}}})
		err = errors.Join(err, recordErr)
	}
	if s.writer != nil {
		err = errors.Join(err, s.writer.Close())
	}
	if s.gate != nil {
		err = errors.Join(err, s.gate.Close())
	}
	return err
}
