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
		Tool       string `json:"tool_name"`
		Call       string `json:"tool_call_id"`
		Diagnostic struct {
			Source, Stage, Reason string
			Path                  string `json:"field_path"`
		} `json:"diagnostic"`
		Stopping *struct {
			Condition string
			Observed  int64 `json:"observed_count"`
			Limit     int64 `json:"configured_limit"`
		} `json:"stopping"`
	}
	_ = json.Unmarshal(q.Body, &body)
	d := campaign.NewWorkflowDiagnostic(q.ID, "harness-local", body.Diagnostic.Stage, body.Diagnostic.Reason, body.Diagnostic.Path, body.Tool, body.Call)
	// Never copy Harness-supplied message/identifier/correction into host summaries.
	// Their potentially sensitive prose is omitted from structured diagnostics.
	d.Omitted = true
	if body.Stopping != nil {
		d.Stopping = body.Stopping.Condition
		d.Observed = body.Stopping.Observed
		d.Limit = body.Stopping.Limit
	}
	if !d.Valid() {
		return s.stateEnvelope(raw, seq, stateDenied("INVALID_ARGUMENTS"))
	}
	receipt := "diagnostic-" + termination.NewRequestID()
	if _, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d, "receipt_id": receipt, "assertion_origin": "harness"})}, "", false); err != nil {
		return nil, err
	}
	s.workflowDiagnostic.Store(&d)
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
	if q.Operation == "engine.finish" {
		reply, err = s.rejectConclusion(q, &contracts.ConclusionError{Reason: "invalid_structure", FieldPath: issue.FieldPath, Cause: cause})
	} else {
		d := campaign.NewWorkflowDiagnostic(q.ID, "operator", "arguments", issue.Reason, issue.FieldPath, "", "")
		s.workflowDiagnostic.Store(&d)
		_, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
		reply = stateReply{Error: &attemptadapter.Fault{Code: "INVALID_ARGUMENTS", Message: d.Message(), InstancePath: d.FieldPath, Diagnostic: d.Wire(), Effect: "none", Disposition: "correct-and-resubmit"}}
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
	s.workflowDiagnostic.Store(&d)
	_, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
	return d.Wire(), d.Message(), d.FieldPath, err
}

// Preserve a safe primary cause even when a poisoned journal cannot retain it;
// worker completion has an independent durable record and receives this copy.
func (s *Service) retentionFailure(id, field string, primary error) error {
	reason := "retention_failed"
	if field == "assessment" {
		reason = "assessment_retention_failed"
	}
	d := campaign.NewWorkflowDiagnostic(id, "operator", "retention", reason, "/"+field, "", "")
	s.workflowDiagnostic.Store(&d)
	_, secondary := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
	failure := errors.Join(d, primary, secondary)
	s.Stop(failure)
	return failure
}
