//go:build linux || darwin

package campaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func attemptWriter(t *testing.T, maximum int64) (string, *Writer, *Attempts) {
	t.Helper()
	root := privateRoot(t)
	m := testManifest(t)
	m.Retention.MaxJournalBytes = 64 << 20
	var limits map[string]json.RawMessage
	json.Unmarshal(m.RemainingLimits, &limits)
	limits["attempt_admissions"] = json.RawMessage(fmt.Sprint(maximum))
	m.RemainingLimits, _ = json.Marshal(limits)
	w, err := Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	a, err := NewAttempts(w, 5)
	if err != nil {
		t.Fatal(err)
	}
	return root, w, a
}

func attemptInput(id string, index int64) AttemptInput {
	return AttemptInput{"campaign-1", "worker-1", 3, []byte(fmt.Sprintf(`{"request_id":%q,"attempt_id":%q,"attempt_index":%d,"invocation":{"operation_id":"tool-1"}}`, id, "attempt-"+id, index))}
}
func completion(outcome string) AttemptCompletion {
	return AttemptCompletion{Outcome: outcome, Result: []byte("{\n\"outcome\": \"" + outcome + "\"\n}"), NativeResult: []byte{0, 255, 27}, Receipts: json.RawMessage(`{"receipt_id":"receipt-1"}`)}
}
func observe(t *testing.T, a *Attempts, id string, index int64) SavedAttempt {
	t.Helper()
	r, replay, err := a.Observe(attemptInput(id, index))
	if err != nil || replay {
		t.Fatal(r, replay, err)
	}
	return r
}
func admit(t *testing.T, a *Attempts, id string) {
	t.Helper()
	fresh, err := a.Admit(id, []byte(`{"native_operation":"invoke"}`))
	if err != nil || !fresh {
		t.Fatal(fresh, err)
	}
}
func dispatch(t *testing.T, a *Attempts, id string) {
	t.Helper()
	fresh, err := a.MarkDispatched(id)
	if err != nil || !fresh {
		t.Fatal(fresh, err)
	}
}

func TestDurableAttemptsRejectReplayRestore(t *testing.T) {
	root, w, a := attemptWriter(t, 2)
	observe(t, a, "rejected", 7)
	if err := a.Resolve("rejected", completion("rejected")); err != nil {
		t.Fatal(err)
	}
	if status := a.Status(); status.HighWatermark != 7 || status.Admissions != 0 {
		t.Fatal(status)
	}
	observe(t, a, "executed", 8)
	admit(t, a, "executed")
	dispatch(t, a, "executed")
	if fresh, err := a.MarkDispatched("executed"); err != nil || fresh {
		t.Fatal("duplicate dispatch", fresh, err)
	}
	result := completion("succeeded")
	if err := a.Resolve("executed", result); err != nil {
		t.Fatal(err)
	}
	before := w.head.Sequence
	if err := a.Resolve("executed", result); err != nil || w.head.Sequence != before {
		t.Fatal("duplicate completion wrote again", err)
	}
	if fresh, err := a.Admit("executed", []byte(`{ "native_operation": "invoke" }`)); err != nil || fresh {
		t.Fatal("duplicate admission", fresh, err)
	}
	if fresh, err := a.Admit("executed", []byte(`{"native_operation":"different"}`)); err == nil || fresh {
		t.Fatal("changed plan accepted")
	}
	target := w.manifest.Target
	target.SessionID = "session-2"
	target.WorkerInstanceID = "worker-2"
	if err := a.Rebind(4, target); err != nil {
		t.Fatal(err)
	}
	in := attemptInput("executed", 8)
	in.RunRevision = 4
	in.WorkerInstanceID = "different-worker"
	in.Body = bytes.Replace(in.Body, []byte(`"attempt_index":8`), []byte(`"attempt_index":8.0`), 1)
	r, replay, err := a.Observe(in)
	if err != nil || !replay || r.Target.SessionID != "session-1" || r.WorkerInstanceID != "worker-1" {
		t.Fatal("restore/attribution changed identity", r, replay, err)
	}
	rawRecord, raw, err := a.Lookup("executed")
	if err != nil || !bytes.Equal(raw, result.Result) {
		t.Fatal("saved bytes changed", err)
	}
	rawRecord.Result.Digest = "mutated"
	if _, _, err := a.Lookup("executed"); err != nil {
		t.Fatal("lookup exposed mutable state", err)
	}
	in.Body = bytes.Replace(in.Body, []byte("tool-1"), []byte("tool-2"), 1)
	if _, _, err := a.Observe(in); err == nil {
		t.Fatal("mutated command reused key")
	}
	in = attemptInput("next", 10)
	in.RunRevision = 4
	if _, replay, err := a.Observe(in); err != nil || replay {
		t.Fatal(err)
	}
	admit(t, a, "next")
	dispatch(t, a, "next")
	if err := a.Resolve("next", completion("failed")); err != nil {
		t.Fatal(err)
	}
	status := a.Status()
	if status.HighWatermark != 10 || status.Admissions != 2 || status.Submissions != 3 || status.RunRevision != 4 {
		t.Fatal(status)
	}
	if _, err := NewAttempts(w, 0); err == nil {
		t.Fatal("created second ledger")
	}
	w.Close()
	var observedIndexes []int64
	report, err := Inspect(root, "campaign-1", func(e Event) error {
		if e.Kind == "attempt.observed" {
			var m struct {
				High int64 `json:"attempt_index_high_watermark"`
			}
			json.Unmarshal(e.Metadata, &m)
			observedIndexes = append(observedIndexes, m.High)
		}
		return nil
	})
	if err != nil || !report.JournalIntact || len(report.Reservations) != 0 || len(report.Operations) != 3 {
		t.Fatal(report, err)
	}
	if fmt.Sprint(observedIndexes) != "[7 8 10]" {
		t.Fatal(observedIndexes)
	}
}

func TestAttemptAdmissionBoundary(t *testing.T) {
	for _, maximum := range []int64{0, 1} {
		t.Run(fmt.Sprint(maximum), func(t *testing.T) {
			_, w, a := attemptWriter(t, maximum)
			if maximum == 1 {
				observe(t, a, "first", 6)
				admit(t, a, "first")
				dispatch(t, a, "first")
				if err := a.Resolve("first", completion("failed")); err != nil {
					t.Fatal(err)
				}
			}
			observe(t, a, "over", 8)
			if fresh, err := a.Admit("over", []byte(`{}`)); fresh || !errors.Is(err, contracts.ErrLimit) {
				t.Fatal(fresh, err)
			}
			if err := a.Resolve("over", completion("rejected")); err != nil {
				t.Fatal(err)
			}
			if a.Status().Admissions != maximum || a.Status().HighWatermark != 8 || w.reservations.total != 0 {
				t.Fatal(a.Status())
			}
		})
	}
}

func TestAttemptBookkeepingBeforeTacticalValidation(t *testing.T) {
	_, w, a := attemptWriter(t, 100)
	in := attemptInput("bad-tactic", 6)
	in.Body = []byte(`{"request_id":"bad-tactic","attempt_id":"attempt-bad","attempt_index":6,"payload":"invalid"}`)
	if _, _, err := a.Observe(in); err != nil {
		t.Fatal("tactical validation happened before observation", err)
	}
	if a.Status().HighWatermark != 6 || a.Status().Admissions != 0 {
		t.Fatal(a.Status())
	}
	target := w.manifest.Target
	target.SessionID = "session-2"
	if err := a.Rebind(4, target); !errors.Is(err, ErrActive) {
		t.Fatal("restore discarded pending attempt", err)
	}
	for _, body := range []string{`[]`, `{"request_id":"dup","request_id":"dup"}`, `{"request_id":"fractional","attempt_id":"fractional","attempt_index":7.00000000000000001}`} {
		in.Body = []byte(body)
		if _, _, err := a.Observe(in); err == nil {
			t.Fatal("accepted malformed bookkeeping")
		}
	}
	in = attemptInput("other-campaign", 7)
	in.CampaignID = "other"
	if _, _, err := a.Observe(in); err == nil {
		t.Fatal("cross-campaign accepted")
	}
	if a.Status().HighWatermark != 6 || w.fence.Err() != nil {
		t.Fatal(a.Status(), w.fence.Err())
	}
}

func TestAdmissionFailureNeverGrantsDispatch(t *testing.T) {
	for _, stage := range []string{"observation", "admission", "dispatch", "result"} {
		t.Run(stage, func(t *testing.T) {
			root, w, a := attemptWriter(t, 2)
			if stage != "observation" {
				observe(t, a, "op", 6)
			}
			if stage == "dispatch" || stage == "result" {
				admit(t, a, "op")
			}
			if stage == "result" {
				dispatch(t, a, "op")
			}
			w.hooks.sync = func(*os.File) error { return syscall.ENOSPC }
			var err error
			switch stage {
			case "observation":
				_, _, err = a.Observe(attemptInput("op", 6))
			case "admission":
				var fresh bool
				fresh, err = a.Admit("op", []byte(`{}`))
				if fresh {
					t.Fatal("admission on failed commit")
				}
			case "dispatch":
				var fresh bool
				fresh, err = a.MarkDispatched("op")
				if fresh {
					t.Fatal("dispatch on failed commit")
				}
			case "result":
				err = a.Resolve("op", completion("succeeded"))
			}
			if !errors.Is(err, ErrStorage) {
				t.Fatal(err)
			}
			select {
			case <-w.Fence().Done():
			default:
				t.Fatal("missing independent failure signal")
			}
			if _, _, err := a.Observe(attemptInput("later", 7)); err == nil {
				t.Fatal("continued after storage failure")
			}
			w.Close()
			report, _ := Inspect(root, "campaign-1", nil)
			for _, op := range report.Operations {
				if op.Outcome != Unknown {
					t.Fatal("uncertain effect became known", op)
				}
			}
		})
	}
}

func TestUnknownSignalsBeforeBlockedJournal(t *testing.T) {
	root, w, a := attemptWriter(t, 2)
	observe(t, a, "op", 6)
	admit(t, a, "op")
	dispatch(t, a, "op")
	w.mu.Lock()
	a.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.Resolve("op", completion("unknown")) }()
	select {
	case <-w.Fence().Done():
	case <-time.After(2 * time.Second):
		a.mu.Unlock()
		w.mu.Unlock()
		t.Fatal("terminal notification waited on journal")
	}
	a.mu.Unlock()
	w.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(w.Fence().Err(), ErrUnknownOutcome) || a.Status().Admissions != 1 {
		t.Fatal(a.Status(), w.Fence().Err())
	}
	if _, _, err := a.Observe(attemptInput("later", 7)); err == nil {
		t.Fatal("unknown reopened execution")
	}
	if _, replay, err := a.Observe(attemptInput("op", 6)); err != nil || !replay {
		t.Fatal("lost known duplicate", err)
	}
	w.Close()
	r, err := Inspect(root, "campaign-1", nil)
	if err != nil || len(r.Reservations) != 0 || len(r.Operations) != 1 || r.Operations[0].Outcome != Unknown {
		t.Fatal(r, err)
	}
}

func TestStopDuringDispatchCommit(t *testing.T) {
	_, w, a := attemptWriter(t, 1)
	observe(t, a, "op", 6)
	admit(t, a, "op")
	w.hooks.sync = func(f *os.File) error {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			w.Fence().Stop(ErrClosed)
		}
		return f.Sync()
	}
	if fresh, err := a.MarkDispatched("op"); fresh || err == nil {
		t.Fatal("granted dispatch after stop", fresh, err)
	}
}

func TestStoredResultCorruptionFencesExecution(t *testing.T) {
	root, _, a := attemptWriter(t, 2)
	observe(t, a, "op", 6)
	if err := a.Resolve("op", completion("rejected")); err != nil {
		t.Fatal(err)
	}
	r, _, err := a.Lookup("op")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "campaigns/campaign-1", r.Result.Path), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Lookup("op"); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if !a.Status().Closed {
		t.Fatal("corrupt result kept execution open")
	}
}

func TestReservationBudgetAndRecovery(t *testing.T) {
	root, w := newWriter(t)
	if _, err := w.AppendReserving(entry(""), "r", 2<<20); err == nil {
		t.Fatal("reservation without space policy")
	}
	if err := w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AppendReserving(entry(""), "r", 2<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AppendReserving(entry(""), "r", 2<<20); err == nil {
		t.Fatal("duplicate reservation")
	}
	e := entry("")
	e.Content = []Content{{"result", "application/octet-stream", make([]byte, 1<<20)}}
	if _, err := w.AppendReserved(e, "r", false); err != nil {
		t.Fatal(err)
	}
	remaining := w.reservations.remaining["r"]
	if remaining <= 0 || remaining >= 1<<20 {
		t.Fatal("cost not charged", remaining)
	}
	w.Close()
	report, err := Inspect(root, "campaign-1", nil)
	if err != nil || len(report.Reservations) != 1 || report.Reservations[0].RemainingBytes != remaining {
		t.Fatal(report.Reservations, err)
	}
}

func TestReservationCannotBeStolenOrForged(t *testing.T) {
	_, w := newWriter(t)
	if err := w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AppendReserving(entry(""), "held", 15<<20); err != nil {
		t.Fatal(err)
	}
	bad := entry("")
	bad.Metadata = json.RawMessage(`{"journal_reservation":{"id":"held","action":"consume","bytes":0,"release":true}}`)
	if _, err := w.Append(bad); err == nil {
		t.Fatal("forged reservation use")
	}
	e := entry("")
	e.Content = []Content{{"other", "text/plain", make([]byte, 2<<20)}}
	if _, err := w.Append(e); !errors.Is(err, ErrQuota) {
		t.Fatal("ordinary append stole capacity", err)
	}
	if w.reservations.remaining["held"] != 15<<20 {
		t.Fatal("failed append changed reservation")
	}
}

func TestFilesystemPressureFencesBeforeReservation(t *testing.T) {
	root, w := newWriter(t)
	if err := w.ConfigureFreeSpace(1024); err != nil {
		t.Fatal(err)
	}
	w.hooks.available = func(*os.Root) (int64, error) { return 1024, nil }
	if _, err := w.AppendReserving(entry(""), "r", 1024); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	select {
	case <-w.Fence().Done():
	default:
		t.Fatal("pressure did not signal stop")
	}
	w.Close()
	r, err := Inspect(root, "campaign-1", nil)
	if err != nil || len(r.Reservations) != 0 || r.VerifiedEvents != 1 {
		t.Fatal(r, err)
	}
}

func TestMaximumAttemptResultFitsReservedCapacity(t *testing.T) {
	_, w, a := attemptWriter(t, 1)
	observe(t, a, "op", 6)
	plan := []byte(`{"plan":"` + strings.Repeat("x", MaxContentBytes-11) + `"}`)
	if len(plan) != MaxContentBytes {
		t.Fatal(len(plan))
	}
	if fresh, err := a.Admit("op", plan); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	dispatch(t, a, "op")
	c := completion("succeeded")
	c.Result = plan
	c.NativeResult = make([]byte, MaxContentBytes)
	if err := a.Resolve("op", c); err != nil {
		t.Fatal(err)
	}
	if w.reservations.total != 0 {
		t.Fatal("reservation leaked")
	}
}

func TestConcurrentDuplicatesCannotDispatchTwice(t *testing.T) {
	_, _, a := attemptWriter(t, 1)
	run := func(fn func() (bool, error)) int64 {
		var fresh atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Go(func() {
				ok, err := fn()
				if err != nil {
					t.Error(err)
				}
				if ok {
					fresh.Add(1)
				}
			})
		}
		wg.Wait()
		return fresh.Load()
	}
	if count := run(func() (bool, error) { _, replay, err := a.Observe(attemptInput("op", 6)); return !replay, err }); count != 1 {
		t.Fatal("observed twice", count)
	}
	if count := run(func() (bool, error) { return a.Admit("op", []byte(`{}`)) }); count != 1 {
		t.Fatal("charged twice", count)
	}
	if count := run(func() (bool, error) { return a.MarkDispatched("op") }); count != 1 {
		t.Fatal("dispatched twice", count)
	}
	if status := a.Status(); status.Admissions != 1 || status.Submissions != 1 {
		t.Fatal(status)
	}
}

func TestAttemptCrashRetainsChargesAndReservation(t *testing.T) {
	root := privateRoot(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestAttemptCrashHelper$")
	cmd.Env = append(os.Environ(), "OPERATOR_TEST_ATTEMPT_CRASH_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var high, admissions int64
	r, err := Inspect(root, "campaign-1", func(e Event) error {
		var counters struct {
			High       int64 `json:"attempt_index_high_watermark"`
			Admissions int64 `json:"attempt_admissions"`
		}
		json.Unmarshal(e.Metadata, &counters)
		if e.Kind == "attempt.dispatched" {
			high, admissions = counters.High, counters.Admissions
		}
		return nil
	})
	if err != nil || !r.JournalIntact || len(r.Reservations) != 1 || r.Reservations[0].RemainingBytes <= 0 || len(r.Operations) != 1 || r.Operations[0].Outcome != Unknown || high != 9 || admissions != 1 {
		t.Fatal(r, high, admissions, err)
	}
}

func TestAttemptCrashHelper(t *testing.T) {
	root := os.Getenv("OPERATOR_TEST_ATTEMPT_CRASH_ROOT")
	if root == "" {
		return
	}
	w, err := Create(root, testManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	a, err := NewAttempts(w, 5)
	if err != nil {
		t.Fatal(err)
	}
	observe(t, a, "crash", 9)
	admit(t, a, "crash")
	dispatch(t, a, "crash")
	os.Exit(0)
}
