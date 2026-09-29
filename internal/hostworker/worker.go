//go:build linux || darwin

// Package hostworker owns one freshly prepared campaign's runtime. It cannot
// reconstruct execution from a journal or restart an accepted container.
package hostworker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostlifetime"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

var ErrWorker = errors.New("host worker cannot start or continue this campaign")

type Docker interface {
	campaignservice.Runtime
	Create(context.Context, *dockercontrol.LaunchPlan) (campaign.DockerBinding, error)
	StartCreated(context.Context, string, campaign.DockerBinding, *dockercontrol.LaunchPlan) error
	WatchEvents(context.Context, campaign.DockerBinding, time.Time) error
	RemoveStopped(context.Context, campaign.DockerBinding) error
}

type Config struct {
	Service         campaignservice.Config // Prepared target, peer, model/evidence services and deadline.
	Docker          Docker
	Image           imagerelease.Prepared
	Requirements    imagerelease.Requirements
	Tree            *staging.Tree
	Channel         *transport.Session
	Inputs          campaign.LaunchInputs // Messages are ignored; replies must arrive from the live channel.
	PolicyDirectory string
	StartRequestID  string                // Host-issued 32-character idempotency key, durably recorded before create.
	Lifetime        *hostlifetime.Manager // Nil selects the installed platform backend.
}

type Worker struct {
	config Config
	plan   *dockercontrol.LaunchPlan
	power  *hostlifetime.Manager
	used   atomic.Bool
}

type Result struct {
	StopAccepted     bool                           `json:"stop_accepted"`
	LaunchPhase      string                         `json:"launch_phase"`
	ContainerCleanup string                         `json:"container_cleanup"`
	Binding          campaign.DockerBinding         `json:"binding"`
	Terminal         campaignservice.TerminalResult `json:"terminal"`
	Termination      termination.Receipt            `json:"termination"`
	TransportCleanup string                         `json:"transport_cleanup"`
	InputCleanup     string                         `json:"input_cleanup"`
}

// New binds fresh in-memory preparation to installed release authority and a
// single-use launch plan. No process/container starts here. Caller retains ownership
// of the writer and must not close/mutate dependencies until Run has returned.
func New(ctx context.Context, c Config) (*Worker, error) {
	if c.Service.Prepared == nil || c.Service.Peer == nil || !filepath.IsAbs(c.Service.StateRoot) || c.Docker == nil || c.Tree == nil || c.Channel == nil || !campaign.ValidStopRequest(c.StartRequestID, "start") || c.Requirements.HostPlatform != runtime.GOOS+"/"+runtime.GOARCH {
		return nil, ErrWorker
	}
	w := c.Service.Prepared.Writer()
	m := w.Manifest()
	pin, _ := c.Service.Prepared.Target().Protocol().PackageIdentity()
	if c.Image.Image.HostPlatform != c.Requirements.HostPlatform || m.HostPlatform != c.Requirements.HostPlatform || c.Requirements.Contract != pin || c.Image.Release.Digest() == "" || c.Image.Release.Digest() != m.ReleaseRecordDigest || c.Image.Release.Record().Compatible(m.ImageDigest, c.Requirements) != nil || c.Requirements.RuntimeProfile != m.RuntimeProfile {
		return nil, ErrWorker
	}
	if c.Service.Deadline.IsZero() || !c.Service.Deadline.After(time.Now()) || c.Service.Deadline.After(time.Now().Add(30*time.Minute)) {
		return nil, ErrWorker
	}
	var remaining struct {
		Milliseconds int64 `json:"campaign_time_ms"`
	}
	if json.Unmarshal(m.RemainingLimits, &remaining) != nil || remaining.Milliseconds < 1 || remaining.Milliseconds > 1800000 {
		return nil, ErrWorker
	}
	limit := time.Now().Add(time.Duration(remaining.Milliseconds) * time.Millisecond)
	if limit.Before(c.Service.Deadline) {
		c.Service.Deadline = limit
	}
	c.Inputs = cloneInputs(c.Inputs)
	if _, err := StartupMessages(c.Service.Prepared.Target().Protocol(), m, c.Inputs); err != nil {
		return nil, err
	}
	p, err := dockercontrol.NewLaunchPlan(ctx, c.PolicyDirectory, c.Image.Image, m, c.Tree, c.Channel)
	if err != nil {
		return nil, err
	}
	power := c.Lifetime
	if power == nil {
		power = hostlifetime.New(w.Fence().Stop)
	}
	return &Worker{config: c, plan: p, power: power}, nil
}

// Run owns bootstrap, ordinary dispatch, event/power failure and terminal cleanup.
// All failures fence. Stale files and unknown Docker outcomes cannot authorize a
// second Run. Shutdown confirmation has its own context independent of ctx.
func (w *Worker) Run(ctx context.Context) (result Result, runErr error) {
	if w == nil || !w.used.CompareAndSwap(false, true) {
		return result, ErrWorker
	}
	c := w.config
	writer := c.Service.Prepared.Writer()
	fence := writer.Fence()
	m := writer.Manifest()
	result.LaunchPhase = "prepare"
	result.ContainerCleanup = "not-created"
	result.TransportCleanup = "retained"
	result.InputCleanup = "retained"
	var err error
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	stopCancel := context.AfterFunc(ctx, func() { fence.Stop(ctx.Err()) })
	defer stopCancel()
	timer := time.AfterFunc(time.Until(c.Service.Deadline), func() { fence.Stop(context.DeadlineExceeded) })
	defer timer.Stop()
	go func() {
		select {
		case <-fence.Done():
			cancel()
		case <-life.Done():
		}
	}()
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	var service *campaignservice.Service
	created := false
	var startPending atomic.Bool
	terminalReserved := false
	defer func() {
		fence.Stop(runErr)
		cancel()
		if service != nil {
			// The service already bounds native cleanup/evidence independently. Waiting
			// does not delay its parallel Docker kill or release the power assertion early.
			result.Terminal, result.Termination, err = service.Wait(context.Background())
			runErr = errors.Join(runErr, err)
			result.StopAccepted = service.StopAccepted()
		} else if result.Binding.DockerContainerID != "" {
			result.Termination = termination.New(c.Docker).Watch(context.Background(), fence, c.Service.StateRoot, result.Binding)
		}
		if startPending.Load() {
			result.Termination.Outcome.Confirmed = false
			result.Termination.Outcome.State = "unknown"
			result.Termination.Outcome.Code = "startup_outcome_unconfirmed"
		}
		_ = c.Channel.Close()
		// Unknown create may have left a stopped container retaining these mounts.
		// Unknown termination may still have live execution. Preserve both explicitly.
		safe := !created || result.Termination.Outcome.Confirmed
		if result.Termination.Outcome.Confirmed {
			if err = c.Docker.RemoveStopped(context.Background(), result.Binding); err == nil {
				result.ContainerCleanup = "removed"
			} else {
				result.ContainerCleanup = "unconfirmed"
				runErr = errors.Join(runErr, err)
			}
		}
		if safe {
			if err = c.Channel.CleanupAfterExit(true); err == nil {
				result.TransportCleanup = "removed"
			} else {
				runErr = errors.Join(runErr, err)
			}
			if err = c.Tree.Discard(); err == nil {
				result.InputCleanup = "removed"
			} else {
				runErr = errors.Join(runErr, err)
			}
		}
		runErr = errors.Join(runErr, w.plan.Discard())
		raw, _ := json.Marshal(result)
		entry := campaign.Entry{RunRevision: writer.Revision(), Kind: "launch.terminal", Metadata: json.RawMessage(`{"execution_admission":"closed"}`), Content: []campaign.Content{{Role: "launch-result", MediaType: "application/json", Bytes: raw}}}
		var recordErr error
		if terminalReserved {
			_, recordErr = writer.AppendReserved(entry, "launch-terminal", true)
		} else {
			_, recordErr = writer.Append(entry)
		}
		runErr = errors.Join(runErr, recordErr)
	}()
	// AfterFunc is asynchronous, even for an already canceled context. Do not
	// classify a create as uncertain when execution was canceled before Run.
	if err = life.Err(); err != nil {
		return result, err
	}
	release, err = w.power.Acquire(ctx)
	if err != nil {
		return result, err
	}
	if err = w.power.Check(); err != nil {
		return result, err
	}
	mounts, mountErr := c.Channel.LaunchMounts(m.CampaignID, m.LaunchID, m.Transport, c.Channel.AccessGroup())
	if mountErr != nil {
		return result, mountErr
	}
	metadata, _ := json.Marshal(map[string]any{"start_request_id": c.StartRequestID, "image_id": c.Image.Image.ImageID, "endpoint": c.Image.Image.Endpoint, "daemon_id": c.Image.Image.DaemonID, "input_directory": c.Tree.Directory(), "transport_mounts": mounts})
	if _, err = writer.AppendReserving(campaign.Entry{RunRevision: m.InitialRevision, Kind: "launch.start-intent", Metadata: metadata, Content: []campaign.Content{{Role: "release", MediaType: "application/json", Bytes: c.Image.Release.Bytes()}}}, "launch-terminal", 1<<20); err != nil {
		return result, err
	}
	terminalReserved = true
	since := time.Now()
	result.LaunchPhase = "create"
	result.ContainerCleanup = "retained"
	created = true
	result.Binding, err = c.Docker.Create(life, w.plan)
	if result.Binding.DockerContainerID != "" {
		saveErr := writer.SaveDockerBinding(result.Binding)
		err = errors.Join(err, saveErr)
	}
	if err != nil {
		return result, err
	}
	c.Service.Docker = result.Binding
	c.Service.Runtime = guardedRuntime{Docker: c.Docker, power: w.power, startPending: &startPending}
	c.Service.Deadline = w.config.Service.Deadline
	originalCheck := c.Service.CheckHost
	c.Service.CheckHost = func() error {
		if err := w.power.Check(); err != nil {
			return err
		}
		if originalCheck != nil {
			return originalCheck()
		}
		return nil
	}
	startPending.Store(true)
	service, err = campaignservice.New(life, c.Service)
	if err != nil {
		startPending.Store(false)
		return result, err
	}
	binding := result.Binding
	go func() {
		err := c.Docker.WatchEvents(life, binding, since)
		if life.Err() == nil {
			if err == nil {
				err = dockercontrol.ErrEvents
			}
			fence.Stop(err)
		}
	}()
	result.LaunchPhase = "start"
	if err = c.Docker.StartCreated(life, c.Service.StateRoot, result.Binding, w.plan); err != nil {
		return result, err
	}
	if err = life.Err(); err != nil {
		return result, err
	}
	startPending.Store(false)
	go func() {
		if err := c.Channel.Run(life); err != nil {
			fence.Stop(err)
		}
	}()
	check := func(ctx context.Context) error {
		if err := w.power.Check(); err != nil {
			return err
		}
		if err := c.Tree.Verify(ctx); err != nil {
			return err
		}
		return c.Docker.CheckRunning(ctx, result.Binding)
	}
	result.LaunchPhase = "bootstrap"
	if err = Bootstrap(life, c.Service.Prepared.Target().Protocol(), writer, c.Channel, service, c.Inputs, check); err != nil {
		return result, err
	}
	result.LaunchPhase = "running"
	go func() {
		err := service.ServeOrdinary(life, c.Channel)
		if err != nil {
			fence.Stop(err)
		}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-fence.Done():
			return result, fence.Err()
		case <-life.Done():
			return result, life.Err()
		case <-ticker.C:
			if _, ok := c.Channel.Receive("control-out"); ok {
				return result, ErrWorker
			}
			if err = w.power.Check(); err != nil {
				return result, err
			}
			probe, done := context.WithTimeout(life, dockercontrol.ConfirmationTimeout)
			err = c.Docker.CheckRunning(probe, result.Binding)
			done()
			if err != nil {
				return result, err
			}
		}
	}
}

type guardedRuntime struct {
	Docker
	power        *hostlifetime.Manager
	startPending *atomic.Bool
}

func (r guardedRuntime) CheckRunning(ctx context.Context, b campaign.DockerBinding) error {
	if err := r.power.Check(); err != nil {
		return err
	}
	return r.Docker.CheckRunning(ctx, b)
}

// A stop overlapping an unconfirmed start cannot prove that a late daemon-side
// start will not execute. Preserve uncertainty in the durable termination receipt.
func (r guardedRuntime) Terminate(ctx context.Context, b campaign.DockerBinding) dockercontrol.Outcome {
	pending := r.startPending.Load()
	out := r.Docker.Terminate(ctx, b)
	if pending || r.startPending.Load() {
		out.Confirmed = false
		out.State = "unknown"
		out.Code = "startup_outcome_unconfirmed"
	}
	return out
}

// Discard releases the generated launch policy for a worker that never ran. It
// cannot remove a running worker's policy or make a launch plan reusable.
func (w *Worker) Discard() error {
	if w == nil || !w.used.CompareAndSwap(false, true) {
		return ErrWorker
	}
	return w.plan.Discard()
}
