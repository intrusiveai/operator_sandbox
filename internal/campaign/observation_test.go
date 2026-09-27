//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestObserveActiveCapturedPrefix(t *testing.T) {
	root, w := newWriter(t)
	if _, err := Inspect(root, "campaign-1", nil); !errors.Is(err, ErrActive) {
		t.Fatalf("inspection bypassed writer: %v", err)
	}
	empty, err := Observe(context.Background(), root, "campaign-1", nil)
	if err != nil || empty.VerifiedEvents != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	first := entry(IntentCommitted)
	first.Content = []Content{{"request", "text/plain", []byte("retained input")}}
	appendOK(t, w, first)
	var events []Event
	got, err := Observe(context.Background(), root, "campaign-1", func(e Event) error {
		events = append(events, e)
		// Publish a newer head during the read. It must not enlarge this snapshot.
		appendOK(t, w, entry(Dispatched))
		return nil
	})
	if err != nil || got.VerifiedEvents != 1 || len(events) != 1 || len(got.Operations) != 1 || got.Operations[0].LastRecordedState != IntentCommitted {
		t.Fatalf("captured prefix: %+v %v", got, err)
	}
	next, err := Observe(context.Background(), root, "campaign-1", nil)
	if err != nil || next.VerifiedEvents != 2 || next.VerifiedBytes <= got.VerifiedBytes {
		t.Fatalf("new snapshot: %+v %v", next, err)
	}
	w.Close()
	full, err := Inspect(root, "campaign-1", nil)
	if err != nil || !full.JournalIntact || full.VerifiedBytes != next.VerifiedBytes {
		t.Fatalf("inspection: %+v %v", full, err)
	}
}

func TestObservePendingTailDoesNotClaimIntegrity(t *testing.T) {
	root, w := newWriter(t)
	appendOK(t, w, entry(IntentCommitted))
	w.Close()
	dir := filepath.Join(root, "campaigns/campaign-1")
	segment := filepath.Join(dir, "journals/0000000000000003/events-000001.jsonl")
	f, err := os.OpenFile(segment, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"partial":`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for name, raw := range map[string]string{
		"journal-head.json.pending":                            "incomplete",
		"journals/0000000000000003/content/orphan.bin.pending": "incomplete",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "journals/0000000000000004"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Observe(context.Background(), root, "campaign-1", nil)
	if err != nil || got.VerifiedEvents != 1 {
		t.Fatalf("committed prefix: %+v %v", got, err)
	}
	if _, err := Inspect(root, "campaign-1", nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("strict recovery accepted tail: %v", err)
	}
}

func TestObserveRejectsCommittedCorruptionAndCancellation(t *testing.T) {
	root, w := newWriter(t)
	e := entry(IntentCommitted)
	e.Content = []Content{{"request", "text/plain", []byte("input")}}
	appendOK(t, w, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Observe(ctx, root, "campaign-1", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sentinel := errors.New("visitor stopped")
	if _, err := Observe(context.Background(), root, "campaign-1", func(Event) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	p := filepath.Join(root, "campaigns/campaign-1", contentPath(3, 1, 0))
	if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Observe(context.Background(), root, "campaign-1", nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("accepted changed content: %v", err)
	}
}

func TestObserveConcurrentHeadPublication(t *testing.T) {
	root, w := newWriter(t)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 80; i++ {
			if _, err := w.Append(entry("")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var previous int64
	for {
		got, err := Observe(context.Background(), root, "campaign-1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.VerifiedEvents < previous {
			t.Fatal("head regressed")
		}
		previous = got.VerifiedEvents
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}
