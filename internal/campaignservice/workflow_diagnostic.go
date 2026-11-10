//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

func (s *Service) WorkflowFailure() *campaign.WorkflowDiagnostic {
	if d := s.workflowDiagnostic.Load(); d != nil {
		copy := *d
		return &copy
	}
	return nil
}
func (s *Service) handleDiagnostic(ctx context.Context, raw []byte, seq int64) ([]byte, error) {
	q, saved, err := s.observeTool(raw)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		return s.stateEnvelope(raw, seq, *saved)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	var body struct {
		Selections json.RawMessage `json:"observation_selections"`
		Tool       string          `json:"tool_name"`
		Call       string          `json:"tool_call_id"`
		Diagnostic struct {
			Source, Stage, Reason string
			Message, Correction   string
			Omissions             *[]campaign.DiagnosticOmission `json:"omissions"`
			Facts                 *contracts.FailureFacts        `json:"facts"`
			UnsupportedFields     *contracts.UnsupportedFields   `json:"unsupported_fields"`
			Constraint            *contracts.SchemaConstraint    `json:"constraint"`
			Path                  string                         `json:"field_path"`
		} `json:"diagnostic"`
		Stopping *struct {
			Condition string
			Budget    *contracts.ModelTokenBudget `json:"budget"`
			Observed  int64                       `json:"observed_count"`
			Limit     int64                       `json:"configured_limit"`
		} `json:"stopping"`
	}
	_ = json.Unmarshal(q.Body, &body)
	d := campaign.NewWorkflowDiagnostic(q.ID, "harness-local", body.Diagnostic.Stage, body.Diagnostic.Reason, body.Diagnostic.Path, body.Tool, body.Call)
	d.RetainDetails(body.Diagnostic.Message, body.Diagnostic.Correction, body.Diagnostic.Facts)
	d.ProducerOmissions = body.Diagnostic.Omissions
	if body.Diagnostic.UnsupportedFields != nil && body.Diagnostic.Constraint == nil {
		return s.stateEnvelope(raw, seq, stateDenied("INVALID_ARGUMENTS"))
	}
	if body.Diagnostic.Constraint != nil {
		correction, ok := s.target().Protocol().ConstraintCorrectionWithFields(body.Diagnostic.Constraint, body.Diagnostic.UnsupportedFields)
		if !ok {
			return s.stateEnvelope(raw, seq, stateDenied("INVALID_ARGUMENTS"))
		}
		d.UnsupportedFields = body.Diagnostic.UnsupportedFields
		d.Constraint = body.Diagnostic.Constraint
		d.Correction = correction
	}
	// Producer explanations remain explicitly attributed to Harness; host facts remain separate.
	d.Omitted = d.Omitted || d.DetailOmitted != "" || d.CorrectionOmitted != "" || d.FactsOmitted
	if body.Stopping != nil {
		d.Budget = body.Stopping.Budget
		d.Stopping = body.Stopping.Condition
		d.Observed = body.Stopping.Observed
		d.Limit = body.Stopping.Limit
	}
	if !d.Valid() {
		return s.stateEnvelope(raw, seq, stateDenied("INVALID_ARGUMENTS"))
	}
	selections := campaign.DecodeObservationSelections(body.Selections)
	if len(body.Selections) > 0 && len(selections) == 0 {
		return s.stateEnvelope(raw, seq, stateDenied("INVALID_ARGUMENTS"))
	}
	for _, selection := range selections {
		if err = s.checkReferences(map[string]any{"observation_refs": []any{selection["observation_ref"]}}, "", true); err != nil {
			return s.stateEnvelope(raw, seq, stateDenied("INVALID_ARGUMENTS"))
		}
	}
	receipt := "diagnostic-" + termination.NewRequestID()
	if d.Stopping != "" {
		s.recordFailureTrigger("workflow", d.OperationID)
	}
	if _, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d, "receipt_id": receipt, "assertion_origin": "harness", "observation_selections": selections, "failure_trigger": s.FailureTrigger()})}, "", false); err != nil {
		return nil, err
	}
	if !d.Informational() {
		if primary := s.WorkflowFailure(); primary == nil || primary.TargetFailure == nil {
			s.workflowDiagnostic.Store(&d)
		}
	}
	reply := stateReply{Result: map[string]any{"status": "retained", "receipt_id": receipt}}
	if err = s.attempts.Tools().Finish(q.ID, marshal(reply), 0); err != nil {
		return nil, err
	}
	return s.stateEnvelope(raw, seq, reply)
}

// A structurally valid envelope with invalid arguments is correctable. Retain its
// rejection in the existing namespace; the same ID cannot later execute a repair.
func (s *Service) rejectArguments(raw []byte, seq int64, cause error) ([]byte, error) {
	q, saved, err := s.observeTool(raw)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		return s.stateEnvelope(raw, seq, *saved)
	}
	var issue *contracts.ArgumentError
	if !errors.As(cause, &issue) {
		issue = &contracts.ArgumentError{Reason: "invalid_structure", Correction: "Supply the required fields with the documented types and no unknown fields."}
	}
	var reply stateReply
	if q.Operation == "engine.finish" && issue.Constraint == nil {
		reply, err = s.rejectConclusion(q, &contracts.ConclusionError{Reason: "invalid_structure", FieldPath: issue.FieldPath, Cause: cause})
	} else {
		d := campaign.NewWorkflowDiagnostic(q.ID, "operator", "arguments", issue.Reason, issue.FieldPath, "", "")
		d.UnsupportedFields = issue.UnsupportedFields
		d.Constraint = issue.Constraint
		d.RetainDetails("", issue.Correction, issue.Facts)
		message := d.Message()
		if d.Correction != "" {
			message = rejectionMessage(d.Correction) // Full guidance is in the diagnostic.
		}
		s.workflowDiagnostic.Store(&d)
		_, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
		reply = stateReply{Error: &attemptadapter.Fault{Code: "INVALID_ARGUMENTS", Message: message, InstancePath: d.FieldPath, Diagnostic: d.Wire(), Effect: "none", Disposition: "correct-and-resubmit"}}
	}
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	if err = s.attempts.Tools().Finish(q.ID, marshal(reply), 0); err != nil {
		return nil, errors.Join(cause, err)
	}
	return s.stateEnvelope(raw, seq, reply)
}

func (s *Service) recordArgumentRejection(id string, cause error) (json.RawMessage, string, string, error) {
	reason, path := "invalid_arguments", ""
	var issue *contracts.ArgumentError
	if errors.As(cause, &issue) {
		reason, path = issue.Reason, issue.FieldPath
	}
	if issue == nil && errors.Is(cause, attemptadapter.ErrPolicy) {
		reason = "policy_denied"
	}
	d := campaign.NewWorkflowDiagnostic(id, "operator", "arguments", reason, path, "", "")
	if issue != nil {
		d.UnsupportedFields = issue.UnsupportedFields
		d.Constraint = issue.Constraint
		d.RetainDetails("", issue.Correction, issue.Facts)
	}
	s.workflowDiagnostic.Store(&d)
	_, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
	message := d.Message()
	if d.Correction != "" {
		message = rejectionMessage(d.Correction)
	}
	return d.Wire(), message, d.FieldPath, err
}

// Preserve a safe primary cause even when a poisoned journal cannot retain it;
// worker completion has an independent durable record and receives this copy.
func (s *Service) retentionFailure(id, field string, primary error) error {
	reason := "retention_failed"
	if field == "assessment" {
		reason = "assessment_retention_failed"
	}
	d := campaign.NewWorkflowDiagnostic(id, "operator", "retention", reason, "/"+field, "", "")
	d.RetainDetails("Required "+field+" retention failed. The operation was not admitted; execution closes for owned cleanup.", "", nil)
	s.workflowDiagnostic.Store(&d)
	_, secondary := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
	failure := errors.Join(d, primary, secondary)
	s.Stop(failure)
	return failure
}

// recordAttemptTimeout selects only validated, product-authored result fields.
func (s *Service) recordAttemptTimeout(operationID, callID string, raw []byte) error {
	var r struct {
		AttemptID  string                   `json:"attempt_id"`
		Stage      string                   `json:"stage"`
		Invocation string                   `json:"invocation_state"`
		Timeout    *campaign.AttemptTimeout `json:"timeout"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Timeout == nil {
		return nil
	}
	d := campaign.NewWorkflowDiagnostic(operationID, "operator", r.Stage, "attempt_timeout", "", "attempt_execute", callID)
	d.AttemptID = r.AttemptID
	d.Timeout = r.Timeout
	d.Invocation = r.Invocation
	d.ResultDelivery = "unconfirmed"
	if !d.Valid() {
		return ErrService
	}
	s.workflowDiagnostic.Store(&d)
	s.recordFailureTrigger("workflow", d.OperationID)
	_, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d, "failure_trigger": s.FailureTrigger()})}, "", false)
	if err != nil {
		return errors.Join(d, err)
	}
	return nil
}
func (s *Service) recordTimeoutDelivery(delivery string) error {
	d := s.WorkflowFailure()
	if d == nil || d.Timeout == nil {
		return nil
	}
	d.ResultDelivery = delivery
	s.workflowDiagnostic.Store(d)
	_, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d, "failure_trigger": s.FailureTrigger()})}, "", false)
	return err
}

// recordTargetFailure runs before attempt publication and survives terminal shutdown.
func (s *Service) recordTargetFailure(operationID, callID string, failure *campaign.TargetFailure) error {
	source, stage, reason := "target", "delivery", "target_failed"
	switch failure.Category {
	case "native_rejection":
		source = "interceptor"
	case "communication_failure", "preflight_failure", "native_response_invalid":
		source = "operator"
	case "unknown_boundary_failure":
		source = "unknown"
	}
	switch failure.NativeOperation {
	case "observation.read", "observation.content.read":
		stage = "observation"
	case "injection.delete":
		source, stage, reason = "cleanup", "cleanup", "cleanup_failed"
	}
	d := campaign.NewWorkflowDiagnostic(operationID, source, stage, reason, "", "attempt_execute", callID)
	d.TargetFailure = failure
	if !d.Valid() {
		return ErrService
	}
	s.workflowDiagnostic.Store(&d)
	s.recordFailureTrigger("workflow", d.OperationID)
	_, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d, "failure_trigger": s.FailureTrigger()})}, "", false)
	if err != nil {
		return errors.Join(d, err)
	}
	return nil
}

// Keep the initiating terminal failure separate from recoverable rejections and
// later cancellation. Native execution fences before publishing its typed cause.
func (s *Service) recordFailureTrigger(kind, operation string) {
	cause := s.writer.Fence().Err()
	if cause != nil && cause != campaign.ErrExecutionFailure && cause != campaign.ErrUnknownOutcome {
		return
	}
	fact := campaign.FailureTrigger{Kind: kind, OperationID: operation}
	if fact.Valid() {
		s.failureTrigger.CompareAndSwap(nil, &fact)
	}
}
func (s *Service) FailureTrigger() *campaign.FailureTrigger {
	if fact := s.failureTrigger.Load(); fact != nil {
		copy := *fact
		return &copy
	}
	return nil
}

func rejectionMessage(correction string) string {
	return contracts.ShortCorrection(correction)
}
