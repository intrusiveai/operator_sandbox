//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/hostconfig"
	"github.com/intrusive-ai/operator-sandbox/internal/termination"
)

func savedCampaign(t *testing.T) (string, campaign.DockerBinding) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	d := contracts.RawDigest([]byte("fixture"))
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 3,
		CreatedAt: "2026-09-23T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1",
		ImageDigest: d, ReleaseRecordDigest: d, Contract: campaign.ContractPin{Version: "0.1.0", Digest: d, CatalogDigest: d, OperationsDigest: d},
		EngineContextDigest: d, InputTreeDigest: d, SkillSetDigest: d, ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d,
		Target:          campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: "session-1", WorkerInstanceID: "worker-1", NativeFeedbackProfile: "diagnostic", CapabilitySourceDigest: d, CapabilityProjectionDigest: d},
		RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`),
		HarnessLimits:   json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`),
		Retention:       campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 16 << 20, MaxSegmentBytes: campaign.MaxEventBytes}}
	w, err := campaign.Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(),
		Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: d, Labels: m.DockerLabels()}
	if err := w.SaveDockerBinding(b); err != nil {
		t.Fatal(err)
	}
	return root, b
}

func fakeCLI(t *testing.T, b campaign.DockerBinding) string {
	t.Helper()
	dir := t.TempDir()
	inspect := filepath.Join(dir, "inspect.json")
	stopped := filepath.Join(dir, "stopped.json")
	item := map[string]any{"id": b.DockerContainerID, "image": b.ImageDigest, "labels": b.Labels, "status": "running", "running": true, "paused": false, "restarting": false, "restart_policy": "no", "auto_remove": false}
	raw, _ := json.Marshal(item)
	if err := os.WriteFile(inspect, raw, 0600); err != nil {
		t.Fatal(err)
	}
	item["status"], item["running"] = "exited", false
	raw, _ = json.Marshal(item)
	if err := os.WriteFile(stopped, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPERATOR_TEST_INSPECT", inspect)
	t.Setenv("OPERATOR_TEST_STOPPED", stopped)
	t.Setenv("OPERATOR_TEST_ID", b.DockerContainerID)
	t.Setenv("DOCKER_CONTEXT", "wrong-context")
	t.Setenv("DOCKER_HOST", "tcp://wrong-daemon:2375")
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/sh
if [ "$1" != "--host" ] || [ "$2" != "unix:///saved/docker.sock" ] || [ -n "$DOCKER_CONTEXT$DOCKER_HOST" ]; then exit 10; fi
if [ "$3" = info ]; then printf '"daemon-1"\n'; exit 0; fi
if [ "$3" != container ] || [ "$7" != "$OPERATOR_TEST_ID" ]; then exit 11; fi
case "$4" in
inspect) exec /bin/cat "$OPERATOR_TEST_INSPECT" ;;
kill) exec /bin/cp "$OPERATOR_TEST_STOPPED" "$OPERATOR_TEST_INSPECT" ;;
*) exit 12 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestAdministrativeCommandWithActiveWriterAndDamagedJournal(t *testing.T) {
	root, b := savedCampaign(t)
	bin := fakeCLI(t, b)
	// Keep the writer and its process lock alive. Damage the journal to ensure
	// independent identity lookup and emergency recording do not inspect its head.
	if err := os.WriteFile(filepath.Join(root, "campaigns", b.CampaignID, "journal-head.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"campaign", "terminate", "--campaign", b.CampaignID, "--state-root", root, "--docker-bin", bin, "--mode", "immediate", "--reason", "user-request"}
	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatal(code, stdout.String(), stderr.String())
		}
		var r termination.Receipt
		if err := json.Unmarshal(stdout.Bytes(), &r); err != nil || !r.Successful() || r.Outcome.KillAttempted != (i == 0) {
			t.Fatal(r, err)
		}
	}
	intent, err := campaign.ReadStopIntent(root, b.CampaignID)
	if err != nil || intent == nil {
		t.Fatal(intent, err)
	}
	result, err := campaign.ReadTerminationResult(root, b.CampaignID)
	if err != nil || result == nil || !result.Outcome.Confirmed {
		t.Fatal(result, err)
	}
}

func TestCommandReportsDockerAndRecordingSeparately(t *testing.T) {
	root, b := savedCampaign(t)
	bin := fakeCLI(t, b)
	// An unresolved prior emergency write blocks new recording, not Docker kill.
	if err := os.WriteFile(filepath.Join(root, "campaigns", b.CampaignID, "termination-intent.json.pending"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"campaign", "terminate", "--campaign", b.CampaignID, "--state-root", root, "--docker-bin", bin}, &stdout, &stderr)
	var r termination.Receipt
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil || code != 1 || !r.Outcome.Confirmed || r.IntentRecording != "failed" {
		t.Fatal(code, r, err, stderr.String())
	}
}

func TestCommandRejectsUnsupportedArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"campaign", "start"}, {"campaign", "terminate"}, {"campaign", "terminate", "--campaign", "x", "--mode", "graceful"}, {"campaign", "terminate", "--campaign", "x", "--state-root", "relative"}, {"campaign", "terminate", "--campaign", "x", "--request-id", "bad"}} {
		var out, err bytes.Buffer
		if code := run(context.Background(), args, &out, &err); code != 2 {
			t.Fatal(args, code)
		}
	}
}

func configPaths(t *testing.T) hostconfig.Paths {
	t.Helper()
	dir := t.TempDir()
	return hostconfig.Paths{ConfigFile: filepath.Join(dir, "config.yaml"), StateRoot: filepath.Join(dir, "data"), DockerEndpoint: "unix:///default/docker.sock"}
}

func writeConfig(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigCheckIsReadOnly(t *testing.T) {
	p := configPaths(t)
	// The executable deliberately does not exist; checking config must not run it,
	// inspect Docker, validate releases or create the configured state/cache roots.
	raw := "engine: {image: 'example/attack_harness:dev'}\ndocker: {executable: '/does/not/exist/docker'}\n"
	writeConfig(t, p.ConfigFile, raw)
	for _, args := range [][]string{{"config", "check"}, {"config", "check", "--config", p.ConfigFile}} {
		var out, stderr bytes.Buffer
		code := runWithDefaults(context.Background(), args, &out, &stderr, p)
		var result struct {
			APIVersion string `json:"api_version"`
			Status     string `json:"status"`
			hostconfig.Loaded
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || code != 0 || result.Status != "valid" || result.APIVersion != "operator.dev/config-check/v1alpha1" || result.Digest != contracts.RawDigest([]byte(raw)) || result.Config.State.Root != p.StateRoot || result.Config.Spool.MaxBytes != 536870912 || result.Config.Evidence.MaxArchiveBytes != 4294967296 {
			t.Fatal(code, out.String(), stderr.String(), err)
		}
		if _, err := os.Stat(p.StateRoot); !os.IsNotExist(err) {
			t.Fatal("check created state", err)
		}
	}
}

func TestTerminationConfigurationAndOverridePrecedence(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "config", true: "override"}[override], func(t *testing.T) {
			root, b := savedCampaign(t)
			bin := fakeCLI(t, b)
			p := configPaths(t)
			configRoot, configBin := root, bin
			args := []string{"campaign", "terminate", "--campaign", b.CampaignID, "--config", p.ConfigFile}
			if override {
				configRoot, configBin = "/wrong/state", "/wrong/docker"
				args = append(args, "--state-root", root, "--docker-bin", bin)
			}
			// Endpoint differs from the saved campaign. The fake CLI rejects any
			// command that doesn't use the recorded endpoint and exact container ID.
			writeConfig(t, p.ConfigFile, "engine: {image: 'example/attack_harness:dev'}\nstate: {root: '"+configRoot+"'}\ndocker: {endpoint: 'unix:///changed/docker.sock', executable: '"+configBin+"'}\n")
			var out, stderr bytes.Buffer
			if code := runWithDefaults(context.Background(), args, &out, &stderr, p); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
}

func TestTerminationBypassesBrokenDefaultWithExplicitStateRoot(t *testing.T) {
	root, b := savedCampaign(t)
	bin := fakeCLI(t, b)
	p := configPaths(t)
	writeConfig(t, p.ConfigFile, "not a valid configuration")
	args := []string{"campaign", "terminate", "--campaign", b.CampaignID, "--state-root", root, "--docker-bin", bin}
	var out, stderr bytes.Buffer
	if code := runWithDefaults(context.Background(), args, &out, &stderr, p); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	// Explicit --config must never be silently ignored, even during recovery.
	out.Reset()
	stderr.Reset()
	if code := runWithDefaults(context.Background(), append(args, "--config", p.ConfigFile), &out, &stderr, p); code != 2 || out.Len() != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
}

func TestExplicitRecoveryWithoutHostDefaults(t *testing.T) {
	root, b := savedCampaign(t)
	bin := fakeCLI(t, b)
	t.Setenv("HOME", "")
	var out, stderr bytes.Buffer
	args := []string{"campaign", "terminate", "--campaign", b.CampaignID, "--state-root", root, "--docker-bin", bin}
	if code := run(context.Background(), args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
}

func TestDefaultConfigIsUsedAndOnlyAbsencePermitsFallback(t *testing.T) {
	for _, state := range []string{"valid", "absent", "invalid"} {
		t.Run(state, func(t *testing.T) {
			root, b := savedCampaign(t)
			bin := fakeCLI(t, b)
			p := configPaths(t)
			p.StateRoot = root
			args := []string{"campaign", "terminate", "--campaign", b.CampaignID}
			switch state {
			case "valid":
				p.StateRoot = "/wrong/state"
				writeConfig(t, p.ConfigFile, "engine: {image: test}\nstate: {root: '"+root+"'}\ndocker: {executable: '"+bin+"'}\n")
			case "invalid":
				writeConfig(t, p.ConfigFile, "engine: {unknown: test}")
				args = append(args, "--docker-bin", bin)
			case "absent":
				args = append(args, "--docker-bin", bin)
			}
			var out, stderr bytes.Buffer
			code := runWithDefaults(context.Background(), args, &out, &stderr, p)
			if state == "invalid" {
				if code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "--state-root") {
					t.Fatal(code, out.String(), stderr.String())
				}
			} else if code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
}

func TestConfigCommandRejectsMissingFileAndInvalidArguments(t *testing.T) {
	p := configPaths(t)
	for _, args := range [][]string{
		{"config", "check"}, {"config", "check", "--config", ""}, {"config", "check", "extra"}, {"config", "check", "--config", "relative.yaml"},
		{"campaign", "terminate", "--campaign", "x", "--config", p.ConfigFile},
		{"campaign", "terminate", "--campaign", "x", "--state-root", ""},
		{"campaign", "terminate", "--campaign", "x", "--config", ""},
	} {
		var out, stderr bytes.Buffer
		if code := runWithDefaults(context.Background(), args, &out, &stderr, p); code != 2 || out.Len() != 0 {
			t.Fatal(args, code, out.String(), stderr.String())
		}
	}
}
