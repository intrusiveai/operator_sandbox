//go:build linux || darwin

package campaignservice

import (
	"errors"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// First rejection survives later finalization, persistence and cleanup failures.
// A corrected conclusion can still finish normally; this is diagnostic history.
func (s *Service) ConclusionFailure() *campaign.ConclusionDiagnostic {
	if d := s.conclusionDiagnostic.Load(); d != nil {
		copy := *d
		return &copy
	}
	return nil
}
func (s *Service) rejectConclusion(q stateRequest, err error) (stateReply, error) {
	var issue *contracts.ConclusionError
	if !errors.As(err, &issue) {
		issue = &contracts.ConclusionError{Reason: "completion_chain_invalid"}
	}
	d := campaign.NewConclusionDiagnostic(q.ID, issue)
	s.conclusionDiagnostic.CompareAndSwap(nil, &d)
	_, saveErr := s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "conclusion.rejected", Metadata: marshal(map[string]any{"operation_id": d.OperationID, "diagnostic": d})}, "", false)
	if saveErr != nil {
		return stateReply{}, errors.Join(d, err, saveErr)
	}
	// Storage/corruption failures remain terminal; document/schema errors are
	// definite invalid submissions that may be corrected.
	if issue.Reason != "invalid_structure" && issue.Cause != nil && !errors.Is(err, errReference) {
		return stateReply{}, errors.Join(d, err)
	}
	return stateReply{Error: &attemptadapter.Fault{Code: "CONCLUSION_INVALID", Message: d.Message(), InstancePath: d.FieldPath, Effect: "none", Disposition: "correct-and-resubmit"}}, nil
}
