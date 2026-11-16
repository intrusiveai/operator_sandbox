package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func nativeRejection(t *testing.T, status int, code, message string) interceptor.Response {
	t.Helper()
	r, err := interceptor.ParseResponse(encode(map[string]any{"status": status, "session_revision": 26, "body": map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": "req-native-1"}}}))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Real spool transport, production service/journal/export, and separate CLI
// processes; the native target and termination controller are controlled fixtures.
func TestSnapshotFailureSurvivesTransportCleanupInspectionAndExport(t *testing.T) {
	s, p, r, w, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	p.nativeResponses = map[string]interceptor.Response{
		"snapshot.create": nativeRejection(t, 409, "snapshot_failed", "scan mutable root /state/app: open /state/app/evidence: permission denied"),
		"session.owner":   nativeRejection(t, 409, "closure_failed", "controller could not confirm ownership closure"),
	}
	var spool string
	serviceSpool(t, s, p, w, launch, func(dir string) { spool = dir })
	if err := spoolPut(spool, "ordinary-out", 0, stateWire("engine.snapshot_request", "snapshot-1", 1, map[string]any{})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Fence().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot failure did not stop execution")
	}
	terminal, err := s.Shutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.killed:
	default:
		t.Fatal("owned termination did not run")
	}
	d := s.WorkflowFailure()
	if d == nil || !d.Valid() || d.Integration == nil || d.Integration.Code != "snapshot_failed" || s.FailureTrigger() == nil || s.FailureTrigger().OperationID != "snapshot-1" {
		t.Fatalf("lost primary: %+v", d)
	}
	if terminal.Closure != "unconfirmed" || terminal.Failures == nil || len(*terminal.Failures) != 1 || (*terminal.Failures)[0].Code != "closure_failed" {
		t.Fatalf("lost secondary: %+v", terminal)
	}
	count := 0
	for _, op := range p.calls {
		if op == "snapshot.create" {
			count++
		}
		if op == "application.invoke" || op == "snapshot.restore" {
			t.Fatal("unexpected effect", op)
		}
	}
	if count != 1 {
		t.Fatal("snapshot replayed", count)
	}
	if _, err = s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot-1", 1, map[string]any{}), 1); err == nil {
		t.Fatal("execution reopened")
	}
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	binary := processCLI(t)
	for _, verb := range []string{"inspect", "report", "export"} {
		args := []string{verb, "--state-root", root, "--campaign", "campaign-1"}
		output := filepath.Join(t.TempDir(), "export")
		if verb == "export" {
			args = append(args, "--output", output)
		}
		raw, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", verb, err, raw)
		}
		if verb == "inspect" && !bytes.Contains(raw, []byte("snapshot_failed")) {
			t.Fatal("inspection lost diagnostic", string(raw))
		}
		if verb != "export" {
			continue
		}
		var result reporting.Result
		if err = json.Unmarshal(read(t, filepath.Join(output, "result-manifest.json")), &result); err != nil {
			t.Fatal(err)
		}
		if result.FailureTrigger == nil || result.FailureTrigger.OperationID != "snapshot-1" {
			t.Fatal("export lost trigger", result.FailureTrigger)
		}
		report := string(read(t, filepath.Join(output, "report.md")))
		for _, want := range []string{"snapshot.create", "snapshot_failed", "permission denied", "req-native-1", "closure_failed"} {
			if !strings.Contains(report, want) {
				t.Fatalf("missing %s in report: %s", want, report)
			}
		}
		events := read(t, filepath.Join(output, "events", "000001.jsonl"))
		if !bytes.Contains(events, []byte("permission denied")) {
			t.Fatal("export lost structured detail")
		}
	}
}

func TestNativeAttempt500RetainsUnknownOutcomeAndCause(t *testing.T) {
	s, p, _, w, launch := serviceWithOperations(t, 0, []string{"engine.attempt_execute"})
	p.nativeResponses = map[string]interceptor.Response{"application.invoke": nativeRejection(t, 503, "future_backend_error", "application worker unavailable")}
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0)
	if err != nil || !bytes.Contains(raw, []byte(`"status":"unknown"`)) {
		t.Fatalf("%s %v", raw, err)
	}
	if bytes.Contains(raw, []byte("application worker unavailable")) {
		t.Fatal("private native detail leaked to black-box Harness")
	}
	d := s.WorkflowFailure()
	if d == nil || d.TargetFailure == nil || d.TargetFailure.Integration == nil || !strings.Contains(d.Message(), "future_backend_error") {
		t.Fatalf("lost unknown outcome cause: %+v", d)
	}
	s.Shutdown(context.Background())
}

func TestStatusFailurePreservesRemoteCause(t *testing.T) {
	s, p, _, _, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.nativeResponses = map[string]interceptor.Response{"status": nativeRejection(t, 503, "store_unavailable", "state store unavailable")}
	p.mu.Unlock()
	_, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot-status", 1, map[string]any{}), 0)
	if err == nil {
		t.Fatal("status error accepted")
	}
	d := s.WorkflowFailure()
	if d == nil || d.Integration == nil || !strings.Contains(d.Message(), "store_unavailable") {
		t.Fatal(d, err)
	}
	s.Shutdown(context.Background())
}

func TestSnapshotCauseSurvivesResponseRetentionFailure(t *testing.T) {
	s, p, _, w, launch := serviceWithOperations(t, 0, snapshotRoutes, snapshotInput(t))
	p.nativeResponses = map[string]interceptor.Response{"snapshot.create": nativeRejection(t, 409, "snapshot_failed", "scan mutable root: permission denied")}
	p.beforeNativeRejection = func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	}
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	_, err := s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot-1", 1, map[string]any{}), 1)
	if err == nil || !errors.Is(err, campaign.ErrClosed) {
		t.Fatal(err)
	}
	var remote *interceptor.RemoteError
	if !errors.As(err, &remote) {
		t.Fatal("lost original native rejection", err)
	}
	d := s.WorkflowFailure()
	if d == nil || d.Integration == nil || d.Integration.Code != "snapshot_failed" || !strings.Contains(d.Integration.Omissions, "response_retention:failed") || s.FailureTrigger() == nil {
		t.Fatalf("lost completion diagnostic after journal loss: %+v", d)
	}
}
