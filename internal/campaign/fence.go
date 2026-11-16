package campaign

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type terminalCause struct{ err error }

// Fence is a one-way execution stop. Err closes admission immediately. Done
// normally triggers termination immediately, independently of journal locks.
// Only a scoped attempt timeout may drain its existing reply for at most 2s.
type Fence struct {
	cause          atomic.Pointer[terminalCause]
	done           chan struct{}
	once           sync.Once
	mu             sync.Mutex
	reportContext  context.Context
	reportDeadline time.Time
	reporting      atomic.Bool
	timer          *time.Timer
}

const TimeoutReportingGrace = 2 * time.Second

func NewFence() *Fence                 { return &Fence{done: make(chan struct{})} }
func (f *Fence) Done() <-chan struct{} { return f.done }
func (f *Fence) Err() error {
	if c := f.cause.Load(); c != nil {
		return c.err
	}
	return nil
}

// ArmTimeoutReply is host-only and scoped to the current serialized attempt.
// A shorter request/campaign deadline is never extended. Cancellation, storage,
// protocol and independent product failures bypass this reporting grace.
func (f *Fence) ArmTimeoutReply(execution context.Context, replyDeadline time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err() == nil {
		f.reportContext = execution
		f.reportDeadline = replyDeadline
	}
}
func (f *Fence) Reporting() bool { return f.reporting.Load() }
func (f *Fence) EndTimeoutReply() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reportContext = nil
	if f.timer != nil {
		f.timer.Stop()
	}
	if f.reporting.Swap(false) {
		f.once.Do(func() { close(f.done) })
	}
}
func (f *Fence) Stop(err error) {
	if err == nil {
		err = ErrClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cause.CompareAndSwap(nil, &terminalCause{err})
	select {
	case <-f.done:
		return
	default:
	}
	eligible := (err == ErrExecutionFailure || err == ErrUnknownOutcome) && f.reportContext != nil && deadlineReached(f.reportContext) && time.Now().Before(f.reportDeadline)
	if eligible {
		if !f.reporting.Swap(true) {
			until := minDuration(TimeoutReportingGrace, time.Until(f.reportDeadline))
			f.timer = time.AfterFunc(until, f.EndTimeoutReply)
		}
		return
	}
	f.reporting.Store(false)
	if f.timer != nil {
		f.timer.Stop()
	}
	f.once.Do(func() { close(f.done) })
}
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Equal parent/child deadlines can fire in either order. A native child may
// finish before its parent's timer goroutine marks Done, although the absolute
// execution deadline has already elapsed. Do not convert that into cancellation.
func deadlineReached(ctx context.Context) bool {
	if ctx.Err() == context.DeadlineExceeded {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ctx.Err() == nil && ok && !time.Now().Before(deadline)
}
