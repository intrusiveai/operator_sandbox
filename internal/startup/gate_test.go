//go:build linux || darwin

package startup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

func savedManifest(t *testing.T) campaign.RunManifest {
	t.Helper()
	d := contracts.RawDigest([]byte("fixture"))
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 3,
		CreatedAt: "2026-09-23T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1",
		ImageDigest: d, ReleaseRecordDigest: d, Contract: campaign.ContractPin{Version: "0.1.0", Digest: d, CatalogDigest: d, OperationsDigest: d},
		EngineContextDigest: d, InputTreeDigest: d, SkillSetDigest: d, ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d,
		Target:          campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: "session-1", WorkerInstanceID: "worker-1", NativeFeedbackProfile: "diagnostic", CapabilitySourceDigest: d, CapabilityProjectionDigest: d},
		RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`),
		HarnessLimits:   json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`),
		Retention:       campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 16 << 20, MaxSegmentBytes: campaign.MaxEventBytes}}
	return m
}

type recoveryDocker struct {
	state                              string
	kills, removes                     int
	mismatch, failedKill, failedRemove bool
}

func (d *recoveryDocker) CheckInactive(_ context.Context, b campaign.DockerBinding) dockercontrol.Inactivity {
	if b.DockerContainerID != strings.Repeat("b", 64) || b.DaemonID != "saved-daemon" || b.Endpoint != "unix:///saved/socket" {
		panic("binding drift")
	}
	if d.mismatch {
		return dockercontrol.Inactivity{State: "unknown", Code: "identity_mismatch"}
	}
	return dockercontrol.Inactivity{Confirmed: d.state != "active", State: d.state, Code: "fixture"}
}
func (d *recoveryDocker) Terminate(context.Context, campaign.DockerBinding) dockercontrol.Outcome {
	d.kills++
	if d.failedKill {
		return dockercontrol.Outcome{State: "unknown", Code: "fixture"}
	}
	d.state = "exited"
	return dockercontrol.Outcome{Confirmed: true, KillAttempted: true, State: "exited", Code: "confirmed_stopped"}
}
func (d *recoveryDocker) RemoveStopped(context.Context, campaign.DockerBinding) error {
	d.removes++
	if d.failedRemove {
		return errors.New("failed")
	}
	d.state = "absent"
	return nil
}
func gateFixture(t *testing.T, withBinding, started bool) (string, *campaign.Writer) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	m := savedManifest(t)
	w, err := campaign.Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if started {
		if _, err := w.Append(campaign.Entry{RunRevision: 3, Kind: "launch.start-intent", Metadata: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if withBinding {
		b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///saved/socket", DaemonID: "saved-daemon", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
		if err := w.SaveDockerBinding(b); err != nil {
			t.Fatal(err)
		}
	}
	return root, w
}
func TestGateConfirmsOldContainersAndHoldsLease(t *testing.T) {
	for _, state := range []string{"active", "exited", "absent"} {
		t.Run(state, func(t *testing.T) {
			root, w := gateFixture(t, true, true)
			w.Close()
			d := &recoveryDocker{state: state}
			gate, rows, err := Acquire(context.Background(), root, d)
			if err != nil || len(rows) != 1 || rows[0].State != "container-absent" || !rows[0].JournalIntact {
				t.Fatalf("%+v %v", rows, err)
			}
			defer gate.Close()
			if _, err := campaign.AcquireHostLease(root); !errors.Is(err, campaign.ErrActive) {
				t.Fatal("lease lost", err)
			}
			if d.kills != map[bool]int{true: 1, false: 0}[state == "active"] || d.removes != map[bool]int{true: 0, false: 1}[state == "absent"] {
				t.Fatal(d)
			}
			if state == "active" {
				if rows[0].Termination == nil || !rows[0].Termination.Successful() {
					t.Fatal(rows)
				}
			}
			gate.Close()
			second, _, err := Acquire(context.Background(), root, d)
			if err != nil {
				t.Fatal(err)
			}
			second.Close()
		})
	}
}
func TestGateDoesNotResumeOrRepairDamagedJournal(t *testing.T) {
	root, w := gateFixture(t, true, true)
	w.Close()
	path := filepath.Join(root, "campaigns/campaign-1/journal-head.json")
	if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &recoveryDocker{state: "active"}
	gate, rows, err := Acquire(context.Background(), root, d)
	if err != nil || rows[0].JournalIntact || d.kills != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	gate.Close()
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "damaged" {
		t.Fatal("journal changed", err)
	}
}
func TestGateRejectsActiveWritersAndUncertainExecution(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		binding, started, active, corrupt bool
		docker                            recoveryDocker
	}{
		{name: "active writer", binding: true, started: true, active: true, docker: recoveryDocker{state: "active"}},
		{name: "lost create identity", started: true, docker: recoveryDocker{state: "absent"}},
		{name: "damaged without identity", corrupt: true, docker: recoveryDocker{state: "absent"}},
		{name: "daemon mismatch", binding: true, started: true, docker: recoveryDocker{state: "active", mismatch: true}},
		{name: "kill uncertainty", binding: true, started: true, docker: recoveryDocker{state: "active", failedKill: true}},
		{name: "remove uncertainty", binding: true, started: true, docker: recoveryDocker{state: "exited", failedRemove: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, w := gateFixture(t, tc.binding, tc.started)
			if !tc.active {
				w.Close()
			}
			if tc.corrupt {
				if err := os.WriteFile(filepath.Join(root, "campaigns/campaign-1/journal-head.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			gate, _, err := Acquire(context.Background(), root, &tc.docker)
			if err == nil || gate != nil {
				t.Fatal("unresolved start admitted")
			}
			if tc.active && tc.docker.kills != 0 {
				t.Fatal("killed active writer")
			}
			lease, err := campaign.AcquireHostLease(root)
			if err != nil {
				t.Fatal("failed gate leaked lease", err)
			}
			lease.Close()
		})
	}
}
func TestGateNeverCreatedAndInvalidInventory(t *testing.T) {
	root, w := gateFixture(t, false, false)
	w.Close()
	d := &recoveryDocker{state: "absent"}
	gate, rows, err := Acquire(context.Background(), root, d)
	if err != nil || rows[0].State != "never-created" {
		t.Fatal(rows, err)
	}
	gate.Close()
	if err := os.Symlink("campaign-1", filepath.Join(root, "campaigns/other")); err != nil {
		t.Fatal(err)
	}
	if gate, _, err := Acquire(context.Background(), root, d); err == nil || gate != nil {
		t.Fatal("symlink inventory accepted")
	}
	empty := t.TempDir()
	os.Chmod(empty, 0700)
	gate, rows, err = Acquire(context.Background(), empty, d)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	gate.Close()
}

func TestGateClaimIsBoundToRootAndSingleWorker(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	gate, _, err := Acquire(context.Background(), root, &recoveryDocker{state: "absent"})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if gate.Claim(root+"/other") || !gate.Claim(root) || gate.Claim(root) {
		t.Fatal("invalid ownership transfer")
	}
	gate.Close()
	if gate.Claim(root) {
		t.Fatal("closed gate claimed")
	}
}
