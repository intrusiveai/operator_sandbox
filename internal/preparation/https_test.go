package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

func httpsInput(t *testing.T, in *preparation.Input, srv *httptest.Server) {
	t.Helper()
	mapping := httpstarget.Settings{APIVersion: httpstarget.Version, Origin: srv.URL, AllowedPrivateCIDRs: []string{"127.0.0.1/32"}, CACertificatesPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), Authentication: httpstarget.Authentication{Mode: "none"}, Operations: []httpstarget.Operation{{ID: "invoke", Method: "POST", Path: "/chat", Input: httpstarget.Input{Format: "json", MediaType: "application/json"}, Response: httpstarget.Response{Format: "json", Field: []string{"answer"}}, MaximumInputBytes: 1024, MaximumRequestBytes: 2048, MaximumResponseBytes: 4096}}}
	settings := in.Profile.Settings()
	settings.Adapter = httpstarget.Adapter
	settings.HTTPS = encode(mapping)
	var err error
	in.Profile, err = targetprofile.Parse(encode(settings))
	if err != nil {
		t.Fatal(err)
	}
	in.CampaignID = "campaign-1"
	in.WorkerInstanceID = "worker-1"
	in.Authoring, err = capabilities.FromHTTPS(in.Protocol.Catalog(), in.Profile.HTTPS(), settings.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	var bundle map[string]any
	_ = json.Unmarshal(in.Bundle, &bundle)
	bundle["target_requirements"].(map[string]any)["capability_source_digest"] = in.Authoring.SourceDigest()
	scenario := bundle["scenarios"].([]any)[0].(map[string]any)
	scenario["required_capability_refs"] = []string{"operation:invoke"}
	scenario["guidance"].(map[string]any)["action_refs"] = []string{}
	in.Bundle = encode(bundle)
}
func httpsWire(p *peer, index int, release string) []byte {
	var q map[string]any
	_ = json.Unmarshal(attemptWire(p, 1, index, release), &q)
	delete(q["body"].(map[string]any), "observation_selection")
	return encode(q)
}
func TestHTTPSCampaignExecutionFeedbackAndReport(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"answer":"observed reply","private":"excluded"}`))
	}))
	defer srv.Close()
	var root string
	s, p, _, w, launch := serviceWithTemplate(t, 0, []string{"engine.attempt_execute", "engine.observation_read"}, func(c *campaignservice.Config) {
		root = c.StateRoot
		var e error
		c.HTTPS, e = httpstarget.New(c.Prepared.Target().Profile().HTTPS(), nil, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		c.Peer = nil
	}, func(v map[string]any) {
		v["limits"].(map[string]any)["campaign"].(map[string]any)["observation_bytes"] = 1024
		v["remaining_limits"].(map[string]any)["observation_bytes"] = 1024
	}, func(in *preparation.Input) { httpsInput(t, in, srv) })
	if bytes.Contains(launch.HostPolicy, []byte(srv.URL)) || bytes.Contains(launch.EngineContext, []byte("CERTIFICATE")) {
		t.Fatal("private configuration exposed")
	}
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	var receiptID, entryID string
	for i := 1; i <= 2; i++ {
		q := httpsWire(p, i, w.Manifest().ReleaseRecordDigest)
		raw, e := s.Handle(context.Background(), q, int64(i))
		if e != nil {
			t.Fatal(e)
		}
		var response struct {
			Result struct {
				Status   string            `json:"status"`
				Receipt  string            `json:"receipt_id"`
				Feedback feedback.Manifest `json:"feedback"`
			}
		}
		if json.Unmarshal(raw, &response) != nil || response.Result.Status != "completed" {
			t.Fatal(string(raw))
		}
		if len(response.Result.Feedback.Entries) != 1 || response.Result.Feedback.Entries[0].Assurance != "declared-observer" {
			t.Fatal(string(raw))
		}
		receiptID = response.Result.Receipt
		entryID = response.Result.Feedback.Entries[0].ID
		// Correlated duplicate replay must not send another request.
		if _, e = s.Handle(context.Background(), q, int64(i+5)); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("replayed effects", calls.Load())
	}
	readRequest := encode(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 20, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": 1, "call_id": "read", "operation_id": "read", "operation": "engine.observation_read", "timeout_ms": 1000, "body": map[string]any{"receipt_id": receiptID, "entry_id": entryID, "offset": 0, "max_bytes": 256}})
	raw, e := s.Handle(context.Background(), readRequest, 21)
	if e != nil {
		t.Fatal(e)
	}
	var read struct {
		Result feedback.ReadResult `json:"result"`
	}
	_ = json.Unmarshal(raw, &read)
	if string(read.Result.Content) != "observed reply" {
		t.Fatal(string(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	terminal, e := s.Shutdown(ctx)
	if e != nil || terminal.Closure != "not-applicable" || terminal.CleanupState != "not-needed" {
		t.Fatal(terminal, e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	recovery, e := nativerecovery.Run(ctx, root, "campaign-1", true, &peer{})
	if e != nil || recovery.Closure != "not-applicable" {
		t.Fatal(recovery, e)
	}
	receipt, e := reporting.Generate(ctx, root, "campaign-1", "")
	if e != nil {
		t.Fatal(e)
	}
	report := reportResult(t, root, receipt)
	if report.Assurance != "declared-observer" || len(report.UntestedOperations) > 0 || slices.Contains(report.Gaps, "native_evidence_unavailable") {
		t.Fatal(report)
	}

}
func TestHTTPSProductionLaunchOmitsUnsupportedTools(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("startup contacted target") }))
	defer srv.Close()
	in := fixture(t)
	httpsInput(t, &in, srv)
	target, e := preparation.Build(in)
	if e != nil {
		t.Fatal(e)
	}
	launch, e := target.BuildLaunch(launchConfig(t, target, "openai-chat-text-tools-v1"))
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Operations []string `json:"operations"`
	}
	_ = json.Unmarshal(launch.Inputs.EngineContext, &v)
	for _, name := range []string{"engine.injection_delete", "engine.snapshot_request", "engine.restore_request", "engine.snapshot_list", "engine.snapshot_inspect"} {
		if slices.Contains(v.Operations, name) {
			t.Fatal("advertised", name)
		}
	}
}

func TestHTTPSFailuresCloseExecutionAndRetainOutcome(t *testing.T) {
	for _, mode := range []string{"status", "disconnect", "oversize", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			requestContext, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "status":
					w.WriteHeader(http.StatusInternalServerError)
				case "disconnect":
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
				case "oversize":
					_, _ = w.Write(bytes.Repeat([]byte("x"), 4097))
				case "cancel":
					cancelRequest()
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
				}
			}))
			defer srv.Close()
			var root string
			s, p, r, w, launch := serviceWithSettings(t, 0, []string{"engine.attempt_execute"}, func(c *campaignservice.Config) {
				root = c.StateRoot
				var e error
				c.HTTPS, e = httpstarget.New(c.Prepared.Target().Profile().HTTPS(), nil, time.Second)
				if e != nil {
					t.Fatal(e)
				}
				c.Peer = nil
			}, func(in *preparation.Input) { httpsInput(t, in, srv) })
			if e := s.Admit(context.Background(), launch); e != nil {
				t.Fatal(e)
			}
			q := httpsWire(p, 1, w.Manifest().ReleaseRecordDigest)
			// Terminal failures may cancel delivery of the response to the guest;
			// the journal must still contain the final correlated outcome.
			_, _ = s.Handle(requestContext, q, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			terminal, e := s.Shutdown(ctx)
			if e != nil || terminal.TargetStop != "not-applicable" {
				t.Fatal(terminal, e)
			}
			select {
			case <-r.killed:
			default:
				t.Fatal("harness termination not independent")
			}
			if _, e := s.Handle(context.Background(), q, 2); e == nil {
				t.Fatal("failed execution reopened")
			}
			if calls.Load() != 1 {
				t.Fatal("request repeated", calls.Load())
			}
			if e = w.Close(); e != nil {
				t.Fatal(e)
			}
			receipt, e := reporting.Generate(ctx, root, "campaign-1", "")
			if e != nil {
				t.Fatal(e)
			}
			report := reportResult(t, root, receipt)
			if len(report.Attempts) != 1 {
				t.Fatal(report)
			}
			expected := "failed"
			if mode == "disconnect" || mode == "cancel" {
				expected = "unknown"
			}
			if report.Attempts[0].State != expected || report.Attempts[0].Invocation != expected || report.Attempts[0].Feedback == nil || report.Assurance != "declared-observer" {
				t.Fatal(report)
			}
		})
	}
}
func TestHTTPSInterruptedPreparationRecoveryNeverContactsNative(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("recovery contacted target") }))
	defer srv.Close()
	in := fixture(t)
	httpsInput(t, &in, srv)
	target, e := preparation.Build(in)
	if e != nil {
		t.Fatal(e)
	}
	w, launch, root := preparedLaunch(t, target, "engine.attempt_execute")
	if _, e = target.Persist(w, launch.EngineContext); e != nil {
		t.Fatal(e)
	}
	w.Close()
	// Empty peer would be invalid if any native query were attempted.
	out, e := nativerecovery.Run(context.Background(), root, "campaign-1", true, &peer{})
	if e != nil || out.State != "finalized" || out.Closure != "not-applicable" {
		t.Fatal(out, e)
	}
	again, e := nativerecovery.Run(context.Background(), root, "campaign-1", true, &peer{})
	if e != nil || again != out {
		t.Fatal(again, e)
	}
}
