package attemptadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeexec"
)

type cleanupAdapter struct{ command interceptor.PreparedOperation }

func (a cleanupAdapter) Authorize(p interceptor.PreparedOperation) error {
	if !bytes.Equal(a.command.Bytes(), p.Bytes()) {
		return ErrPolicy
	}
	return nil
}
func (a cleanupAdapter) Interpret(p interceptor.PreparedOperation, r interceptor.Response) (nativeexec.Outcome, error) {
	if a.Authorize(p) != nil || r.SessionRevision < p.Request().ExpectedSessionRevision {
		return nativeexec.Unknown, ErrResult
	}
	if r.Status == 204 && (len(r.Body) == 0 || bytes.Equal(r.Body, []byte("null"))) && r.SessionRevision > p.Request().ExpectedSessionRevision {
		return nativeexec.Succeeded, nil
	}
	if r.Status == 404 && r.Code() == "not_found" {
		return nativeexec.Succeeded, nil
	}
	if r.Status >= 400 && r.Status < 500 {
		return nativeexec.Failed, nil
	}
	return nativeexec.Unknown, ErrResult
}
func (b *Broker) cleanup(ctx context.Context, q wireRequest, deadline time.Time) (reply, error) {
	handle, err := b.config.Attempts.Tools().AuthorizeCleanup(q.ID)
	if err != nil {
		if b.config.Writer.Fence().Err() != nil {
			return reply{}, err
		}
		return denied("ACTION_UNAVAILABLE"), nil
	}
	target, err := b.config.Cleanup(ctx, deadline)
	if err != nil {
		if errors.Is(err, ErrPolicy) {
			return denied("POLICY_DENIED"), nil
		}
		return reply{}, b.stop(err)
	}
	saved, _, err := b.config.Attempts.Tools().Lookup(q.ID)
	if err != nil {
		return reply{}, err
	}
	if target.Guard == nil || target.SessionRevision == 0 || target.Binding.SessionID != saved.Target.SessionID || target.Binding.RunRevision != uint64(saved.RunRevision) {
		return reply{}, b.stop(ErrAttempt)
	}
	body, _ := json.Marshal(map[string]string{"injection_id": handle.InjectionID})
	id := opaque("cleanup", q.Campaign, q.ID)
	p, err := interceptor.PrepareOperation(interceptor.OperationRequest{RequestID: id, OperationID: id, Operation: "injection.delete", CampaignID: q.Campaign, SessionID: target.Binding.SessionID, WorkerInstanceID: target.Binding.WorkerInstanceID, RunRevision: target.Binding.RunRevision, ExpectedSessionRevision: target.SessionRevision, Deadline: deadline}, body)
	if err != nil {
		return reply{}, b.stop(err)
	}
	executor, err := nativeexec.New(b.config.Attempts.NativeSteps(), target.Guard, cleanupAdapter{p})
	if err != nil {
		return reply{}, b.stop(err)
	}
	result, err := executor.Execute(ctx, q.ID, p)
	if err != nil {
		if errors.Is(err, nativeexec.ErrFailed) {
			effect := "known"
			if result.Step.Outcome == string(nativeexec.NotDispatched) {
				effect = "none"
			}
			return terminal("CLEANUP_FAILED", effect), nil
		}
		b.stop(err)
		return terminal("OUTCOME_UNKNOWN", "unknown"), nil
	}
	if result.Native == nil {
		return reply{}, b.stop(ErrResult)
	}
	outcome := "deleted"
	if result.Native.Status == 404 {
		outcome = "already_absent"
	}
	raw, _ := json.Marshal(map[string]string{"receipt_id": opaque("cleanup-receipt", q.Campaign, q.ID), "attempt_receipt_id": handle.ReceiptID, "action_id": handle.ActionID, "outcome": outcome})
	return reply{Result: raw}, nil
}
