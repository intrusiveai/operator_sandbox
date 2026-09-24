package campaign

import "sync/atomic"

type terminalCause struct{ err error }

// Fence is a one-way execution stop signal, independent of worker/journal locks.
// Its observer must perform termination without waiting for a journal write.
type Fence struct {
	cause atomic.Pointer[terminalCause]
	done  chan struct{}
}

func NewFence() *Fence                 { return &Fence{done: make(chan struct{})} }
func (f *Fence) Done() <-chan struct{} { return f.done }
func (f *Fence) Err() error {
	if c := f.cause.Load(); c != nil {
		return c.err
	}
	return nil
}
func (f *Fence) Stop(err error) {
	if err == nil {
		err = ErrClosed
	}
	if f.cause.CompareAndSwap(nil, &terminalCause{err}) {
		close(f.done)
	}
}
