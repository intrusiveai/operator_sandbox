//go:build linux || darwin

package startrequest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObservationWaitsForPublicationWithoutAcceptingAbandonedPending(t *testing.T) {
	r := requestFixture(t)
	s, err := Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(r.StateRoot, "starts", r.Selection.StartRequestID, "owner.json.pending")
	if err := os.WriteFile(file, []byte("in progress"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { time.Sleep(30 * time.Millisecond); done <- os.Remove(file) }()
	observed, err := Observe(context.Background(), r.StateRoot, r.Selection.StartRequestID)
	if err != nil || observed.Digest != s.Digest {
		t.Fatal(observed, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("abandoned"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Observe(context.Background(), r.StateRoot, r.Selection.StartRequestID); err == nil {
		t.Fatal("abandoned pending accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Observe(ctx, r.StateRoot, r.Selection.StartRequestID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
