//go:build linux || darwin

package modelprovider

import (
	"context"
	"github.com/intrusiveai/operator_sandbox/internal/operationpolicy"
	"time"
)

type deadlineKey struct{}
type deadlineFact struct {
	at     time.Time
	source string
}

// WithDeadlineSource labels the existing effective deadline; it never changes it.
// Only host-selected sources are accepted. A subsequently shortened deadline is
// attributed to the caller rather than to a stale annotation.
func WithDeadlineSource(ctx context.Context, source string) context.Context {
	at, ok := ctx.Deadline()
	if !ok || !validDeadlineSource(source) {
		return ctx
	}
	return context.WithValue(ctx, deadlineKey{}, deadlineFact{at, source})
}
func validDeadlineSource(source string) bool {
	switch source {
	case "caller", "campaign", "request", "configured-operation", "host-default":
		return true
	}
	return false
}
func requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(operationpolicy.DefaultModelTimeoutMS)*time.Millisecond)
	return WithDeadlineSource(ctx, "host-default"), cancel
}
func (d Diagnostic) WithDeadline(ctx context.Context) Diagnostic {
	if at, ok := ctx.Deadline(); ok {
		d.Deadline = at.UTC().Format(time.RFC3339Nano)
		d.DeadlineSource = "caller"
		if fact, ok := ctx.Value(deadlineKey{}).(deadlineFact); ok && fact.at.Equal(at) {
			d.DeadlineSource = fact.source
		}
	}
	return d
}
