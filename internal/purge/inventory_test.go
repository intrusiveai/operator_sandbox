//go:build linux || darwin

package purge

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
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

func privateRoot(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func fixture(t *testing.T, root, id string, bind bool) campaign.DockerBinding {
	t.Helper()
	d := contracts.RawDigest([]byte("fixture"))
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: id, LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 3,
		CreatedAt: "2026-09-23T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1",
		ImageDigest: d, ReleaseRecordDigest: d, Contract: campaign.ContractPin{Version: "0.1.0", Digest: d, CatalogDigest: d, OperationsDigest: d}, EngineContextDigest: d, InputTreeDigest: d, SkillSetDigest: d, ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d, Target: campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: "session-1", WorkerInstanceID: "worker-1", NativeFeedbackProfile: "diagnostic", CapabilitySourceDigest: d, CapabilityProjectionDigest: d},
		RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`),
		HarnessLimits:   json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`), Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 16 << 20, MaxSegmentBytes: campaign.MaxEventBytes}}
	w, e := campaign.Create(root, m)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	return binding(t, w, m, bind)
}
func binding(t *testing.T, w *campaign.Writer, m campaign.RunManifest, save bool) campaign.DockerBinding {
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///saved/docker.sock", DaemonID: "saved-daemon", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	if save {
		if e := w.SaveDockerBinding(b); e != nil {
			t.Fatal(e)
		}
	}
	return b
}

type fakeDocker struct {
	state   string
	seen    []campaign.DockerBinding
	removed []campaign.DockerBinding
}

func (d *fakeDocker) CheckInactive(_ context.Context, b campaign.DockerBinding) dockercontrol.Inactivity {
	d.seen = append(d.seen, b)
	return dockercontrol.Inactivity{Confirmed: d.state == "absent" || d.state == "exited", State: d.state}
}
func (d *fakeDocker) RemoveStopped(_ context.Context, b campaign.DockerBinding) error {
	d.removed = append(d.removed, b)
	d.state = "absent"
	return nil
}
func savedStart(t *testing.T, root, id string) startrequest.Snapshot {
	t.Helper()
	r := startrequest.Request{APIVersion: startrequest.Version, ConfigurationFile: "/installed/config.yaml", StateRoot: root, DockerEndpoint: "unix:///saved/docker.sock", InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: hostrun.NewSelection(privateRoot(t))}
	r.Selection.CampaignID = id
	s, e := startrequest.Save(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestInventoryIncludesEarlyStartsAttachmentsAndManagedCopies(t *testing.T) {
	ctx := context.Background()
	root := privateRoot(t)
	fixture(t, root, "full", false)
	s := savedStart(t, root, "early")
	a, e := campaign.CreateAttachment(root, campaign.AttachmentIntent{CampaignID: "early", LaunchID: "launch-1", StartRequestID: s.Request.Selection.StartRequestID, WorkerInstanceID: "worker-1", InputsFingerprint: s.Request.InputsFingerprint})
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	output := privateRoot(t)
	copy, e := BeginManagedCopy(ctx, root, "full", output)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(copy.Directory, "report.txt"), []byte("managed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Prepare(ctx, root, Selection{All: true}, nil); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("live copy not excluded", e)
	}
	copy.Close()
	p, e := Prepare(ctx, root, Selection{All: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if len(p.Inventories) != 2 || len(p.Inventories[0].Groups) != 2 || len(p.Inventories[1].Groups) != 3 {
		t.Fatal(p.Inventories)
	}
	if _, e = startrequest.ClaimOnce(ctx, root, s.Request.Selection.StartRequestID, s.Digest); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("late writer raced inventory", e)
	}
	if _, e = campaign.OpenNativeRecovery(root, "full"); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("report/read raced inventory", e)
	}
}
func TestInventoryRequiresExactInactiveDockerAndRejectsWholeSelection(t *testing.T) {
	for _, state := range []string{"absent", "exited", "active", "unknown"} {
		t.Run(state, func(t *testing.T) {
			root := privateRoot(t)
			want := fixture(t, root, "campaign", true)
			d := &fakeDocker{state: state}
			p, e := Prepare(context.Background(), root, Selection{All: true}, d)
			if state == "active" || state == "unknown" {
				if !errors.Is(e, ErrUnconfirmed) {
					t.Fatal(e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			if len(d.seen) != 1 || d.seen[0].DockerContainerID != want.DockerContainerID || d.seen[0].Endpoint != want.Endpoint || d.seen[0].DaemonID != want.DaemonID || len(d.removed) != 0 {
				t.Fatal(d)
			}
		})
	}
}
func TestInventoryExcludesReportingAndDoesNotFollowGuestLinks(t *testing.T) {
	root := privateRoot(t)
	fixture(t, root, "campaign", false)
	outside := privateRoot(t)
	if e := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "campaigns", "campaign", "reports", "link")); e != nil {
		t.Fatal(e)
	}
	reader, e := campaign.OpenNativeRecovery(root, "campaign")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Prepare(context.Background(), root, Selection{All: true}, nil); !errors.Is(e, campaign.ErrActive) {
		t.Fatal(e)
	}
	reader.Close()
	p, e := Prepare(context.Background(), root, Selection{CampaignID: "campaign"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	p.Close()
	if _, e = os.Stat(filepath.Join(outside, "keep")); e != nil {
		t.Fatal(e)
	}
}
func TestInventoryRejectsManagedCopyReplacement(t *testing.T) {
	root := privateRoot(t)
	fixture(t, root, "campaign", false)
	copy, e := BeginManagedCopy(context.Background(), root, "campaign", privateRoot(t))
	if e != nil {
		t.Fatal(e)
	}
	copy.Close()
	if e = os.WriteFile(filepath.Join(copy.Directory, "managed.json"), []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Prepare(context.Background(), root, Selection{All: true}, nil); e == nil {
		t.Fatal("unverified copy accepted")
	}
}
