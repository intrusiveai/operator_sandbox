package preparation_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

// Extend the golden archive with an authoritative native lifecycle event. Keep
// the original execution evidence and update the event chain/bundle digest.
func stoppedArchive(t *testing.T, data []byte) []byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(data))
	var out bytes.Buffer
	writer := tar.NewWriter(&out)
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		switch h.Name {
		case "events.jsonl":
			lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
			var last struct{ Hash string }
			if err = json.Unmarshal(lines[len(lines)-1], &last); err != nil {
				t.Fatal(err)
			}
			event := `{"schema_version":1,"session_id":"sess-1-000000000000000000000000","seq":3,"observed_at":"2026-10-01T00:04:00Z","channel":"control","layer":"sandboxd","kind":"session_stopped","provenance":"sandbox_control","attribution":{},"semantic":{},"previous_hash":"` + last.Hash + `","hash":""}`
			digest := contracts.RawDigest([]byte(event))
			event = strings.Replace(event, `"hash":""`, `"hash":"`+digest+`"`, 1)
			raw = append(raw, []byte(event+"\n")...)
		case "evidence-bundle.json":
			var compact bytes.Buffer
			if err = json.Compact(&compact, raw); err != nil {
				t.Fatal(err)
			}
			raw = compact.Bytes()
			raw = bytes.Replace(raw, []byte(`"end_sequence":2`), []byte(`"end_sequence":3`), 1)
			var b struct{ Digest string }
			if err = json.Unmarshal(raw, &b); err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"digest":"`+b.Digest+`"`), []byte(`"digest":""`), 1)
			hash := contracts.RawDigest(raw)
			raw = bytes.Replace(raw, []byte(`"digest":""`), []byte(`"digest":"`+hash+`"`), 1)
		}
		h.Size = int64(len(raw))
		if err = writer.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestTargetTurnFailureAndLaterCleanupEvidence(t *testing.T) {
	// The simulated target waits on a local model request until its own turn
	// deadline expires. The Operator attempt still has ample time remaining.
	modelEntered := make(chan struct{})
	modelCanceled := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(modelEntered)
		<-r.Context().Done()
		close(modelCanceled)
	}))
	defer model.Close()
	export := &exportPeer{err: &interceptor.RemoteError{Response: interceptor.Response{Status: 409, Body: json.RawMessage(`{"code":"evidence_unavailable_or_target_not_stopped"}`)}}}
	s, native, _, w, launch := serviceWithSettings(t, 0, []string{"engine.attempt_execute", "engine.observation_read"}, func(c *campaignservice.Config) {
		evidenceSettings(export, time.Second)(c)
		c.Evidence.MaxArchiveBytes = 1 << 20
	}, nativeEvidenceInput)
	native.unknownClose = true
	native.invokeTarget = func() string {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, model.URL, nil)
		completed := make(chan error, 1)
		go func() {
			response, err := model.Client().Do(req)
			if response != nil {
				response.Body.Close()
			}
			completed <- err
		}()
		select {
		case <-modelEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("model not reached")
		}
		// Explicitly model a target-owned deadline, rather than waiting 60 seconds.
		deadline, done := context.WithDeadline(ctx, time.Now().Add(-time.Millisecond))
		defer done()
		cancel()
		<-completed
		<-modelCanceled
		return deadline.Err().Error()
	}
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), attemptWire(native, 1, 1, w.Manifest().ReleaseRecordDigest), 0)
	if err != nil || !bytes.Contains(raw, []byte(`"status":"failed"`)) {
		t.Fatalf("%s %v", raw, err)
	}
	d := s.WorkflowFailure()
	if d == nil || d.TargetFailure == nil || !d.Valid() || d.TargetFailure.Category != "target_turn_timeout" || d.Timeout != nil || d.TargetFailure.Detail != "context deadline exceeded" {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(d.Message(), "target application turn exceeded its deadline") {
		t.Fatal(d.Message())
	}
	// A black-box campaign does not receive protected native diagnostic prose.
	if bytes.Contains(raw, []byte("context deadline exceeded")) {
		t.Fatal("black-box policy bypass")
	}
	if _, err = s.Handle(context.Background(), attemptWire(native, 1, 1, w.Manifest().ReleaseRecordDigest), 1); err == nil {
		t.Fatal("terminal target replayed")
	}
	terminal, err := s.Shutdown(context.Background())
	if err != nil || terminal.Closure != "unconfirmed" || terminal.CleanupState != "unconfirmed" || len(terminal.Evidence) != 1 || terminal.Evidence[0].NativeCode != "evidence_unavailable_or_target_not_stopped" {
		t.Fatalf("%+v %v", terminal, err)
	}
	count := 0
	for _, call := range native.calls {
		if call == "application.invoke" {
			count++
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	w.Close()
	initial, err := campaign.Inspect(root, "campaign-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	export.err = nil
	export.data = stoppedArchive(t, read(t, "../interceptor/testdata/native-evidence.tar"))
	collected, err := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, &latePeer{native: native, export: export})
	if err != nil || !collected.Complete() {
		t.Fatalf("%+v %v", collected, err)
	}
	after, err := campaign.Inspect(root, "campaign-1", nil)
	if err != nil || initial.VerifiedBytes != after.VerifiedBytes {
		t.Fatal("history changed", err)
	}
	originalFound := false
	if _, err = campaign.Inspect(root, "campaign-1", func(event campaign.Event) error {
		for _, desc := range event.Content {
			if desc.Digest == d.TargetFailure.ResponseDigest {
				raw := read(t, filepath.Join(root, "campaigns", "campaign-1", desc.Path))
				originalFound = bytes.Contains(raw, []byte("context deadline exceeded"))
			}
		}
		return nil
	}); err != nil || !originalFound {
		t.Fatal("original native response lost", err)
	}
	binary := processCLI(t)
	for _, verb := range []string{"inspect", "report", "export"} {
		args := []string{verb, "--state-root", root, "--campaign", "campaign-1"}
		output := filepath.Join(t.TempDir(), "export")
		if verb == "export" {
			args = append(args, "--output", output)
		}
		data, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s %v", verb, data, err)
		}
		if verb == "inspect" && !bytes.Contains(data, []byte("context deadline exceeded")) {
			t.Fatal("inspection lost cause", string(data))
		}
		if verb != "export" {
			continue
		}
		var result reporting.Result
		raw := read(t, filepath.Join(output, "result-manifest.json"))
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Attempts) != 1 || result.Attempts[0].TargetFailure == nil || result.Attempts[0].Invocation != "failed" {
			t.Fatal(string(raw))
		}
		if len(result.CleanupResolutions) != 1 || result.CleanupResolutions[0].Collection != "complete" || result.CleanupResolutions[0].Stop == nil || result.TargetClosure != "unconfirmed" || result.InjectionCleanup != "unconfirmed" {
			t.Fatal(string(raw))
		}
		report := string(read(t, filepath.Join(output, "report.md")))
		for _, want := range []string{"target application turn exceeded its deadline", "context deadline exceeded", "Currently verified evidence collection: complete", "Recorded collection outcome 1: missing", "native HTTP 409", "evidence_unavailable_or_target_not_stopped", "Target stop confirmed", "injection cleanup: unconfirmed"} {
			if !strings.Contains(report, want) {
				t.Fatalf("missing %q in %s", want, report)
			}
		}
		events := read(t, filepath.Join(output, "events/000001.jsonl"))
		for _, want := range []string{"context deadline exceeded", "native_http_status", "evidence_unavailable_or_target_not_stopped"} {
			if !bytes.Contains(events, []byte(want)) {
				t.Fatalf("export lost %s", want)
			}
		}

	}
}

func TestTargetFailureIsGeneralAndCredentialFiltered(t *testing.T) {
	for _, message := range []string{"tool database query failed", "context canceled", "target rejected authorization: Bearer synthetic-secret"} {
		t.Run(message, func(t *testing.T) {
			s, p, _, w, launch := serviceFixture(t)
			p.turnError = message
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0); err != nil {
				t.Fatal(err)
			}
			d := s.WorkflowFailure()
			if d == nil || d.TargetFailure == nil || d.TargetFailure.Category != "target_turn_failed" || strings.Contains(d.Message(), "synthetic-secret") {
				t.Fatalf("%+v", d)
			}
			if !strings.Contains(message, "authorization") && d.TargetFailure.Detail != message {
				t.Fatal(d.Message())
			}
			s.Shutdown(context.Background())
		})
	}
}
