//go:build linux || darwin

package livequalification

import (
	"context"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

func (e *execution) secrets(ctx context.Context, r *credentials.Resolver) error {
	var event credentials.AuditEvent
	// Capture only the production audit's cache signal. Do not retain version
	// strings, locators or values. Every remote read is bracketed by durable checks.
	audited := r.WithAudit(func(v credentials.AuditEvent) error { event = v; return nil })
	resolve := func(check, id string, callCtx context.Context) (credentials.Resolution, error) {
		if err := e.emit(check, "started", "read_only_resolution"); err != nil {
			return credentials.Resolution{}, err
		}
		return audited.Resolve(callCtx, id)
	}
	id := e.q.plan.CredentialID
	first, err := resolve("resolve", id, ctx)
	if err != nil {
		return e.failedCheck("resolve", "resolution_failed")
	}
	if err = e.emit("resolve", "passed", "resolved"); err != nil {
		return err
	}
	if e.q.cacheTTL > 0 {
		second, err := resolve("cache_hit", id, ctx)
		if err != nil || !event.CacheHit || second.Value != first.Value {
			return e.failedCheck("cache_hit", "cache_not_observed")
		}
		if err = e.emit("cache_hit", "passed", "cached_value_reused"); err != nil {
			return err
		}
	} else if err = e.emit("cache_hit", "not_run", "cache_disabled"); err != nil {
		return err
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = resolve("pre_canceled_resolution", id, canceled)
	if err == nil || event.Code != "canceled" {
		return e.failedCheck("pre_canceled_resolution", "cancellation_not_observed")
	}
	if err = e.emit("pre_canceled_resolution", "passed", "canceled_without_value"); err != nil {
		return err
	}
	// Invalidate explicitly to exercise uncached token-sink and backend rereads.
	r.Invalidate(id)
	_, err = resolve("refresh", id, ctx)
	if err != nil || event.CacheHit {
		return e.failedCheck("refresh", "refresh_failed")
	}
	if err = e.emit("refresh", "passed", "uncached_resolution"); err != nil {
		return err
	}
	if e.q.cacheTTL > 0 {
		d := time.Duration(e.q.cacheTTL)*time.Second + 10*time.Millisecond
		deadline, _ := ctx.Deadline()
		if time.Until(deadline) > d+10*time.Second {
			if err = e.emit("cache_expiry", "started", "waiting_for_expiry"); err != nil {
				return err
			}
			if wait(ctx, d) != nil {
				return e.failedCheck("cache_expiry", "canceled")
			}
			if _, err = resolve("cache_expiry", id, ctx); err != nil || event.CacheHit {
				return e.failedCheck("cache_expiry", "refresh_failed")
			}
			if err = e.emit("cache_expiry", "passed", "expired_entry_refreshed"); err != nil {
				return err
			}
		} else if err = e.emit("cache_expiry", "not_run", "insufficient_time_budget"); err != nil {
			return err
		}
	} else if err = e.emit("cache_expiry", "not_run", "cache_disabled"); err != nil {
		return err
	}
	if seconds := e.q.plan.RotationWaitSeconds; seconds > 0 {
		if err = e.emit("rotation", "started", "administrator_rotation_window"); err != nil {
			return err
		}
		if wait(ctx, time.Duration(seconds)*time.Second) != nil {
			return e.failedCheck("rotation", "canceled")
		}
		r.Invalidate(id)
		rotated, err := resolve("rotation", id, ctx)
		if err != nil || rotated.Value == first.Value {
			return e.failedCheck("rotation", "changed_value_not_observed")
		}
		if err = e.emit("rotation", "passed", "changed_value_observed_after_invalidation"); err != nil {
			return err
		}
	} else if err = e.emit("rotation", "not_run", "no_rotation_window"); err != nil {
		return err
	}
	if failure := e.q.plan.FailureCredentialID; failure != "" {
		_, err = resolve("expected_failure", failure, ctx)
		if err == nil || ctx.Err() != nil {
			return e.failedCheck("expected_failure", "expected_backend_failure_not_observed")
		}
		if err = e.emit("expected_failure", "passed", "resolution_failed_without_value"); err != nil {
			return err
		}
	} else if err = e.emit("expected_failure", "not_run", "no_failure_fixture"); err != nil {
		return err
	}
	if err = e.emit("inflight_cancellation", "not_run", "requires_controlled_live_fault"); err != nil {
		return err
	}
	return e.emit("identity_mechanism", "not_run", "requires_runner_identity_attestation")
}
func (e *execution) failedCheck(check, code string) error {
	if err := e.emit(check, "failed", code); err != nil {
		return err
	}
	return ErrCheck
}
