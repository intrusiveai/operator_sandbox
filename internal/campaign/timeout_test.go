package campaign

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimeoutReportingStopsAdmissionBeforeBoundedDrain(t *testing.T) {
	for _, mode := range []string{"published", "timer", "independent", "cancel", "no-extension"} {
		t.Run(mode, func(t *testing.T) {
			f := NewFence()
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			deadline := time.Now().Add(50 * time.Millisecond)
			if mode == "no-extension" {
				deadline = time.Now().Add(-time.Second)
			}
			if mode == "cancel" {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			f.ArmTimeoutReply(ctx, deadline)
			f.Stop(ErrExecutionFailure)
			if f.Err() != ErrExecutionFailure {
				t.Fatal("admission left open")
			}
			if mode == "no-extension" || mode == "cancel" {
				select {
				case <-f.Done():
				default:
					t.Fatal("unexpected reporting grace")
				}
				return
			}
			if !f.Reporting() {
				t.Fatal("no grace")
			}
			select {
			case <-f.Done():
				t.Fatal("reply cannot drain")
			default:
			}
			if mode == "published" {
				f.EndTimeoutReply()
			}
			if mode == "independent" {
				f.Stop(errors.New("storage failed"))
			}
			select {
			case <-f.Done():
			case <-time.After(time.Second):
				t.Fatal("cleanup unbounded")
			}
			if f.Reporting() || f.Err() != ErrExecutionFailure {
				t.Fatal("primary failure changed")
			}
		})
	}
}

// A child can observe an expired absolute deadline before the parent's timer
// callback updates Err. This models that ordering without scheduling luck.
type delayedDeadline struct {
	context.Context
	at time.Time
}

func (c delayedDeadline) Deadline() (time.Time, bool) { return c.at, true }
func TestTimeoutReportingRecognizesElapsedParentDeadline(t *testing.T) {
	f := NewFence()
	ctx := delayedDeadline{context.Background(), time.Now().Add(-time.Millisecond)}
	f.ArmTimeoutReply(ctx, time.Now().Add(time.Second))
	f.Stop(ErrUnknownOutcome)
	if !f.Reporting() || f.Err() != ErrUnknownOutcome {
		t.Fatal("native deadline lost", f.Err())
	}
	select {
	case <-f.Done():
		t.Fatal("normal reporting window bypassed")
	default:
	}
	f.EndTimeoutReply()
	select {
	case <-f.Done():
	default:
		t.Fatal("reporting did not end")
	}
}
