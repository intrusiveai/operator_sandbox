package preparation_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeevidence"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
)

type latePeer struct {
	native       *peer
	export       *exportPeer
	statusCalls  int
	beforeExport func()
	archives     map[string][]byte
}

func (p *latePeer) Status(ctx context.Context, id string) (interceptor.Status, error) {
	p.statusCalls++
	return p.native.Status(ctx, id)
}
func (p *latePeer) DownloadEvidence(ctx context.Context, q interceptor.EvidenceRequest, dir string) (*interceptor.EvidenceDownload, error) {
	if p.beforeExport != nil {
		p.beforeExport()
	}
	if p.archives != nil {
		p.export.data = p.archives[q.SessionID]
	}
	return p.export.DownloadEvidence(ctx, q, dir)
}

type lateDocker struct {
	active bool
	calls  int
}

func (d *lateDocker) CheckInactive(_ context.Context, b campaign.DockerBinding) dockercontrol.Inactivity {
	d.calls++
	if b.Endpoint != "unix:///fixture/docker.sock" || b.DaemonID != "daemon-1" || b.DockerContainerID != strings.Repeat("b", 64) {
		panic("lost saved Docker identity")
	}
	return dockercontrol.Inactivity{Confirmed: !d.active, State: "absent"}
}
func lateFixture(t *testing.T) (string, *campaign.Writer, *latePeer, campaign.EvidencePolicy, nativeevidence.Target) {
	t.Helper()
	in := fixture(t)
	nativeEvidenceInput(&in)
	target, e := preparation.Build(in)
	if e != nil {
		t.Fatal(e)
	}
	w, launch, root := preparedLaunchConfigured(t, target, nil, "")
	if _, e = target.Persist(w, launch.EngineContext); e != nil {
		t.Fatal(e)
	}
	policy := campaign.EvidencePolicy{MaxArchiveBytes: 1 << 20, TimeoutNS: int64(time.Second), TotalTimeoutNS: int64(2 * time.Second)}
	if _, e = w.Append(campaign.Entry{RunRevision: 1, Kind: "evidence.policy", Metadata: encode(policy)}); e != nil {
		t.Fatal(e)
	}
	identity, maximum := target.NativeEvidence()
	selection := nativeevidence.Target{Identity: identity, NativeMaxBytes: maximum, Reservation: "evidence:" + contracts.RawDigest([]byte(identity.SessionID))[7:]}
	selectEvidence(t, w, selection)
	m := w.Manifest()
	binding := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: w.ManifestDigest(), Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	if e = w.SaveDockerBinding(binding); e != nil {
		t.Fatal(e)
	}
	p := &latePeer{native: &peer{input: in, revision: 5}, export: &exportPeer{data: read(t, "../interceptor/testdata/native-evidence.tar")}}
	p.beforeExport = func() {
		if _, e := os.Stat(filepath.Join(root, "campaigns/campaign-1/evidence-recovery", identity.SessionID, "intent.json")); e != nil {
			t.Fatal("download before durable claim", e)
		}
	}
	return root, w, p, policy, selection
}
func selectEvidence(t *testing.T, w *campaign.Writer, selection nativeevidence.Target) {
	t.Helper()
	if _, e := w.AppendReserving(campaign.Entry{RunRevision: w.Revision(), Kind: "evidence.session-selected", Metadata: encode(map[string]string{"session_id": selection.Identity.SessionID}), Content: []campaign.Content{{Role: "evidence-selection", MediaType: "application/json", Bytes: encode(selection)}}}, selection.Reservation, 2<<20); e != nil {
		t.Fatal(e)
	}
}
func TestLateEvidenceCollectsOnceWithoutChangingExecutionJournal(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			root, w, p, policy, _ := lateFixture(t)
			w.Close()
			if partial {
				p.export.data = read(t, "../interceptor/testdata/native-evidence-partial.tar")
			}
			before, e := campaign.Inspect(root, "campaign-1", nil)
			if e != nil {
				t.Fatal(e)
			}
			docker := &lateDocker{}
			report, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", docker, p)
			want := "complete"
			if partial {
				want = "partial"
			}
			if e != nil || len(report.Outcomes) != 1 || report.Outcomes[0].State != want || !report.Outcomes[0].Recorded || len(p.export.calls) != 1 || report.Complete() == partial {
				t.Fatal(report, e)
			}
			if q := p.export.calls[0]; q.MaxArchiveBytes != policy.MaxArchiveBytes || q.InterceptorMaxBytes != 4<<30 {
				t.Fatal(q)
			}
			after, e := campaign.Inspect(root, "campaign-1", nil)
			if e != nil || after.VerifiedBytes != before.VerifiedBytes || after.VerifiedEvents != before.VerifiedEvents {
				t.Fatal(after, e)
			}
			again, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", docker, p)
			if e != nil || len(p.export.calls) != 1 || p.statusCalls != 1 || string(encode(again)) != string(encode(report)) {
				t.Fatal(again, e)
			}
			if len(p.native.calls) != 0 {
				t.Fatal("mutated native target", p.native.calls)
			}
			archive := filepath.Join(root, "campaigns/campaign-1", report.Outcomes[0].ArchivePath)
			os.WriteFile(archive, []byte("damaged"), 0600)
			damaged, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", docker, p)
			if e != nil || damaged.Outcomes[0].State != "invalid" || damaged.Complete() || len(p.export.calls) != 1 {
				t.Fatal(damaged, e)
			}
		})
	}
}
func TestLateEvidenceRefusesLiveOrUnverifiedExecution(t *testing.T) {
	for _, fault := range []string{"writer", "lease", "Docker active", "journal corrupt", "binding missing", "policy missing"} {
		t.Run(fault, func(t *testing.T) {
			root, w, p, _, _ := lateFixture(t)
			switch fault {
			case "binding missing":
				if _, e := w.Append(campaign.Entry{RunRevision: 1, Kind: "launch.start-intent", Metadata: json.RawMessage(`{}`)}); e != nil {
					t.Fatal(e)
				}
			case "policy missing": // A conflicting second policy is equally untrusted.
				w.Append(campaign.Entry{RunRevision: 1, Kind: "evidence.policy", Metadata: json.RawMessage(`{}`)})
			}
			if fault != "writer" {
				w.Close()
			} else {
				defer w.Close()
			}
			if fault == "lease" {
				l, e := campaign.AcquireHostLease(root)
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
			}
			if fault == "journal corrupt" {
				os.WriteFile(filepath.Join(root, "campaigns/campaign-1/launch/run-manifest.json"), []byte(`{}`), 0600)
			}
			if fault == "binding missing" {
				os.Remove(filepath.Join(root, "campaigns/campaign-1/launch/docker-binding.json"))
			}
			_, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{active: fault == "Docker active"}, p)
			if e == nil || p.statusCalls != 0 || len(p.export.calls) != 0 {
				t.Fatal("contacted native after failed preflight", e, p.statusCalls)
			}
		})
	}
}
func TestLateEvidenceFailuresHaveExplicitImmutableRetries(t *testing.T) {
	for _, fault := range []string{"lost reply", "capacity", "invalid archive", "wrong provenance", "instance changed", "session unknown", "timeout", "interrupted", "publication failed"} {
		t.Run(fault, func(t *testing.T) {
			root, w, p, policy, selection := lateFixture(t)
			w.Close()
			switch fault {
			case "lost reply":
				p.export.err = errors.New("private transport detail")
			case "capacity":
				p.export.err = &interceptor.RemoteError{Response: interceptor.Response{Status: 413, Body: json.RawMessage(`{"code":"evidence_limit_exceeded"}`)}}
			case "invalid archive":
				p.export.data = []byte("invalid")
			case "wrong provenance":
				p.export.data = read(t, "../interceptor/testdata/native-evidence-restored.tar")
			case "instance changed":
				p.native.input.Status.InstanceID = "other"
			case "session unknown":
				p.native.input.Status.Sessions = map[string]interceptor.Binding{}
			case "timeout":
				p.export.wait = true
			case "publication failed":
				p.beforeExport = func() {
					os.WriteFile(filepath.Join(root, "campaigns/campaign-1/evidence-recovery", selection.Identity.SessionID, "result.json.pending"), []byte("partial"), 0600)
				}
			case "interrupted":
				a, e := campaign.OpenNativeRecovery(root, "campaign-1")
				if e != nil {
					t.Fatal(e)
				}
				digest, e := contracts.CanonicalDigest(encode(map[string]any{"policy": policy, "target": selection}), campaign.ManifestLimit)
				if e != nil {
					t.Fatal(e)
				}
				if fresh, _, e := a.BeginEvidence(selection.Identity.SessionID, digest); e != nil || !fresh {
					t.Fatal(e)
				}
				a.Close()
			}
			report, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, p)
			if (e != nil) != (fault == "publication failed") || len(report.Outcomes) != 1 || report.Complete() != (fault == "interrupted") {
				t.Fatal(report, e)
			}
			if fault == "capacity" && report.Outcomes[0].Reason != "evidence_limit_exceeded" {
				t.Fatal(report)
			}
			calls, statuses := len(p.export.calls), p.statusCalls
			again, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, p)
			retry := fault != "capacity" && fault != "interrupted"
			wantCalls, wantStatus := calls, statuses
			if retry {
				wantCalls *= 2
				wantStatus *= 2
			}
			if e != nil || len(p.export.calls) != wantCalls || p.statusCalls != wantStatus || again.Complete() != (fault == "interrupted" || fault == "publication failed") {
				t.Fatal(again, e, p.export.calls, p.statusCalls)
			}
			if retry || fault == "interrupted" {
				if _, e = os.Stat(filepath.Join(root, "campaigns/campaign-1/evidence-recovery", selection.Identity.SessionID, "retries/0001/result.json")); e != nil {
					t.Fatal("retry outcome missing", e)
				}
			}
			if fault == "interrupted" {
				if _, e = os.Stat(filepath.Join(root, "campaigns/campaign-1/evidence-recovery", selection.Identity.SessionID, "result.json")); !os.IsNotExist(e) {
					t.Fatal("overwrote interrupted result", e)
				}
			}
		})
	}
}
func TestLateEvidenceReusesLiveFinalization(t *testing.T) {
	export := &exportPeer{data: read(t, "../interceptor/testdata/native-evidence.tar")}
	s, native, _, w, launch := serviceWithSettings(t, 0, []string{"engine.observation_read"}, evidenceSettings(export, time.Second), nativeEvidenceInput)
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, e := s.Shutdown(ctx)
	if e != nil || len(result.Evidence) != 1 {
		t.Fatal(result, e)
	}
	dir, e := w.EvidenceDirectory()
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	w.Close()
	p := &latePeer{native: native, export: export}
	report, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, p)
	if e != nil || !report.Complete() || len(export.calls) != 1 || p.statusCalls != 0 {

		t.Fatal(report, e)
	}
}

func TestLateEvidenceCollectsVerifiedSourceAndReplacement(t *testing.T) {
	root, w, p, _, source := lateFixture(t)
	restored := read(t, "../interceptor/testdata/native-evidence-restored.tar")
	var session interceptor.Session
	var checkpoint interceptor.Checkpoint
	readMember := func(data []byte, prefix string, value any) {
		t.Helper()
		r := tar.NewReader(bytes.NewReader(data))
		for {
			h, e := r.Next()
			if e == io.EOF {
				t.Fatal("fixture member missing", prefix)
			}
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(h.Name, prefix) {
				if e = json.NewDecoder(r).Decode(value); e != nil {
					t.Fatal(e)
				}
				return
			}
		}
	}
	readMember(restored, "session.json", &session)
	readMember(p.export.data, "checkpoints/", &checkpoint)
	replacement := source
	replacement.Identity.SessionID = session.ID
	replacement.Identity.ParentSessionID = checkpoint.SourceSessionID
	replacement.Identity.ParentCheckpointID = checkpoint.ID
	replacement.Identity.ParentCheckpointDigest = checkpoint.Hash
	replacement.Reservation = "evidence:" + contracts.RawDigest([]byte(session.ID))[7:]
	selectEvidence(t, w, replacement)
	w.Close()
	p.native.input.Status.Sessions[session.ID] = interceptor.Binding{SessionID: session.ID, WorkerInstanceID: "another-worker", RunRevision: 2}
	p.native.input.Status.Sessions["unselected"] = interceptor.Binding{SessionID: "unselected", WorkerInstanceID: "another-worker", RunRevision: 3}
	p.archives = map[string][]byte{source.Identity.SessionID: p.export.data, session.ID: restored}
	report, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, p)
	if e != nil || !report.Complete() || len(report.Outcomes) != 2 || len(p.export.calls) != 2 {
		t.Fatal(report, e)
	}
	if p.export.calls[0].SessionID != source.Identity.SessionID || p.export.calls[1].SessionID != session.ID {
		t.Fatal(p.export.calls)
	}
}
func TestLateEvidenceAdoptsJournaledArchiveAfterLostCollectionResult(t *testing.T) {
	ctx := context.Background()
	root, w, p, policy, selection := lateFixture(t)
	dir, e := w.EvidenceDirectory()
	if e != nil {
		t.Fatal(e)
	}
	download, e := p.export.DownloadEvidence(ctx, interceptor.EvidenceRequest{CampaignID: "campaign-1", SessionID: selection.Identity.SessionID, MaxArchiveBytes: policy.MaxArchiveBytes, InterceptorMaxBytes: selection.NativeMaxBytes}, dir)
	if e != nil {
		t.Fatal(e)
	}
	archive, e := download.InspectArchive(ctx, interceptor.DefaultArchiveLimits(policy.MaxArchiveBytes))
	if e != nil {
		t.Fatal(e)
	}
	verified, e := archive.VerifyProvenance(ctx, selection.Identity)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.RetainEvidence(ctx, verified, selection.Reservation); e != nil {
		t.Fatal(e)
	}
	download.Close()
	w.Close()
	report, e := nativerecovery.CollectEvidence(ctx, root, "campaign-1", &lateDocker{}, p)
	if e != nil || !report.Complete() || len(p.export.calls) != 1 || p.statusCalls != 0 {
		t.Fatal(report, e)
	}
}

func TestLateEvidenceRetriesAnEarlierLiveTransportFailure(t *testing.T) {
	export := &exportPeer{err: errors.New("unavailable")}
	s, native, _, w, launch := serviceWithSettings(t, 0, []string{"engine.observation_read"}, func(c *campaignservice.Config) {
		evidenceSettings(export, time.Second)(c)
		c.Evidence.MaxArchiveBytes = 1 << 20
	}, nativeEvidenceInput)
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	terminal, e := s.Shutdown(ctx)
	if e != nil || len(terminal.Evidence) != 1 || terminal.Evidence[0].State != "missing" {
		t.Fatal(terminal, e)
	}
	dir, e := w.EvidenceDirectory()
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	w.Close()
	export.err = nil
	export.data = read(t, "../interceptor/testdata/native-evidence.tar")
	p := &latePeer{native: native, export: export}
	before, e := campaign.Inspect(root, "campaign-1", nil)
	if e != nil {
		t.Fatal(e)
	}
	report, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, p)
	if e != nil || !report.Complete() || len(export.calls) != 2 {
		t.Fatal(report, e)
	}
	after, e := campaign.Inspect(root, "campaign-1", nil)
	if e != nil || before.VerifiedBytes != after.VerifiedBytes {
		t.Fatal(after, e)
	}
}

func TestLateEvidencePersistenceFailureReportsUnattemptedSessions(t *testing.T) {
	root, w, p, _, source := lateFixture(t)
	next := source
	next.Identity.SessionID = "session-2"
	next.Identity.ParentSessionID = source.Identity.SessionID
	next.Identity.ParentCheckpointID = "cp-1-" + strings.Repeat("0", 24)
	next.Identity.ParentCheckpointDigest = contracts.RawDigest([]byte("checkpoint"))
	next.Reservation = "evidence:" + contracts.RawDigest([]byte(next.Identity.SessionID))[7:]
	selectEvidence(t, w, next)
	w.Close()
	p.beforeExport = func() {
		os.WriteFile(filepath.Join(root, "campaigns/campaign-1/evidence-recovery", source.Identity.SessionID, "result.json.pending"), []byte("partial"), 0600)
	}
	report, e := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, p)
	if e == nil || len(report.Outcomes) != 2 || report.Outcomes[1].Reason != "collection_aborted" || report.Outcomes[1].Recorded || len(p.export.calls) != 1 {
		t.Fatal(report, e)
	}
}
