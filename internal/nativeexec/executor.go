package nativeexec

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type Outcome string

const (
	Succeeded     Outcome = "succeeded"
	Failed        Outcome = "failed"
	Unknown       Outcome = "unknown"
	NotDispatched Outcome = "not_dispatched"
)

var (
	ErrAdapter = errors.New("native operation lacks valid installed adapter handling")
	ErrPending = errors.New("native operation has no committed outcome; replay cannot dispatch")
	ErrFailed  = errors.New("native execution failed; campaign execution is closed")
	ErrUnknown = errors.New("native execution outcome is unknown; campaign execution is closed")
)

// Adapter is trusted installed code, not guest-provided callbacks. Authorize
// verifies that this exact step belongs to the admitted plan and current policy.
// Interpret verifies operation-specific receipts and semantics. HTTP 200 alone
// is never sufficient. Neither method may contact the target or choose tactics.
type Adapter interface {
	Authorize(interceptor.PreparedOperation) error
	Interpret(interceptor.PreparedOperation, interceptor.Response) (Outcome, error)
}

type Executor struct {
	steps   *campaign.NativeSteps
	guard   *Guard
	adapter Adapter
}

func New(steps *campaign.NativeSteps, guard *Guard, adapter Adapter) (*Executor, error) {
	if steps == nil || guard == nil || adapter == nil {
		return nil, ErrAdapter
	}
	if err := steps.VerifyRuntimeBinding(guard.docker); err != nil {
		return nil, err
	}
	return &Executor{steps, guard, adapter}, nil
}

type Result struct {
	Step   campaign.NativeStep
	Replay bool
	// Native is host-only. A typed adapter must project permitted fields before
	// constructing any harness result. It is nil for an untrusted/missing reply.
	Native *interceptor.Response
}

func resultError(outcome string) error {
	switch Outcome(outcome) {
	case Succeeded:
		return nil
	case Failed, NotDispatched:
		return ErrFailed
	case Unknown:
		return ErrUnknown
	}
	return ErrPending
}
func (e *Executor) saved(id string, replay bool) (Result, error) {
	step, _, raw, err := e.steps.Lookup(id)
	if err != nil {
		return Result{}, err
	}
	result := Result{Step: step, Replay: replay}
	if len(raw) > 0 {
		r, err := interceptor.ParseResponse(raw)
		if err != nil {
			e.steps.Fence().Stop(err)
			return Result{}, err
		}
		result.Native = &r
	}
	return result, resultError(step.Outcome)
}
func (e *Executor) finish(id string, outcome Outcome, raw []byte) (Result, error) {
	if err := e.steps.Resolve(id, string(outcome), raw); err != nil {
		return Result{}, err
	}
	return e.saved(id, false)
}
func (e *Executor) executionContext(parent context.Context, p interceptor.PreparedOperation) (context.Context, context.CancelFunc) {
	deadline := p.Request().Deadline
	if cap := time.Now().Add(interceptor.MaxOperationTimeout); deadline.After(cap) {
		deadline = cap
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	go func() {
		select {
		case <-e.steps.Fence().Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// Execute dispatches a frozen, authorized native step once. Every possible effect
// is preceded by a committed marker; every returned result by a committed result.
func (e *Executor) Execute(ctx context.Context, parent string, p interceptor.PreparedOperation) (Result, error) {
	step, replay, err := e.steps.Begin(parent, p)
	if err != nil {
		return Result{}, err
	}
	if replay {
		return e.saved(step.ID, true)
	}
	ctx, cancel := e.executionContext(ctx, p)
	defer cancel()
	if e.adapter.Authorize(p) != nil {
		return e.finish(step.ID, NotDispatched, nil)
	}
	proof, err := e.guard.check(ctx, p, false)
	if err != nil {
		return e.finish(step.ID, NotDispatched, nil)
	}
	if err = e.steps.RecordPreflight(step.ID, proof); err != nil {
		return Result{}, err
	}
	if ctx.Err() != nil || e.steps.Fence().Err() != nil {
		return e.finish(step.ID, NotDispatched, nil)
	}
	fresh, err := e.steps.MarkDispatched(step.ID)
	if err != nil {
		return Result{}, err
	}
	if !fresh {
		return e.saved(step.ID, true)
	}
	// A stop may arrive during the journal commit. This synchronous check and the
	// cancelable call context complement the independent Docker termination observer.
	if ctx.Err() != nil || e.steps.Fence().Err() != nil {
		return e.finish(step.ID, NotDispatched, nil)
	}
	r, err := e.guard.peer.Execute(ctx, p)
	if err != nil {
		var call *interceptor.CallError
		if errors.As(err, &call) && !call.Uncertain {
			return e.finish(step.ID, NotDispatched, nil)
		}
		return e.finish(step.ID, Unknown, nil)
	}
	raw := r.Bytes()
	verified, err := interceptor.ParseResponse(raw)
	if err != nil {
		return e.finish(step.ID, Unknown, nil)
	}
	// These native responses cannot prove a known effect, regardless of adapter
	// interpretation. Interceptor marks 5xx as unknown in its durable store.
	if verified.Status == 202 || verified.Status >= 500 || strings.Contains(verified.Code(), "unknown") {
		return e.finish(step.ID, Unknown, raw)
	}
	outcome, err := e.adapter.Interpret(p, verified)
	if err != nil || outcome != Succeeded && outcome != Failed && outcome != Unknown {
		return e.finish(step.ID, Unknown, raw)
	}
	absent := p.Request().Operation == "injection.delete" && verified.Status == 404 && verified.Code() == "not_found"
	if outcome == Succeeded && !absent && (verified.Status != 200 && verified.Status != 201 && verified.Status != 204) {
		return e.finish(step.ID, Unknown, raw)
	}
	return e.finish(step.ID, outcome, raw)
}

// Reconciliation returns a validated native record for reporting only. It never
// modifies the saved step/parent outcome, executes another mutation or resumes
// the campaign. The original reservation covers one query, including lost replies.
func (e *Executor) Reconcile(ctx context.Context, id string, query interceptor.PreparedOperation) (interceptor.OperationRecord, error) {
	saved, original, _, err := e.steps.Lookup(id)
	if err != nil {
		return interceptor.OperationRecord{}, err
	}
	fresh, err := e.steps.BeginReconciliation(id, query)
	if err != nil {
		return interceptor.OperationRecord{}, err
	}
	if !fresh {
		_, raw, err := e.steps.Reconciliation(id)
		if err != nil {
			return interceptor.OperationRecord{}, err
		}
		if len(raw) == 0 {
			if saved.ReconciliationState == campaign.ResultCommitted {
				return interceptor.OperationRecord{}, ErrUnknown
			}
			return interceptor.OperationRecord{}, ErrPending
		}
		response, err := interceptor.ParseResponse(raw)
		if err != nil {
			return interceptor.OperationRecord{}, err
		}
		return interceptor.DecodeOperationRecord(response, original)
	}
	// Reporting ignores the execution fence, but observes its own original deadline.
	deadline := query.Request().Deadline
	if cap := time.Now().Add(interceptor.QueryTimeout); deadline.After(cap) {
		deadline = cap
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if _, err = e.guard.check(ctx, query, true); err != nil {
		if save := e.steps.ResolveReconciliation(id, nil); save != nil {
			return interceptor.OperationRecord{}, save
		}
		return interceptor.OperationRecord{}, err
	}
	response, err := e.guard.peer.Execute(ctx, query)
	raw := response.Bytes()
	if err != nil {
		raw = nil
	} else if response, err = interceptor.ParseResponse(raw); err != nil {
		raw = nil
	}
	if save := e.steps.ResolveReconciliation(id, raw); save != nil {
		return interceptor.OperationRecord{}, save
	}
	if err != nil {
		return interceptor.OperationRecord{}, ErrUnknown
	}
	return interceptor.DecodeOperationRecord(response, original)
}
