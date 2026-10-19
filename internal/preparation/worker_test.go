package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
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
	checking               chan struct{}
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
func (d *workerDocker) CheckRunning(ctx context.Context, _ campaign.DockerBinding) error {
	if d.mode == "poll cancellation" {
		select {
		case <-d.ready:
			d.once.Do(func() { close(d.checking) })
			<-ctx.Done()
			return ctx.Err()
		default:
		}
	}
	return nil
}
func (d *workerDocker) WatchEvents(ctx context.Context, _ campaign.DockerBinding, _ time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.stop:
		if d.mode == "requested stop" || d.mode == "poll cancellation" || d.mode == "harness limit" {
			return dockercontrol.ErrExited
		}
		return dockercontrol.ErrEvents
	}
}
func (d *workerDocker) Terminate(context.Context, campaign.DockerBinding) dockercontrol.Outcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kills++
	return dockercontrol.Outcome{Confirmed: d.mode != "kill unknown", State: map[bool]string{true: "unknown", false: "exited"}[d.mode == "kill unknown"], Code: map[bool]string{true: "container_unconfirmed", false: "confirmed_stopped"}[d.mode == "kill unknown"]}
}

func (d *workerDocker) RemoveStopped(context.Context, campaign.DockerBinding) error {
	if d.mode == "model cleanup failure" {
		return errors.New("fixture cleanup failure")
	}
	return nil
}
func (d *workerDocker) ObserveExit(context.Context, campaign.DockerBinding) campaign.HarnessExit {
	d.mu.Lock()
	defer d.mu.Unlock()
	return campaign.HarnessExit{ExitObserved: d.kills > 0, ExitCode: 0, StderrState: "empty"}
}

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

func localCompatibility(t *testing.T, p *contracts.Protocol, hostPlatform ...string) (imagerelease.Prepared, imagerelease.Requirements) {
	t.Helper()
	pin, _ := p.PackageIdentity()
	platform := "darwin/" + goruntime.GOARCH
	if len(hostPlatform) > 0 {
		platform = hostPlatform[0]
	}
	h := imagerelease.Requirements{OperatorVersion: "0.1.0", Contract: pin, HostPlatform: platform, RuntimeProfile: "operator-container/v1"}
	image := dockercontrol.ImagePin{Selector: "harness:latest", Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", ImageID: contracts.RawDigest([]byte("approved test image")), HostPlatform: platform, ImagePlatform: "linux/" + goruntime.GOARCH}
	record := imagerelease.Record{APIVersion: imagerelease.CompatibilityVersion, MinimumOperatorVersion: "0.1.0", ContractPackageVersion: pin.Version, ContractPackageDigest: pin.Digest, RuntimeProfile: h.RuntimeProfile, Platform: image.ImagePlatform}
	approval, err := imagerelease.Validate(encode(record), image.ImageID, h)
	if err != nil {
		t.Fatal(err)
	}
	return imagerelease.Prepared{Image: image, Release: approval}, h
}
func TestWorkerOwnsStartupFailureAndCleanup(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("full worker integration uses the macOS spool profile; FIFO mechanics have separate transport coverage")
	}
	for _, mode := range []string{"event loss", "requested stop", "poll cancellation", "bad bootstrap", "create lost", "start lost", "kill unknown", "model failure", "model cleanup failure", "harness limit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			input := fixture(t)
			target, err := preparation.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			image, requirements := localCompatibility(t, input.Protocol)
			var modelConfig *campaignservice.ModelConfig
			var modelRequest map[string]any
			var modelCalls *provider
			if strings.HasPrefix(mode, "model ") {
				policy, request, _ := modelFixture(t)
				modelRequest = request
				failure := modelprovider.Diagnostic{Provider: "openai-chat", Model: "test-model", HTTPStatus: 401, ProviderCode: "invalid_api_key"}.WithFailure("http_rejection", "http_rejected")
				modelCalls = &provider{err: modelprovider.FailureError(failure)}
				modelConfig = &campaignservice.ModelConfig{Provider: modelCalls, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
			}
			var operations []string
			if modelConfig != nil {
				operations = []string{"engine.model_generate"}
			}
			if mode == "requested stop" || mode == "poll cancellation" || mode == "harness limit" {
				operations = []string{"engine.diagnostic_report", "engine.finish"}
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
			docker := &workerDocker{writer: writer, root: root, spool: dir, mode: mode, guestDone: make(chan error, 1), ready: make(chan struct{}), checking: make(chan struct{}), stop: make(chan struct{})}
			power := &workerPower{checkClose: func() {
				docker.mu.Lock()
				defer docker.mu.Unlock()
				if mode != "create lost" && docker.kills == 0 {
					t.Error("power lease released before termination")
				}
			}}
			worker, err := hostworker.New(ctx, hostworker.Config{Service: campaignservice.Config{Prepared: stored, Peer: &peer{input: input, revision: 5}, StateRoot: root, Deadline: deadline, Model: modelConfig}, Docker: docker, Image: image, Requirements: requirements, Tree: tree, Channel: channel, Inputs: launch, PolicyDirectory: parent, StartRequestID: strings.Repeat("a", 32), Lifetime: hostlifetime.NewWithBackend(power, writer.Fence().Stop)})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan struct{})
			var result hostworker.Result
			var runErr error
			go func() { result, runErr = worker.Run(ctx); close(finished) }()
			if mode == "event loss" || mode == "kill unknown" || mode == "requested stop" || mode == "poll cancellation" || mode == "harness limit" || modelConfig != nil {
				select {
				case <-docker.ready:
					if modelConfig != nil {
						modelRequest["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
						if e := spoolPut(dir, "ordinary-out", 0, stateWire("engine.model_generate", "model-1", 1, modelRequest)); e != nil {
							t.Fatal(e)
						}
					}
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
					if mode == "requested stop" || mode == "poll cancellation" || mode == "harness limit" {
						seq := int64(0)
						if mode == "harness limit" {
							body := map[string]any{"tool_name": "attempt_execute", "tool_call_id": "call-5", "diagnostic": map[string]any{"source": "harness-local", "stage": "arguments", "reason": "invalid_structure", "field_path": "/input", "message": "Required input is absent.", "execution_occurred": "no", "resubmission_safe": true}, "stopping": map[string]any{"condition": "invalid-call-limit", "observed_count": 5, "configured_limit": 5}}
							if e := spoolPut(dir, "ordinary-out", 0, stateWire("engine.diagnostic_report", "local-report", 1, body)); e != nil {
								t.Fatal(e)
							}
							raw, e := spoolRead(ctx, dir, "ordinary-in", 0)
							if e != nil {
								t.Fatal(e)
							}
							stateResult(t, raw, nil)
							if e = spoolACK(dir, 0, 2); e != nil {
								t.Fatal(e)
							}
							seq = 1
						}
						request := stateWire("engine.finish", "stop", 1, map[string]any{"finish_reason": "budget-limit", "unavailable": map[string]any{"reason": "budget-limit"}})
						var envelope map[string]any
						_ = json.Unmarshal(request, &envelope)
						envelope["seq"] = seq
						if mode == "harness limit" {
							envelope["body"] = map[string]any{"finish_reason": "harness-error", "unavailable": map[string]any{"reason": "invalid-call-limit"}}
						}
						request = encode(envelope)
						if e := spoolPut(dir, "ordinary-out", seq, request); e != nil {
							t.Fatal(e)
						}
						raw, e := spoolRead(ctx, dir, "ordinary-in", seq)
						if e != nil {
							t.Fatal(e)
						}
						ack := stateResult(t, raw, nil)
						if ack["status"] != "accepted" {
							t.Fatal(ack)
						}
						if e = spoolACK(dir, seq, 2); e != nil {
							t.Fatal(e)
						}
					}
					if modelConfig == nil {
						if mode == "poll cancellation" {
							select {
							case <-docker.checking:
							case <-ctx.Done():
								t.Fatal("poll not started")
							}
						}
						close(docker.stop)
					}
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
			if (runErr == nil) != (mode == "requested stop" || mode == "poll cancellation") || docker.creates != 1 {
				t.Fatal("missing terminal result", runErr, docker.creates)
			}
			if _, err = worker.Run(ctx); err == nil || docker.creates != 1 {
				t.Fatal("worker restarted")
			}
			if modelConfig != nil {
				if result.ModelDiagnostic == nil || result.ModelDiagnostic.HTTPStatus != 401 || result.ModelDiagnostic.OperationID != "model-1" || modelprovider.Details(runErr) == nil || modelCalls.calls != 1 {
					t.Fatal("model failure lost", result, runErr)
				}
				want := "removed"
				if mode == "model cleanup failure" {
					want = "unconfirmed"
				}
				if result.ContainerCleanup != want {
					t.Fatal("cleanup outcome", result)
				}
			}
			if result.StopAccepted != (mode == "requested stop" || mode == "poll cancellation" || mode == "harness limit") {
				t.Fatal("lost stop disposition", result)
			}
			if mode == "harness limit" {
				d := result.WorkflowDiagnostic
				if d == nil || !d.Valid() || !d.HostFinalized || d.Dispatched != 0 || !d.CleanupCompleted || d.AssessmentAccepted || d.Stopping != "invalid-call-limit" || !errors.Is(runErr, *d) {
					t.Fatal("lost Harness stopping cause", result, runErr)
				}
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
