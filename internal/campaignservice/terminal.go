package campaignservice

import (
	"context"
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

func (s *Service) finish() {
	defer close(s.terminalDone)
	result := TerminalResult{Closure: "unconfirmed", CleanupState: "unconfirmed", TargetStop: "not-permitted"}
	if s.target().Profile().Settings().AllowTargetStop {
		result.TargetStop = "unconfirmed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), TerminalTimeout)
	defer cancel()
	defer func() { s.terminal = result }()
	if err := s.acquire(ctx); err != nil {
		for _, target := range s.selectedEvidence() {
			result.Evidence = append(result.Evidence, EvidenceOutcome{SessionID: target.Identity.SessionID, State: "missing", Reason: "finalization_gate_unavailable", LocalMaxBytes: s.config.Evidence.MaxArchiveBytes, NativeMaxBytes: target.NativeMaxBytes})
		}
		return
	}
	defer s.release()
	defer func() { raw, _ := json.Marshal(result); _ = s.log("service.terminal-result", nil, raw, true, true) }()
	if s.config.HTTPS != nil {
		result.Closure = "not-applicable"
		result.CleanupState = "not-needed"
		result.TargetStop = "not-applicable"
		return
	}
	defer func() { result.Evidence = s.collectEvidence() }()
	view, err := s.inspect(ctx, true)
	if err != nil {
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
	closed, err := s.terminalCall(ctx, "service.native-close", closeRequest)
	if err == nil {
		_, err = interceptor.DecodeClosure(closed, campaignID)
	}
	if err != nil {
		return
	}
	result.Closure = "confirmed"
	view, err = s.inspect(ctx, true)
	if err != nil || view.Available {
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
			break
		}
		body, _ := json.Marshal(map[string]string{"injection_id": id})
		p, err := query("injection.delete", body)
		if err != nil {
			result.CleanupState = "unconfirmed"
			break
		}
		r, err := s.terminalCall(ctx, "service.native-cleanup", p)
		if err != nil || r.SessionRevision < view.Session.Revision || !(r.Status == 204 && (len(r.Body) == 0 || string(r.Body) == "null") && r.SessionRevision > view.Session.Revision || r.Status == 404 && r.Code() == "not_found") {
			result.CleanupState = "unconfirmed"
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
			r, err := s.config.Peer.ExecuteLifecycle(ctx, p)
			logErr := s.log("service.target-stop-result", nil, boundedResponse(r), true, false)
			if err == nil && logErr == nil {
				if _, err = interceptor.DecodeStop(r, p); err == nil {
					result.TargetStop = "confirmed"
				}
			}
		}
	}
}
func (s *Service) terminalCall(ctx context.Context, kind string, p interceptor.PreparedOperation) (interceptor.Response, error) {
	if ctx.Err() != nil {
		return interceptor.Response{}, ctx.Err()
	}
	if err := s.log(kind+"-intent", p.Bytes(), nil, true, false); err != nil {
		return interceptor.Response{}, err
	}
	if ctx.Err() != nil {
		return interceptor.Response{}, ctx.Err()
	}
	r, err := s.config.Peer.Execute(ctx, p)
	raw := boundedResponse(r)
	if logErr := s.log(kind+"-result", nil, raw, true, false); logErr != nil {
		return interceptor.Response{}, logErr
	}
	if err != nil {
		return interceptor.Response{}, err
	}
	if raw == nil {
		return interceptor.Response{}, ErrService
	}
	verified, err := interceptor.ParseResponse(raw)
	return verified, err
}

// Shutdown signals termination immediately. The caller can bound how long it
// waits for cleanup/reporting; that wait never gates the independent Docker kill.
func (s *Service) Shutdown(ctx context.Context) (TerminalResult, error) {
	s.Stop(ErrService)
	result, _, err := s.Wait(ctx)
	return result, err
}
