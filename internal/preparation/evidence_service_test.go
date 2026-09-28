package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
)

type exportPeer struct {
	mu      sync.Mutex
	data    []byte
	err     error
	entered chan struct{}
	wait    bool
	calls   []interceptor.EvidenceRequest
}

func (p *exportPeer) DownloadEvidence(ctx context.Context, q interceptor.EvidenceRequest, dir string) (*interceptor.EvidenceDownload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, q)
	p.mu.Unlock()
	if p.entered != nil {
		close(p.entered)
	}
	if p.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.err != nil {
		return nil, p.err
	}
	return interceptor.StageEvidence(ctx, interceptor.EvidenceReceipt{CampaignID: q.CampaignID, SessionID: q.SessionID, Bytes: int64(len(p.data)), SHA256: contracts.RawDigest(p.data), LocalMaxBytes: q.MaxArchiveBytes, InterceptorMaxBytes: q.InterceptorMaxBytes}, bytes.NewReader(p.data), dir)
}
func nativeEvidenceInput(in *preparation.Input) {
	in.Attachment.Binding.SessionID = "sess-1-000000000000000000000000"
	in.Attachment.Session.ID = in.Attachment.Binding.SessionID
	in.Attachment.EvidenceMaxBytes = 4 << 30
	in.Status.Active = in.Attachment.Binding
	in.Status.Sessions = map[string]interceptor.Binding{in.Attachment.Binding.SessionID: in.Attachment.Binding}
	in.Status.EvidenceMaxBytes = 4 << 30
}
func evidenceSettings(peer *exportPeer, timeout time.Duration) func(*campaignservice.Config) {
	return func(c *campaignservice.Config) {
		c.Evidence = &campaignservice.EvidenceConfig{Peer: peer, MaxArchiveBytes: 4 << 30, Timeout: timeout, TotalTimeout: timeout}
	}
}
func TestServiceEvidenceRetainsCompleteAndPartialNativeExports(t *testing.T) {
	for _, name := range []string{"native-evidence.tar", "native-evidence-partial.tar"} {
		t.Run(name, func(t *testing.T) {
			peer := &exportPeer{data: read(t, "../interceptor/testdata/"+name)}
			s, _, runtime, w, launch := serviceWithSettings(t, 0, []string{"engine.observation_read"}, evidenceSettings(peer, time.Second), nativeEvidenceInput)
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := s.Shutdown(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if name == "native-evidence-partial.tar" {
				want = "partial"
			}
			if len(result.Evidence) != 1 || result.Evidence[0].State != want || !result.Evidence[0].Recorded || result.Evidence[0].ArchiveDigest != contracts.RawDigest(peer.data) {
				t.Fatal(result)
			}
			select {
			case <-runtime.killed:
			default:
				t.Fatal("Docker termination missing")
			}
			result.Evidence[0].State = "changed"
			again, _, err := s.Wait(ctx)
			if err != nil || again.Evidence[0].State != want {
				t.Fatal(again, err)
			}
			dir, err := w.EvidenceDirectory()
			if err != nil {
				t.Fatal(err)
			}
			pending, err := filepath.Glob(filepath.Join(dir, "*.pending"))
			if err != nil || len(pending) != 0 {
				t.Fatal(pending, err)
			}
			root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			adopted, completed, policies := 0, 0, 0
			inspection, err := campaign.Inspect(root, "campaign-1", func(e campaign.Event) error {
				switch e.Kind {
				case "evidence.policy":
					policies++
					var policy campaign.EvidencePolicy
					if json.Unmarshal(e.Metadata, &policy) != nil || !policy.Valid() || policy.MaxArchiveBytes != 4<<30 || policy.TimeoutNS != int64(time.Second) || policy.TotalTimeoutNS != int64(time.Second) {
						t.Fatal(policy)
					}
				case "evidence.adopted":
					adopted++
				case "evidence.collection-result":
					completed++
				case "service.terminal-result":
					if len(e.Content) != 1 {
						t.Fatal("terminal result not persisted")
					}
				}
				return nil
			})
			if err != nil || !inspection.JournalIntact || adopted != 1 || completed != 1 || policies != 1 {
				t.Fatal(inspection, err, adopted, completed)
			}
		})
	}
}
func TestEvidenceFailuresDoNotBlockTerminationOrChangeExecution(t *testing.T) {
	for _, mode := range []string{"timeout", "capacity", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			peer := &exportPeer{}
			switch mode {
			case "timeout":
				peer.wait = true
				peer.entered = make(chan struct{})
			case "capacity":
				peer.err = &interceptor.RemoteError{Response: interceptor.Response{Status: 413, Body: json.RawMessage(`{"code":"evidence_limit_exceeded","maximum_bytes":1024,"evidence_retained":true}`)}}
			case "invalid":
				peer.data = []byte("not a native archive")
			}
			s, _, runtime, _, launch := serviceWithSettings(t, 0, []string{"engine.observation_read"}, evidenceSettings(peer, 300*time.Millisecond), nativeEvidenceInput)
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			s.Stop(errors.New("test campaign finished"))
			if mode == "timeout" {
				select {
				case <-peer.entered:
				case <-time.After(time.Second):
					t.Fatal("export did not start")
				}
			}
			select {
			case <-runtime.killed:
			case <-time.After(time.Second):
				t.Fatal("export held Docker termination")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, _, err := s.Wait(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result.Closure != "confirmed" || len(result.Evidence) != 1 || !result.Evidence[0].Recorded || result.Evidence[0].ArchivePath != "" || len(peer.calls) != 1 {
				t.Fatal(result, peer.calls)
			}
			expected := map[string]string{"timeout": "collection_deadline", "capacity": "evidence_limit_exceeded", "invalid": "archive_invalid_size"}[mode]
			if mode == "invalid" {
				if result.Evidence[0].State != "invalid" {
					t.Fatal(result)
				}
			} else if result.Evidence[0].Reason != expected {
				t.Fatal(result)
			}
		})
	}
}
func TestEvidenceMissingPeerIsExplicit(t *testing.T) {
	s, _, _, w, launch := serviceFixture(t)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Shutdown(ctx)
	if err != nil || len(result.Evidence) != 1 || result.Evidence[0].State != "missing" || result.Evidence[0].Reason != "export_unavailable" {
		t.Fatal(result, err)
	}
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

func TestEvidenceSelectsSourceAndReplacementOnce(t *testing.T) {
	export := &exportPeer{err: errors.New("source unavailable")}
	s, _, _, _, launch := serviceWithSettings(t, 0, snapshotRoutes, evidenceSettings(export, time.Second), nativeEvidenceInput, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "baseline", 1, map[string]any{}), 0)
	cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
	handle := map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}
	raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, handle), 0)
	stateResult(t, raw, err)
	raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 2, handle), 0)
	stateResult(t, raw, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Shutdown(ctx)
	if err != nil || len(result.Evidence) != 2 || len(export.calls) != 2 {
		t.Fatal(result, err, export.calls)
	}
	if export.calls[0].SessionID != "sess-1-000000000000000000000000" || export.calls[1].SessionID != "session-2" {
		t.Fatal(export.calls)
	}
}
