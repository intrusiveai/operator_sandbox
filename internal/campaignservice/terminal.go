package campaignservice

import (
	"context"
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

func (s *Service) finish() {
	defer close(s.terminalDone)
	var failures []diagnostictext.Failure
	retainFailure := func(operation string, err error) {
		if err == nil || len(failures) >= 16 {
			return
		}
		f := diagnostictext.FailureFromError(diagnostictext.Context(err, "Interceptor", operation, ""))
		if f != nil {
			failures = append(failures, *f)
		}
	}
	result := TerminalResult{Closure: "unconfirmed", CleanupState: "unconfirmed", TargetStop: "not-permitted"}
	if s.target().Profile().Settings().AllowTargetStop {
		result.TargetStop = "unconfirmed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), TerminalTimeout)
	defer cancel()
	defer func() {
		if len(failures) > 0 {
			result.Failures = &failures
		}
		result.ConclusionDiagnostic = s.ConclusionFailure()
		result.WorkflowDiagnostic = s.WorkflowFailure()
		s.terminal = result
	}()
	if err := s.acquire(ctx); err != nil {
		for _, target := range s.selectedEvidence() {
			result.Evidence = append(result.Evidence, EvidenceOutcome{SessionID: target.Identity.SessionID, State: "missing", Reason: "finalization_gate_unavailable", LocalMaxBytes: s.config.Evidence.MaxArchiveBytes, NativeMaxBytes: target.NativeMaxBytes})
		}
		return
	}
	defer s.release()
	dispatched := s.attempts.Status().Dispatched
	result.Dispatched = &dispatched
	result.AssessmentState = s.completion.assessmentState
	defer func() {
		if len(failures) > 0 {
			result.Failures = &failures
		}
		result.ConclusionDiagnostic = s.ConclusionFailure()
		result.WorkflowDiagnostic = s.WorkflowFailure()
		raw, _ := json.Marshal(result)
		_ = s.log("service.terminal-result", nil, raw, true, true)
	}()
	if s.config.HTTPS != nil {
		result.Closure = "not-applicable"
		result.CleanupState = "not-needed"
		result.TargetStop = "not-applicable"
		return
	}
	defer func() { result.Evidence = s.collectEvidence() }()
	view, err := s.inspect(ctx, true)
	if err != nil {
		retainFailure("session.status", err)
		return
	}
	b := s.target().Live().Binding()
	campaignID := s.writer.Manifest().CampaignID
	deadline, _ := ctx.Deadline()
	query := func(operation string, body []byte) (interceptor.PreparedOperation, error) {
		id := "terminal-" + termination.NewRequestID()
		return interceptor.PrepareOperation(interceptor.OperationRequest{RequestID: id, OperationID: id, Operation: operation, CampaignID: campaignID, SessionID: b.SessionID, WorkerInstanceID: b.WorkerInstanceID, RunRevision: b.RunRevision, ExpectedSessionRevision: view.Session.Revision, Deadline: deadline}, body)
	}
	closeID := "close-" + termination.NewRequestID()
	closeRequest, err := interceptor.PrepareClose(interceptor.OperationRequest{RequestID: closeID, OperationID: closeID, CampaignID: campaignID, SessionID: b.SessionID, WorkerInstanceID: b.WorkerInstanceID, RunRevision: b.RunRevision, ExpectedSessionRevision: view.Session.Revision, Deadline: deadline})
	if err != nil {
		return
	}
	_, err = s.terminalCall(ctx, "service.native-close", closeRequest, func(r interceptor.Response) (interceptor.Response, error) {
		_, err := interceptor.DecodeClosure(r, campaignID)
		return r, err
	})
	if err != nil {
		retainFailure("session.owner", err)
		return
	}
	result.Closure = "confirmed"
	view, err = s.inspect(ctx, true)
	if err != nil || view.Available {
		if err == nil {
			err = diagnostictext.New("Execution remains available after closure confirmation.")
		}
		retainFailure("session.status", err)
		return
	}
	ids, total, err := s.attempts.CleanupCandidates(MaxCleanup)
	if err != nil {
		return
	}
	result.CleanupRemaining = total
	result.CleanupState = "complete"
	for _, id := range ids {
		if ctx.Err() != nil {
			result.CleanupState = "deadline"
			retainFailure("injection.delete", diagnostictext.Context(ctx.Err(), "Interceptor", "injection.delete", ""))
			break
		}
		body, _ := json.Marshal(map[string]string{"injection_id": id})
		p, err := query("injection.delete", body)
		if err != nil {
			result.CleanupState = "unconfirmed"
			retainFailure("injection.delete", err)
			break
		}
		r, err := s.terminalCall(ctx, "service.native-cleanup", p, func(r interceptor.Response) (interceptor.Response, error) {
			return interceptor.DecodeDeletion(r, view.Session.Revision)
		})

		if err != nil {
			result.CleanupState = "unconfirmed"
			retainFailure("injection.delete", err)
			break
		}
		view.Session.Revision = r.SessionRevision
		result.CleanupConfirmed++
		result.CleanupRemaining--
	}
	if result.CleanupState == "complete" && result.CleanupRemaining > 0 {
		result.CleanupState = "bounded-remainder"
	}
	if s.target().Profile().Settings().AllowTargetStop {
		result.TargetStop = "unconfirmed"
		p, err := interceptor.PrepareLifecycle(interceptor.LifecycleRequest{CampaignID: campaignID, SessionID: b.SessionID, WorkerInstanceID: b.WorkerInstanceID, RunRevision: b.RunRevision, OperationID: "stop-" + termination.NewRequestID(), Operation: "session.stop"}, deadline)
		if err == nil && ctx.Err() == nil && s.log("service.target-stop-intent", p.Bytes(), nil, true, false) == nil {
			_, err = interceptor.Call("session.stop", p.Request().OperationID,
				func() (interceptor.Response, error) { return s.config.Peer.ExecuteLifecycle(ctx, p) },
				func(r interceptor.Response) error {
					return s.log("service.target-stop-result", nil, boundedResponse(r), true, false)
				},
				func(r interceptor.Response) (interceptor.StopResult, error) { return interceptor.DecodeStop(r, p) })
			if err == nil {
				result.TargetStop = "confirmed"
			} else {
				retainFailure("session.stop", err)
			}
		}
	}
}
func (s *Service) terminalCall(ctx context.Context, kind string, p interceptor.PreparedOperation, decode func(interceptor.Response) (interceptor.Response, error)) (interceptor.Response, error) {
	if ctx.Err() != nil {
		return interceptor.Response{}, ctx.Err()
	}
	if err := s.log(kind+"-intent", p.Bytes(), nil, true, false); err != nil {
		return interceptor.Response{}, err
	}
	if ctx.Err() != nil {
		return interceptor.Response{}, ctx.Err()
	}
	return interceptor.Call(p.Request().Operation, p.Request().RequestID,
		func() (interceptor.Response, error) { return s.config.Peer.Execute(ctx, p) },
		func(r interceptor.Response) error { return s.log(kind+"-result", nil, boundedResponse(r), true, false) },
		decode)
}

// Shutdown signals termination immediately. The caller can bound how long it
// waits for cleanup/reporting; that wait never gates the independent Docker kill.
func (s *Service) Shutdown(ctx context.Context) (TerminalResult, error) {
	s.Stop(ErrService)
	result, _, err := s.Wait(ctx)
	return result, err
}
