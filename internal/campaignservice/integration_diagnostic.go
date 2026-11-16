package campaignservice

import (
	"errors"
	"strings"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// recordIntegrationFailure is shared by native operations, background monitoring,
// and other integrations exposing producer-owned diagnostics. It runs before the
// execution fence closes. Cleanup uses separate terminal records instead.
func (s *Service) recordIntegrationFailure(err error, operation string) error {
	if err == nil || s.stopAccepted.Load() {
		return nil
	}
	if s.FailureTrigger() != nil {
		return nil
	}
	f := diagnostictext.FailureFromError(err)
	if f == nil {
		return nil
	}
	if operation == "" {
		operation = f.RequestID
	}
	if operation == "" {
		operation = "integration-failure"
	}
	d := campaign.NewWorkflowDiagnostic(operation, "operator", "integration", f.Category, "", "", "")
	d.Integration = f
	d.IntegrationTerminal = true
	if !d.Valid() {
		return nil
	}
	// A response rejected before its failed retention remains primary even if the
	// writer has already closed its fence. The joined error proves that this
	// fence failure belongs to this exchange, not an unrelated earlier stop.
	cause := s.writer.Fence().Err()
	if cause != nil && strings.Contains(f.Omissions, "response_retention:failed;") && errors.Is(err, cause) {
		fact := campaign.FailureTrigger{Kind: "workflow", OperationID: operation}
		if fact.Valid() {
			s.failureTrigger.CompareAndSwap(nil, &fact)
		}
	} else {
		s.recordFailureTrigger("workflow", operation)
	}
	if trigger := s.FailureTrigger(); trigger == nil || trigger.OperationID != operation {
		return nil
	}
	s.workflowDiagnostic.Store(&d)
	_, save := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d, "failure_trigger": s.FailureTrigger()})}, "", false)
	if save != nil {
		return errors.Join(d, save)
	}
	return nil
}

// Recoverable native rejections remain history and never become a stop trigger.
func (s *Service) recordNativeRejection(operation string, err error) error {
	f := diagnostictext.FailureFromError(err)
	if f == nil {
		return nil
	}
	d := campaign.NewWorkflowDiagnostic(operation, "operator", "integration", f.Category, "", "", "")
	d.Integration = f
	if !d.Valid() {
		return nil
	}
	_, save := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "workflow.diagnostic", Metadata: marshal(map[string]any{"diagnostic": d})}, "", false)
	return save
}
