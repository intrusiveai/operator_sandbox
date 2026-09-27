package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostlifetime"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

type workerDocker struct {
	mu                     sync.Mutex
	writer                 *campaign.Writer
	root, spool, mode      string
	creates, starts, kills int
	binding                campaign.DockerBinding
	guestDone              chan error
	ready                  chan struct{}
	stop                   chan struct{}
	once                   sync.Once
}

func (d *workerDocker) Create(ctx context.Context, _ *dockercontrol.LaunchPlan) (campaign.DockerBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.creates++
	if d.mode == "create lost" {
		return campaign.DockerBinding{}, errors.New("create reply lost")
	}
	m := d.writer.Manifest()
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: d.writer.ManifestDigest(), Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	d.binding = b
	return b, nil
}
func (d *workerDocker) StartCreated(ctx context.Context, root string, b campaign.DockerBinding, _ *dockercontrol.LaunchPlan) error {
	d.mu.Lock()
	d.starts++
	d.mu.Unlock()
	saved, err := campaign.ReadDockerBinding(root, b.CampaignID)
	if err != nil || saved.DockerContainerID != b.DockerContainerID {
		return errors.New("binding not saved before start")
	}
	if d.mode == "start lost" {
		return errors.New("start reply lost")
	}
	go func() {
		mode := "success"
		if d.mode == "bad bootstrap" {
			mode = "wrong confinement"
		}
		err := bootstrapGuest(ctx, d.spool, mode)
		if err == nil {
			close(d.ready)
		}
		d.guestDone <- err
	}()
	return nil
}
func (d *workerDocker) CheckRunning(context.Context, campaign.DockerBinding) error { return nil }
func (d *workerDocker) WatchEvents(ctx context.Context, _ campaign.DockerBinding, _ time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.stop:
		return dockercontrol.ErrEvents
	}
}
func (d *workerDocker) Terminate(context.Context, campaign.DockerBinding) dockercontrol.Outcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kills++
	return dockercontrol.Outcome{Confirmed: d.mode != "kill unknown", State: map[bool]string{true: "unknown", false: "exited"}[d.mode == "kill unknown"], Code: map[bool]string{true: "container_unconfirmed", false: "confirmed_stopped"}[d.mode == "kill unknown"]}
}

func (d *workerDocker) RemoveStopped(context.Context, campaign.DockerBinding) error { return nil }

type workerPower struct {
	closed     bool
	checkClose func()
	mu         sync.Mutex
}

func (p *workerPower) Start(context.Context) (hostlifetime.Inhibitor, error) { return p, nil }
func (p *workerPower) SleepMarker() (string, error)                          { return "awake", nil }
func (p *workerPower) Done() <-chan struct{}                                 { return nil }
func (p *workerPower) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.checkClose != nil {
		p.checkClose()
	}
}

func cachedApproval(t *testing.T, p *contracts.Protocol) (imagerelease.Prepared, imagerelease.Requirements) {
	t.Helper()
	pin, _ := p.PackageIdentity()
	platform := "darwin/" + goruntime.GOARCH
	h := imagerelease.Requirements{OperatorVersion: "0.1.0", Contract: pin, HostPlatform: platform, RuntimeProfile: "operator-container/v1"}
	image := dockercontrol.ImagePin{Selector: "harness:latest", Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", ImageID: contracts.RawDigest([]byte("approved test image")), HostPlatform: platform, ImagePlatform: "linux/" + goruntime.GOARCH}
	record := imagerelease.Record{APIVersion: "intrusive.ai/engine-release/v1alpha1", ImageDigest: image.ImageID, MinimumOperatorVersion: "0.1.0", ContractPackageVersion: pin.Version, ContractPackageDigest: pin.Digest, RuntimeProfile: h.RuntimeProfile, Platform: image.ImagePlatform}
	raw := encode(record)
	cache := t.TempDir()
	_ = os.Chmod(cache, 0700)
	entry := encode(map[string]any{"api_version": "operator.dev/image-release-cache/v1alpha1", "origin": imagerelease.Origin, "image_id": image.ImageID, "retrieved_at": "2026-09-26T00:00:00Z", "response_base64": raw, "response_digest": contracts.RawDigest(raw)})
	name := strings.TrimPrefix(contracts.RawDigest([]byte(imagerelease.Origin)), "sha256:") + "-" + strings.TrimPrefix(image.ImageID, "sha256:") + ".json"
	if err := os.WriteFile(filepath.Join(cache, name), entry, 0600); err != nil {
		t.Fatal(err)
	}
	client, err := imagerelease.Open(cache)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	approval, err := client.Resolve(context.Background(), image.ImageID, h)
	if err != nil {
		t.Fatal(err)
	}
	return imagerelease.Prepared{Image: image, Release: approval}, h
}
func TestWorkerOwnsStartupFailureAndCleanup(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("full worker integration uses the macOS spool profile; FIFO mechanics have separate transport coverage")
	}
	for _, mode := range []string{"event loss", "requested stop", "bad bootstrap", "create lost", "start lost", "kill unknown"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			input := fixture(t)
			target, err := preparation.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			image, requirements := cachedApproval(t, input.Protocol)
			var operations []string
			if mode == "requested stop" {
				operations = []string{"engine.request_stop"}
			}
			writer, launch, root := preparedLaunchConfigured(t, target, func(m map[string]any) {
				m["release"] = map[string]any{"image_digest": image.Image.ImageID, "release_record_digest": image.Release.Digest()}
			}, requirements.HostPlatform, operations...)
			stored, err := target.Persist(writer, launch.EngineContext)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			_ = os.Chmod(dir, 0700)
			deadline := time.Now().Add(time.Minute)
			channel, err := transport.NewSpool(dir, transport.Config{Protocol: input.Protocol, CampaignID: "campaign-1", LaunchID: "launch-1", Fence: writer.Fence(), CampaignDeadline: deadline})
			if err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			_ = os.Chmod(parent, 0700)
			tree, err := staging.Create(ctx, input.Protocol, parent, staging.Manifests{InputTree: launch.InputTree, SkillSet: launch.SkillSet, Skills: launch.Skills}, map[string][]byte{"input/run-context.json": launch.EngineContext, "input/scenario-bundle.json": launch.ScenarioBundle, "input/system-prompt.txt": launch.Prompt})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tree.Discard(); _ = channel.Close() })
			docker := &workerDocker{writer: writer, root: root, spool: dir, mode: mode, guestDone: make(chan error, 1), ready: make(chan struct{}), stop: make(chan struct{})}
			power := &workerPower{checkClose: func() {
				docker.mu.Lock()
				defer docker.mu.Unlock()
				if mode != "create lost" && docker.kills == 0 {
					t.Error("power lease released before termination")
				}
			}}
			worker, err := hostworker.New(ctx, hostworker.Config{Service: campaignservice.Config{Prepared: stored, Peer: &peer{input: input, revision: 5}, StateRoot: root, Deadline: deadline}, Docker: docker, Image: image, Requirements: requirements, Tree: tree, Channel: channel, Inputs: launch, PolicyDirectory: parent, StartRequestID: strings.Repeat("a", 32), Lifetime: hostlifetime.NewWithBackend(power, writer.Fence().Stop)})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan struct{})
			var result hostworker.Result
			var runErr error
			go func() { result, runErr = worker.Run(ctx); close(finished) }()
			if mode == "event loss" || mode == "kill unknown" || mode == "requested stop" {
				select {
				case <-docker.ready:
					if mode == "event loss" {
						if err = spoolPut(dir, "ordinary-out", 0, attemptWire(&peer{input: input}, 1, 1, writer.Manifest().ReleaseRecordDigest)); err != nil {
							t.Fatal(err)
						}
						raw, e := spoolRead(ctx, dir, "ordinary-in", 0)
						if e != nil {
							t.Fatal(e)
						}
						var response map[string]any
						if json.Unmarshal(raw, &response) != nil || response["error"] != nil || response["result"].(map[string]any)["status"] != "completed" {
							t.Fatal(string(raw))
						}
						if e = spoolACK(dir, 0, 2); e != nil {
							t.Fatal(e)
						}
					}
					if mode == "requested stop" {
						request := stateWire("engine.request_stop", "stop", 1, map[string]any{"finish_reason": "budget-limit", "conclusion": map[string]any{"state": "unavailable", "reason": "budget-limit"}})
						if e := spoolPut(dir, "ordinary-out", 0, request); e != nil {
							t.Fatal(e)
						}
						raw, e := spoolRead(ctx, dir, "ordinary-in", 0)
						if e != nil {
							t.Fatal(e)
						}
						ack := stateResult(t, raw, nil)
						if ack["status"] != "accepted" {
							t.Fatal(ack)
						}
						if e = spoolACK(dir, 0, 2); e != nil {
							t.Fatal(e)
						}
					}
					close(docker.stop)
				case <-finished:
					t.Fatal("worker stopped before admission", runErr)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("worker did not finalize", ctx.Err())
			}
			if runErr == nil || docker.creates != 1 {
				t.Fatal("missing terminal result", runErr, docker.creates)
			}
			if _, err = worker.Run(ctx); err == nil || docker.creates != 1 {
				t.Fatal("worker restarted")
			}
			if result.StopAccepted != (mode == "requested stop") {
				t.Fatal("lost stop disposition", result)
			}
			retained := mode == "create lost" || mode == "start lost" || mode == "kill unknown"
			if retained {
				if result.TransportCleanup != "retained" || result.InputCleanup != "retained" {
					t.Fatal(result)
				}
			} else {
				if result.TransportCleanup != "removed" || result.InputCleanup != "removed" {
					t.Fatal(result, runErr)
				}
				if _, e := os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("spool left behind", e)
				}
			}
			if mode == "start lost" {
				record, e := campaign.ReadTerminationResult(root, "campaign-1")
				if e != nil || record == nil || record.Outcome.Confirmed {
					t.Fatal("durable startup uncertainty lost", record, e)
				}
			}
			if docker.starts > 0 && mode != "start lost" {
				select {
				case <-docker.guestDone:
				case <-ctx.Done():
					t.Fatal("guest leaked")
				}
			}
			if !power.closed {
				t.Fatal("power helper not released")
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			if _, err = campaign.Inspect(root, "campaign-1", func(e campaign.Event) error { found[e.Kind] = true; return nil }); err != nil {
				t.Fatal("invalid retained journal", err)
			}
			if !found["launch.start-intent"] || !found["launch.terminal"] {
				t.Fatal("launch records missing", found)
			}

		})
	}
}
