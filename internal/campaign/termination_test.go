//go:build linux || darwin

package campaign

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func stopFixture(t *testing.T) (string, *Writer, DockerBinding, StopRecord) {
	t.Helper()
	root, w := newWriter(t)
	b := fixtureBinding(w)
	if err := w.SaveDockerBinding(b); err != nil {
		t.Fatal(err)
	}
	return root, w, b, StopObservation(b, strings.Repeat("1", 32), "user-request", nil)
}

func TestEmergencyRecordsDoNotWaitForJournal(t *testing.T) {
	root, w, b, intent := stopFixture(t)
	w.mu.Lock()
	defer w.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- SaveStopRecord(root, b, intent) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop record waited on journal")
	}
	got, err := ReadStopIntent(root, b.CampaignID)
	if err != nil || got == nil || got.RequestID != intent.RequestID {
		t.Fatal(got, err)
	}
	// A different retry preserves the original terminal decision.
	retry := StopObservation(b, strings.Repeat("2", 32), "host-terminal-fence", nil)
	if err := SaveStopRecord(root, b, retry); err != nil {
		t.Fatal(err)
	}
	got, err = ReadStopIntent(root, b.CampaignID)
	if err != nil || got.RequestID != intent.RequestID {
		t.Fatal(got, err)
	}
	intent.Reason = "changed"
	if err := SaveStopRecord(root, b, intent); !errors.Is(err, ErrInvalid) {
		t.Fatal("changed request key accepted", err)
	}
	result := StopObservation(b, retry.RequestID, retry.Reason, &TerminationOutcome{true, true, "exited", "confirmed_stopped"})
	if err := SaveStopRecord(root, b, result); err != nil {
		t.Fatal(err)
	}
	observed, err := ReadTerminationResult(root, b.CampaignID)
	if err != nil || observed == nil || !observed.Outcome.Confirmed {
		t.Fatal(observed, err)
	}
	// A later unavailable daemon is a distinct observation, never restored admission.
	result.RequestID = strings.Repeat("3", 32)
	result.Outcome = &TerminationOutcome{false, false, "unknown", "docker_unavailable"}
	if err := SaveStopRecord(root, b, result); err != nil {
		t.Fatal(err)
	}
	observed, err = ReadTerminationResult(root, b.CampaignID)
	if err != nil || observed.Outcome.Confirmed {
		t.Fatal(observed, err)
	}
	if got, err := ReadStopIntent(root, b.CampaignID); err != nil || got == nil {
		t.Fatal("result removed stop intent", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "campaigns", b.CampaignID, "termination-results.json"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := decodeResults(raw, b)
	if err != nil || len(all) != 2 || !all[0].Record.Outcome.Confirmed {
		t.Fatal("prior observation lost", all, err)
	}
}

func TestEmergencySegmentCapacityAndOrphanResult(t *testing.T) {
	root, _, b, intent := stopFixture(t)
	result := StopObservation(b, intent.RequestID, intent.Reason, &TerminationOutcome{false, true, "unknown", "docker_unavailable"})
	if err := SaveStopRecord(root, b, result); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStopIntent(root, b.CampaignID); !errors.Is(err, ErrClosed) {
		t.Fatal("orphan result reopened admission", err)
	}
	raw, err := encode(result, TerminationRecordLimit)
	if err != nil {
		t.Fatal(err)
	}
	entry := stopEnvelope{result, contracts.RawDigest(raw)}
	records := make([]stopEnvelope, MaxTerminationResults)
	for i := range records {
		records[i] = entry
	}
	raw, err = encodeResults(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "campaigns", b.CampaignID, "termination-results.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveStopRecord(root, b, result); !errors.Is(err, ErrQuota) {
		t.Fatal("unbounded emergency results", err)
	}
	if got, err := ReadTerminationResult(root, b.CampaignID); err != nil || got == nil {
		t.Fatal("quota destroyed prior evidence", err)
	}
}

func TestStopRecordFailuresRemainExplicit(t *testing.T) {
	for _, kind := range []string{"content corruption", "pending", "symlink", "record lock", "journal corruption"} {
		t.Run(kind, func(t *testing.T) {
			root, w, b, intent := stopFixture(t)
			dir := filepath.Join(root, "campaigns", b.CampaignID)
			if got, err := ReadStopIntent(root, b.CampaignID); err != nil || got != nil {
				t.Fatal(got, err)
			}
			switch kind {
			case "content corruption":
				if err := SaveStopRecord(root, b, intent); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(dir, "termination-intent.json")
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.Replace(raw, []byte("user-request"), []byte("user-changed"), 1)
				if err := os.WriteFile(p, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := ReadStopIntent(root, b.CampaignID); err == nil {
					t.Fatal("corruption not detected")
				}
			case "pending":
				if err := os.WriteFile(filepath.Join(dir, "termination-intent.json.pending"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := ReadStopIntent(root, b.CampaignID); err == nil {
					t.Fatal("pending publication reopened admission")
				}
				if err := SaveStopRecord(root, b, intent); err == nil {
					t.Fatal("overwrote uncertain publication")
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, "termination-intent.json")); err != nil {
					t.Fatal(err)
				}
				if err := SaveStopRecord(root, b, intent); err == nil {
					t.Fatal("followed link")
				}
				raw, _ := os.ReadFile(outside)
				if string(raw) != "keep" {
					t.Fatal("outside file changed")
				}
			case "record lock":
				f, err := os.OpenFile(filepath.Join(dir, "termination.lock"), os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				if err := SaveStopRecord(root, b, intent); !errors.Is(err, ErrActive) {
					t.Fatal(err)
				}
			case "journal corruption":
				if err := os.WriteFile(filepath.Join(dir, "journal-head.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
				w.Fence().Stop(ErrCorrupt)
				if err := SaveStopRecord(root, b, intent); err != nil {
					t.Fatal("journal damage blocked stop record", err)
				}
			}
		})
	}
}
