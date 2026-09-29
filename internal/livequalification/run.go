//go:build linux || darwin

package livequalification

import (
	"context"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

var ErrCheck = errors.New("qualification checks failed or were interrupted")

type execution struct {
	q      *Prepared
	id     Identity
	sink   Sink
	failed bool
}

func (e *execution) emit(check, status, code string) error {
	if status == "failed" || status == "uncertain" {
		e.failed = true
	}
	return e.sink(e.q.record(e.id, check, status, code))
}
func (q *Prepared) Run(ctx context.Context, id Identity, sink Sink) error {
	return q.run(ctx, id, sink, nil)
}
func (q *Prepared) run(ctx context.Context, id Identity, sink Sink, factories map[string]credentials.BackendFactory) error {
	if sink == nil {
		return ErrCheck
	}
	e := &execution{q: q, id: id, sink: sink}
	if err := e.emit("run", "started", "explicit_live_opt_in"); err != nil {
		return ErrCheck
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(q.plan.TimeoutSeconds)*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		_ = e.emit("run", "failed", "canceled")
		return ErrCheck
	}
	resolver, err := credentials.New(q.config, factories)
	if err != nil {
		_ = e.emit("run", "failed", "resolver_initialization_failed")
		return ErrCheck
	}
	defer resolver.Close()
	if q.plan.Kind == "provider" {
		audited := resolver.WithAudit(func(event credentials.AuditEvent) error {
			record := e.q.record(e.id, "provider_credential", "observed", event.Code)
			record.Backend = event.Backend
			record.CacheHit = &event.CacheHit
			return e.sink(record)
		})
		client, initErr := modelprovider.New(ctx, q.profile, audited)
		if initErr != nil {
			return e.failedCheck("run", "provider_initialization_failed")
		}
		defer client.Close()
		err = e.provider(ctx, client)
	} else {
		err = e.secrets(ctx, resolver)
	}
	if err != nil {
		return ErrCheck
	}
	if err = e.emit("run", "completed", "selected_checks_finished"); err != nil || e.failed {
		return ErrCheck
	}
	return nil
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
