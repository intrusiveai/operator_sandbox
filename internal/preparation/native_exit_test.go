//go:build darwin

package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostlifetime"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

// Uses real Docker identity, events, exit inspection, bounded logs and removal.
// The script is a controlled peer, not the production Harness/confinement image.
type nativeExitDocker struct {
	*dockercontrol.Client
	t                   *testing.T
	image               dockercontrol.ImagePin
	writer              *campaign.Writer
	spool, script, mode string
	binding             campaign.DockerBinding
}

func (d *nativeExitDocker) command(ctx context.Context, args ...string) []byte {
	d.t.Helper()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", d.image.Endpoint}, args...)...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		d.t.Fatalf("Docker test command failed: %v: %s", err, raw)
	}
	return raw
}
func (d *nativeExitDocker) Create(ctx context.Context, _ *dockercontrol.LaunchPlan) (campaign.DockerBinding, error) {
	m := d.writer.Manifest()
	args := []string{"container", "create", "--pull=never", "--platform", d.image.ImagePlatform, "--network=none", "--restart=no", "--log-driver=local", "--log-opt=max-size=1m", "--log-opt=max-file=1", "--log-opt=compress=false", "--mount", "type=bind,src=" + d.spool + ",dst=/spool", "--mount", "type=bind,src=" + d.script + ",dst=/peer.py,readonly", "--entrypoint=/usr/bin/python3"}
	for k, v := range m.DockerLabels() {
		args = append(args, "--label", k+"="+v)
	}
	args = append(args, d.image.ReferenceForDocker(), "/peer.py", d.mode)
	id := strings.TrimSpace(string(d.command(ctx, args...)))
	d.binding = campaign.DockerBinding{APIVersion: campaign.SelectedImageBindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: d.writer.ManifestDigest(), Endpoint: d.image.Endpoint, DaemonID: d.image.DaemonID, DockerContainerID: id, ImageDigest: d.image.ImageID, Labels: m.DockerLabels()}
	d.t.Cleanup(func() { cmd := exec.Command("docker", "--host", d.image.Endpoint, "rm", "-f", id); _ = cmd.Run() })
	return d.binding, nil
}
func (d *nativeExitDocker) StartCreated(ctx context.Context, _ string, b campaign.DockerBinding, _ *dockercontrol.LaunchPlan) error {
	d.command(ctx, "start", b.DockerContainerID)
	return nil
}

func (d *nativeExitDocker) WatchEvents(ctx context.Context, b campaign.DockerBinding, since time.Time) error {
	if d.mode != "peer-first" {
		return d.Client.WatchEvents(ctx, b, since)
	}
	for {
		if _, err := os.Stat(filepath.Join(d.spool, "peer-closed")); err == nil {
			return transport.ErrPeerLost
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (d *nativeExitDocker) ObserveExit(ctx context.Context, b campaign.DockerBinding) campaign.HarnessExit {
	h := d.Client.ObserveExit(ctx, b)
	if d.mode == "missing-status" {
		h.ExitObserved = false
		h.ExitCode = 0
		h.ExitUnavailable = "injected_inspection_failure"
	}
	return h
}
func (d *nativeExitDocker) RemoveStopped(ctx context.Context, b campaign.DockerBinding) error {
	if d.mode == "cleanup-failure" {
		return errors.New("injected removal failure")
	}
	return d.Client.RemoveStopped(ctx, b)
}

func TestNativeDockerFinishExit(t *testing.T) {
	imageName := os.Getenv("OPERATOR_EXIT_TEST_IMAGE")
	if imageName == "" {
		t.Skip("set OPERATOR_EXIT_TEST_IMAGE to a cached Python image; no pulls")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := dockercontrol.New("")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	endpoint := "unix://" + filepath.Join(home, ".docker/run/docker.sock")
	image, err := client.ResolveImage(ctx, endpoint, imageName, "darwin/"+goruntime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := filepath.Abs("testdata/exit_peer.py")
	binary := filepath.Join(t.TempDir(), "operatorctl")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/operatorctl")
	if raw, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	for _, mode := range []string{"success", "peer-first", "corrected", "nonzero", "silent-nonzero", "no-finish", "missing-status", "timeout", "cleanup-failure"} {
		t.Run(mode, func(t *testing.T) {
			input := fixture(t)
			target, e := preparation.Build(input)
			if e != nil {
				t.Fatal(e)
			}
			approved, req := localCompatibility(t, input.Protocol)
			record := imagerelease.Record{APIVersion: imagerelease.CompatibilityVersion, MinimumOperatorVersion: "0.1.0", ContractPackageVersion: req.Contract.Version, ContractPackageDigest: req.Contract.Digest, RuntimeProfile: req.RuntimeProfile, Platform: image.ImagePlatform}
			approved.Image = image
			approved.Release, e = imagerelease.Validate(encode(record), image.ImageID, req)
			if e != nil {
				t.Fatal(e)
			}
			writer, launch, root := preparedLaunchConfigured(t, target, func(m map[string]any) {
				m["release"] = map[string]any{"image_digest": image.ImageID, "release_record_digest": approved.Release.Digest()}
			}, req.HostPlatform, "engine.finish")
			stored, e := target.Persist(writer, launch.EngineContext)
			if e != nil {
				t.Fatal(e)
			}
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			deadline := time.Now().Add(time.Minute)
			channel, e := transport.NewSpool(dir, transport.Config{Protocol: input.Protocol, CampaignID: "campaign-1", LaunchID: "launch-1", Fence: writer.Fence(), CampaignDeadline: deadline})
			if e != nil {
				t.Fatal(e)
			}
			parent := t.TempDir()
			os.Chmod(parent, 0700)
			tree, e := staging.Create(ctx, input.Protocol, parent, staging.Manifests{InputTree: launch.InputTree, SkillSet: launch.SkillSet, Skills: launch.Skills}, map[string][]byte{"input/run-context.json": launch.EngineContext, "input/scenario-bundle.json": launch.ScenarioBundle, "input/system-prompt.txt": launch.Prompt})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = tree.Discard(); _ = channel.Close(); _ = writer.Close() })
			d := &nativeExitDocker{Client: client, t: t, image: image, writer: writer, spool: dir, script: script, mode: mode}
			worker, e := hostworker.New(ctx, hostworker.Config{Service: campaignservice.Config{Prepared: stored, Peer: &peer{input: input, revision: 5}, StateRoot: root, Deadline: deadline}, Docker: d, Image: approved, Requirements: req, Tree: tree, Channel: channel, Inputs: launch, PolicyDirectory: parent, StartRequestID: strings.Repeat("a", 32), Lifetime: hostlifetime.NewWithBackend(&workerPower{}, writer.Fence().Stop)})
			if e != nil {
				t.Fatal(e)
			}
			result, runErr := worker.Run(ctx)
			h := result.HarnessExit
			success := mode == "success" || mode == "corrected" || mode == "peer-first"
			if (runErr == nil) != success || h == nil || h.ExitObserved != (mode != "missing-status") || (result.ContainerCleanup == "removed") != (mode != "cleanup-failure") || h.CleanupComplete != (mode != "cleanup-failure") {
				t.Fatalf("mode %s: %+v; %v", mode, result, runErr)
			}
			if success && (h.ExitCode != 0 || h.Outcome != "succeeded" || !h.AssessmentCommitted) {
				t.Fatal(h)
			}
			if mode == "corrected" && (result.ConclusionDiagnostic == nil || result.ConclusionDiagnostic.Reason != "invalid_attempt") {
				t.Fatal("expected recovered receipt rejection", result.ConclusionDiagnostic)
			}
			if mode == "nonzero" && (h.ExitCode != 1 || h.StderrState != "retained" || !strings.Contains(h.Cause, "controlled post-finish failure") || strings.Contains(h.Stderr, "synthetic-secret-never-retain")) {
				t.Fatal(h)
			}
			if mode == "peer-first" && (h.Forced || h.WorkerCause != "transport_peer_lost") {
				t.Fatal("peer close was not safely drained", h)
			}
			if mode == "cleanup-failure" && h.Outcome != "succeeded" {
				t.Fatal("cleanup changed Harness success", h)
			}
			if mode == "timeout" && !h.Forced {
				t.Fatal("deadline did not force termination")
			}
			if e = writer.Close(); e != nil {
				t.Fatal(e)
			}
			inspect := exec.CommandContext(ctx, binary, "inspect", "--state-root", root, "--campaign", "campaign-1")
			raw, e := inspect.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(raw))
			}
			var got struct {
				HarnessExit *campaign.HarnessExit `json:"harness_exit"`
			}
			if json.Unmarshal(raw, &got) != nil || got.HarnessExit == nil || *got.HarnessExit != *h {
				t.Fatal("inspection lost exit after removal", string(raw))
			}
			report, e := reporting.Generate(ctx, root, "campaign-1", "")
			if e != nil {
				t.Fatal(e)
			}
			if r := reportResult(t, root, report); r.HarnessExit == nil || *r.HarnessExit != *h {
				t.Fatal("report lost exit", r.HarnessExit)
			}
			if _, e = os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("spool not removed", e)
			}
			t.Log(h.Summary())
		})
	}
}
