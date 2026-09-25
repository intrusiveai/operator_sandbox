//go:build linux || darwin

package campaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

func nativeRequest(t *testing.T, id string) interceptor.PreparedOperation {
	t.Helper()
	p, err := interceptor.PrepareOperation(interceptor.OperationRequest{RequestID: "request-" + id, OperationID: id, Operation: "application.invoke", CampaignID: "campaign-1", SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 3, AttemptID: "attempt-parent", Deadline: time.Now().Add(time.Minute)}, []byte(`{ "operation":"invoke", "input":"<>&" }`))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func nativeWriter(t *testing.T) (string, *Writer, *Attempts, *NativeSteps) {
	t.Helper()
	root, w, a := attemptWriter(t, 10)
	observe(t, a, "parent", 6)
	admit(t, a, "parent")
	dispatch(t, a, "parent")
	return root, w, a, a.NativeSteps()
}

var nativeOK = []byte(`{ "status":200,"session_revision":4,"body":{"result":"ok"} }`)

func TestNativeStepDurableDispatchAndReplay(t *testing.T) {
	root, w, a, s := nativeWriter(t)
	p := nativeRequest(t, "invoke")
	r, replay, err := s.Begin("parent", p)
	if err != nil || replay {
		t.Fatal(replay, err)
	}
	if err := a.Resolve("parent", completion("succeeded")); !errors.Is(err, ErrActive) {
		t.Fatal("parent completed ahead of native step", err)
	}
	var wg sync.WaitGroup
	fresh := make(chan bool, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() { got, err := a.NativeSteps().MarkDispatched(r.ID); fresh <- got; errs <- err })
	}
	wg.Wait()
	close(fresh)
	close(errs)
	count := 0
	for v := range fresh {
		if v {
			count++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatal("duplicate dispatch grants", count)
	}
	if err = s.Resolve(r.ID, "succeeded", nativeOK); err != nil {
		t.Fatal(err)
	}
	if err = s.Resolve(r.ID, "succeeded", nativeOK); err != nil {
		t.Fatal(err)
	}
	saved, prepared, response, err := s.Lookup(r.ID)
	if err != nil || !bytes.Equal(prepared.Bytes(), p.Bytes()) || !bytes.Equal(response, nativeOK) || saved.Outcome != "succeeded" {
		t.Fatal(saved, err)
	}
	// Same campaign data remains accessible using new worker/revision attribution.
	q := p.Request()
	q.WorkerInstanceID = "other-worker"
	q.RunRevision = 4
	differentAttribution, err := interceptor.PrepareOperation(q, []byte(`{ "operation":"invoke", "input":"<>&" }`))
	if err != nil {
		t.Fatal(err)
	}
	repeated, replay, err := s.Begin("parent", differentAttribution)
	if err != nil || !replay || repeated.WorkerInstanceID != "worker-1" {
		t.Fatal("attribution restricted replay", err)
	}
	q.BodyDigest = ""
	changed, err := interceptor.PrepareOperation(q, []byte(`{"operation":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Begin("parent", changed); err == nil {
		t.Fatal("changed effect accepted")
	}
	if err = a.Resolve("parent", completion("succeeded")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Begin("parent", nativeRequest(t, "new")); err == nil {
		t.Fatal("completed parent admitted a new effect")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(root, "campaign-1", nil)
	if err != nil || !report.JournalIntact || len(report.Reservations) != 0 {
		t.Fatal(report, err)
	}
	for _, op := range report.Operations {
		if op.Outcome != ResultCommitted {
			t.Fatal(op)
		}
	}
}
func TestNativeUnknownReconciliationNeverResumes(t *testing.T) {
	root, w, a, s := nativeWriter(t)
	p := nativeRequest(t, "invoke")
	r, _, err := s.Begin("parent", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarkDispatched(r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Resolve(r.ID, "unknown", nil); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(w.Fence().Err(), ErrUnknownOutcome) {
		t.Fatal("not fenced")
	}
	if err = a.Resolve("parent", completion("succeeded")); err == nil {
		t.Fatal("unknown parent became successful")
	}
	if err = a.Resolve("parent", completion("unknown")); err != nil {
		t.Fatal(err)
	}
	q := p.Request()
	q.RequestID = "lookup-request"
	q.OperationID = "lookup"
	q.Operation = "operation.status"
	q.BodyDigest = ""
	q.AttemptID = ""
	q.AttemptContextDigest = ""
	query, err := interceptor.PrepareOperationStatus(q, p)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.BeginReconciliation(r.ID, query); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	if err = s.ResolveReconciliation(r.ID, nativeOK); err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.BeginReconciliation(r.ID, query); err != nil || fresh {
		t.Fatal("replayed reconciliation", fresh, err)
	}
	if err = s.ResolveReconciliation(r.ID, nativeOK); err != nil {
		t.Fatal(err)
	}
	got, raw, err := s.Reconciliation(r.ID)
	if err != nil || !bytes.Equal(got.Bytes(), query.Bytes()) || !bytes.Equal(raw, nativeOK) {
		t.Fatal(err)
	}
	final, _, _, err := s.Lookup(r.ID)
	if err != nil || final.Outcome != "unknown" || final.State != Unknown {
		t.Fatal("reconciliation reopened native step", final, err)
	}
	if fresh, err := s.MarkDispatched(r.ID); fresh || err != nil {
		t.Fatal("second effect", fresh, err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(root, "campaign-1", nil)
	if err != nil || len(report.Reservations) != 0 {
		t.Fatal(report, err)
	}
	for _, op := range report.Operations {
		if op.Outcome != Unknown {
			t.Fatal("recovery advertised success", op)
		}
	}
}
func TestNativeFailureFencesBeforeJournalLock(t *testing.T) {
	for _, outcome := range []string{"unknown", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			_, w, _, s := nativeWriter(t)
			r, _, err := s.Begin("parent", nativeRequest(t, "invoke"))
			if err != nil {
				t.Fatal(err)
			}
			s.MarkDispatched(r.ID)
			w.mu.Lock()
			done := make(chan error, 1)
			go func() {
				var raw []byte
				if outcome == "failed" {
					raw = []byte(`{"status":409,"session_revision":4,"body":{"code":"failed"}}`)
				}
				done <- s.Resolve(r.ID, outcome, raw)
			}()
			select {
			case <-w.Fence().Done():
			case <-time.After(time.Second):
				w.mu.Unlock()
				t.Fatal("fence blocked on journal")
			}
			w.mu.Unlock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeLargeResponseAndCorruption(t *testing.T) {
	root, _, _, s := nativeWriter(t)
	r, _, err := s.Begin("parent", nativeRequest(t, "invoke"))
	if err != nil {
		t.Fatal(err)
	}
	s.MarkDispatched(r.ID)
	raw, _ := json.Marshal(map[string]any{"status": 200, "session_revision": 4, "body": map[string]any{"value": strings.Repeat("a", MaxContentBytes)}})
	if err = s.Resolve(r.ID, "succeeded", raw); err != nil {
		t.Fatal(err)
	}
	saved, _, got, err := s.Lookup(r.ID)
	if err != nil || !bytes.Equal(raw, got) || len(saved.Response) != 2 {
		t.Fatal("response parts", len(saved.Response), err)
	}
	path := filepath.Join(root, "campaigns", "campaign-1", saved.Response[0].Path)
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.Lookup(r.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if s.Fence().Err() == nil {
		t.Fatal("corrupt response did not fence")
	}
}
func TestNativeRejectsOutOfOrderAndWrongBindings(t *testing.T) {
	_, _, _, s := nativeWriter(t)
	p := nativeRequest(t, "invoke")
	for _, mutate := range []func(*interceptor.OperationRequest){func(q *interceptor.OperationRequest) { q.CampaignID = "other" }, func(q *interceptor.OperationRequest) { q.SessionID = "other" }, func(q *interceptor.OperationRequest) { q.RunRevision++ }, func(q *interceptor.OperationRequest) { q.AttemptID = "other" }} {
		q := p.Request()
		mutate(&q)
		q.BodyDigest = ""
		bad, err := interceptor.PrepareOperation(q, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.Begin("parent", bad); err == nil {
			t.Fatal("foreign binding accepted")
		}
	}
	r, _, err := s.Begin("parent", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Begin("parent", nativeRequest(t, "next")); !errors.Is(err, ErrActive) {
		t.Fatal("overlapping steps", err)
	}
	if err = s.Resolve(r.ID, "succeeded", nativeOK); err == nil {
		t.Fatal("undispatched success")
	}
	if err = s.Resolve(r.ID, "not_dispatched", nil); err != nil {
		t.Fatal(err)
	}
	if s.Fence().Err() == nil {
		t.Fatal("undeliverable admitted plan continued")
	}
}

func TestNativeStorageFailureDoesNotGrantEffectOrResult(t *testing.T) {
	for _, stage := range []string{"intent", "dispatch", "result", "reconciliation-intent", "reconciliation-result"} {
		t.Run(stage, func(t *testing.T) {
			root, w, _, s := nativeWriter(t)
			p := nativeRequest(t, "invoke")
			var r NativeStep
			var err error
			if stage != "intent" {
				r, _, err = s.Begin("parent", p)
				if err != nil {
					t.Fatal(err)
				}
			}
			if stage != "intent" && stage != "dispatch" {
				if _, err = s.MarkDispatched(r.ID); err != nil {
					t.Fatal(err)
				}
			}
			var query interceptor.PreparedOperation
			if strings.HasPrefix(stage, "reconciliation") {
				if err = s.Resolve(r.ID, "unknown", nil); err != nil {
					t.Fatal(err)
				}
				q := p.Request()
				q.RequestID = "lookup-request"
				q.OperationID = "lookup"
				q.Operation = "operation.status"
				q.AttemptID = ""
				q.BodyDigest = ""
				query, err = interceptor.PrepareOperationStatus(q, p)
				if err != nil {
					t.Fatal(err)
				}
				if stage == "reconciliation-result" {
					if _, err = s.BeginReconciliation(r.ID, query); err != nil {
						t.Fatal(err)
					}
				}
			}
			w.hooks.sync = func(*os.File) error { return errors.New("injected storage failure") }
			switch stage {
			case "intent":
				_, _, err = s.Begin("parent", p)
			case "dispatch":
				var fresh bool
				fresh, err = s.MarkDispatched(r.ID)
				if fresh {
					t.Fatal("dispatch after failed persistence")
				}
			case "result":
				err = s.Resolve(r.ID, "succeeded", nativeOK)
			case "reconciliation-intent":
				var fresh bool
				fresh, err = s.BeginReconciliation(r.ID, query)
				if fresh {
					t.Fatal("read after failed persistence")
				}
			case "reconciliation-result":
				err = s.ResolveReconciliation(r.ID, nativeOK)
			}
			if !errors.Is(err, ErrStorage) || w.Fence().Err() == nil {
				t.Fatal("failed write not terminal", err)
			}
			w.Close()
			report, _ := Inspect(root, "campaign-1", nil)
			for _, op := range report.Operations {
				if op.Outcome != Unknown {
					t.Fatal("uncommitted result appeared known", op)
				}
			}
		})
	}
}
func TestNativeStopDuringDispatchCommit(t *testing.T) {
	_, w, _, s := nativeWriter(t)
	r, _, err := s.Begin("parent", nativeRequest(t, "invoke"))
	if err != nil {
		t.Fatal(err)
	}
	w.hooks.sync = func(f *os.File) error {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			w.Fence().Stop(ErrClosed)
		}
		return f.Sync()
	}
	if fresh, err := s.MarkDispatched(r.ID); fresh || err == nil {
		t.Fatal("granted dispatch despite stop", fresh, err)
	}
}

func TestNativeCrashLeavesUnknownSteps(t *testing.T) {
	if root := os.Getenv("OPERATOR_NATIVE_CRASH_ROOT"); root != "" {
		m := testManifest(t)
		m.Retention.MaxJournalBytes = 64 << 20
		w, err := Create(root, m)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.ConfigureFreeSpace(0); err != nil {
			t.Fatal(err)
		}
		a, err := NewAttempts(w, 5)
		if err != nil {
			t.Fatal(err)
		}
		observe(t, a, "parent", 6)
		admit(t, a, "parent")
		dispatch(t, a, "parent")
		s := a.NativeSteps()
		r, _, err := s.Begin("parent", nativeRequest(t, "invoke"))
		if err != nil {
			t.Fatal(err)
		}
		if fresh, err := s.MarkDispatched(r.ID); err != nil || !fresh {
			t.Fatal(fresh, err)
		}
		os.Exit(0) // simulate loss without writer Close or result persistence
	}
	root := privateRoot(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestNativeCrashLeavesUnknownSteps$")
	command.Env = append(os.Environ(), "OPERATOR_NATIVE_CRASH_ROOT="+root)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v %s", err, out)
	}
	report, err := Inspect(root, "campaign-1", nil)
	if err != nil || !report.JournalIntact || len(report.Operations) != 2 || len(report.Reservations) != 2 {
		t.Fatal(report, err)
	}
	for _, op := range report.Operations {
		if op.LastRecordedState != Dispatched || op.Outcome != Unknown {
			t.Fatal("crash produced a known result", op)
		}
	}
}
