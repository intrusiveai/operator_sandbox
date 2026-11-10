//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// WorkflowDiagnostic retains bounded failure facts and attributed producer corrections.
// Harness reports are attributed as such; dispatch/cleanup remain host evidence.
type DiagnosticOmission struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type WorkflowDiagnostic struct {
	ProducerOmissions  *[]DiagnosticOmission        `json:"producer_omissions,omitempty"`
	Detail             string                       `json:"detail,omitempty"`
	DetailOmitted      string                       `json:"detail_omitted,omitempty"`
	CorrectionOmitted  string                       `json:"correction_omitted,omitempty"`
	Facts              *contracts.FailureFacts      `json:"facts,omitempty"`
	FactsOmitted       bool                         `json:"facts_omitted,omitempty"`
	UnsupportedFields  *contracts.UnsupportedFields `json:"unsupported_fields,omitempty"`
	Constraint         *contracts.SchemaConstraint  `json:"constraint,omitempty"`
	Correction         string                       `json:"correction,omitempty"`
	Budget             *contracts.ModelTokenBudget  `json:"budget,omitempty"`
	TargetFailure      *TargetFailure               `json:"target_failure,omitempty"`
	Timeout            *AttemptTimeout              `json:"timeout,omitempty"`
	AttemptID          string                       `json:"attempt_id,omitempty"`
	Invocation         string                       `json:"target_invocation,omitempty"`
	ResultDelivery     string                       `json:"result_delivery,omitempty"`
	HostFinalized      bool                         `json:"host_finalized,omitempty"`
	Dispatched         int64                        `json:"attempts_dispatched,omitempty"`
	AssessmentAccepted bool                         `json:"assessment_accepted,omitempty"`
	CleanupCompleted   bool                         `json:"cleanup_completed,omitempty"`
	OperationID        string                       `json:"operation_id,omitempty"`
	Source             string                       `json:"source"`
	Stage              string                       `json:"stage"`
	Reason             string                       `json:"reason"`
	FieldPath          string                       `json:"field_path,omitempty"`
	Tool               string                       `json:"tool,omitempty"`
	CallID             string                       `json:"call_id,omitempty"`
	Omitted            bool                         `json:"unsafe_fields_omitted,omitempty"`
	Stopping           string                       `json:"stopping_condition,omitempty"`
	Observed           int64                        `json:"observed_count,omitempty"`
	Limit              int64                        `json:"configured_limit,omitempty"`
}

func NewWorkflowDiagnostic(operation, source, stage, reason, path, tool, call string) WorkflowDiagnostic {
	d := WorkflowDiagnostic{Source: source, Stage: stage, Reason: reason}
	if source != "harness-local" && source != "operator" && source != "target" && source != "cleanup" && source != "interceptor" && source != "unknown" {
		d.Source = "unknown"
		d.Omitted = true
	}
	if !contracts.SafeReason(stage) {
		d.Stage = "unknown"
		d.Omitted = true
	}
	if !contracts.SafeReason(reason) {
		d.Reason = "unknown_failure"
		d.Omitted = true
	}
	for _, entry := range []struct {
		in  string
		out *string
	}{{operation, &d.OperationID}, {tool, &d.Tool}, {call, &d.CallID}} {
		if entry.in == "" {
			continue
		}
		value, why := diagnosticID(entry.in)
		*entry.out = value
		if why != "" {
			d.Omitted = true
		}
	}
	var ok bool
	d.FieldPath, ok = contracts.SafeArgumentPath(path)
	if len(d.FieldPath) > 256 {
		d.FieldPath = ""
		ok = false
	}
	if !ok {
		d.Omitted = true
	}
	return d
}

// Informational reports remain evidence without replacing an actual failure.
func (d WorkflowDiagnostic) Informational() bool {
	return d.Reason == "observation_selection" || d.Reason == "finalization_capacity_protected"
}

func (d WorkflowDiagnostic) Valid() bool {
	if d.ProducerOmissions != nil {
		if len(*d.ProducerOmissions) > 8 {
			return false
		}
		for _, o := range *d.ProducerOmissions {
			if !slices.Contains([]string{"message", "field_path", "identifier", "correction", "tool_call_id", "facts"}, o.Field) || !slices.Contains([]string{"sensitive", "oversized", "unavailable", "invalid"}, o.Reason) {
				return false
			}
		}
	}
	if !d.Facts.Valid() || !diagnostictext.Valid(d.Detail, 512) || !diagnostictext.Valid(d.Correction, contracts.CorrectionLimit) {
		return false
	}
	for _, why := range []string{d.DetailOmitted, d.CorrectionOmitted} {
		if !slices.Contains([]string{"", "truncated", "unsafe_control", "credential_redacted", "unsupported_value", "not_provided"}, why) {
			return false
		}
	}

	if d.Reason == "finalization_capacity_protected" && (d.Source != "harness-local" || d.Stage != "dispatch" || d.Stopping != "" || d.Budget != nil || d.Observed != 0 || d.Limit != 0 || d.Tool != "" || d.CallID != "" || d.FieldPath != "" || d.Constraint != nil || d.Correction != "") {
		return false
	}
	if len(d.Correction) > contracts.CorrectionLimit || !d.UnsupportedFields.Valid() || d.UnsupportedFields != nil && (d.Constraint == nil || d.Constraint.Keyword != "additionalProperties") {
		return false
	}
	if d.Stopping == "no-progress-limit" && (d.Reason != "no_progress_limit" || d.Stage != "termination" || d.Observed < d.Limit || d.Limit < 1 || d.Tool != "" || d.CallID != "" || d.FieldPath != "" || d.Constraint != nil || d.Correction != "") {
		return false
	}
	if d.Reason == "model_reservation_unavailable" && d.Budget == nil {
		return false
	}
	if d.Budget != nil && (!d.Budget.Valid() || d.Stopping != "budget-limit" || d.Reason != "model_reservation_unavailable" || d.Stage != "termination" || d.Source != "harness-local" || d.Observed != 0 || d.Limit != 0 || d.FieldPath != "" || d.Tool != "" || d.CallID != "") {
		return false
	}
	if d.TargetFailure != nil && (!d.TargetFailure.Valid() || !((d.Source == "target" || d.Source == "operator" || d.Source == "interceptor" || d.Source == "unknown") && d.Reason == "target_failed" || d.Source == "cleanup" && d.Reason == "cleanup_failed") || d.Timeout != nil) {
		return false
	}
	if d.Timeout != nil {
		id, why := diagnosticID(d.AttemptID)
		if !d.Timeout.Valid() || d.Reason != "attempt_timeout" || d.Source != "operator" || id != d.AttemptID || why != "" || !slices.Contains([]string{"not-dispatched", "succeeded", "failed", "unknown"}, d.Invocation) || !slices.Contains([]string{"unconfirmed", "published", "unavailable"}, d.ResultDelivery) {
			return false
		}
	} else if d.AttemptID != "" || d.Invocation != "" || d.ResultDelivery != "" {
		return false
	}
	v := NewWorkflowDiagnostic(d.OperationID, d.Source, d.Stage, d.Reason, d.FieldPath, d.Tool, d.CallID)
	if v.OperationID != d.OperationID || v.Source != d.Source || v.Stage != d.Stage || v.Reason != d.Reason || v.FieldPath != d.FieldPath || v.Tool != d.Tool || v.CallID != d.CallID {
		return false
	}
	switch d.Stopping {
	case "", "no-progress-limit", "invalid-call-limit", "tool-call-limit", "model-turn-limit", "wall-time-limit", "context-limit", "budget-limit", "local-error":
	default:
		return false
	}
	return d.Dispatched >= 0 && d.Dispatched <= contracts.MaxSafeInteger && (d.HostFinalized || d.Dispatched == 0 && !d.AssessmentAccepted && !d.CleanupCompleted) && (d.OperationID != "" || d.Omitted) && len(d.Message()) <= contracts.CorrectionLimit+8192 && d.Observed >= 0 && d.Observed <= contracts.MaxSafeInteger && d.Limit >= 0 && d.Limit <= contracts.MaxSafeInteger && (d.Stopping == "" || d.Limit > 0 || d.Budget != nil)
}
func (d WorkflowDiagnostic) Message() string {
	text := "Reported reason: " + d.Reason + ". No further explanation was retained."
	if d.Detail != "" {
		text = d.Detail
	}
	if d.Correction != "" {
		if d.Detail == "" || d.Detail == d.Correction {
			text = d.Correction
		} else {
			text += " Correction: " + d.Correction
		}
	}
	if d.TargetFailure != nil {
		text = d.TargetFailure.Message()
	}
	if d.Timeout != nil {
		text = d.Timeout.Message(d.Invocation)
	}
	if d.Budget != nil {
		b := d.Budget
		text = fmt.Sprintf("Harness could not request another model response. Model-token allowance: %d; consumed: %d; remaining: %d; required reservation: %d.", b.TotalAllowance, b.Consumed, b.Remaining, b.NextRequestReservation)
		if d.HostFinalized && !d.AssessmentAccepted {
			text += " No final assessment was committed."
		}
	} else if d.Stopping == "no-progress-limit" {
		text = fmt.Sprintf("Harness stopped after %d consecutive turns without recognized progress (configured limit %d).", d.Observed, d.Limit)
		if d.HostFinalized && !d.AssessmentAccepted {
			text += " No final assessment was committed."
		}
	} else if d.Stopping == "invalid-call-limit" {
		text = fmt.Sprintf("Harness stopped after %d invalid tool calls (configured limit %d).", d.Observed, d.Limit)
		if d.HostFinalized && !d.AssessmentAccepted {
			text += " No final assessment was committed."
		}
	} else if d.Stopping != "" && d.Limit > 0 {
		text = fmt.Sprintf("Harness stopped: %s (observed %d; configured limit %d). %s", d.Stopping, d.Observed, d.Limit, text)
	}
	if d.Stopping != "" && d.Limit == 0 && d.Budget == nil {
		text = "Harness stopped: " + d.Stopping + ". " + text
	}
	if d.Stopping == "" && d.TargetFailure == nil && d.Timeout == nil && !d.Informational() {
		switch d.Source {
		case "harness-local":
			text = "Attack Harness reported a local operation failure: " + text
		case "operator":
			text = "Operator rejected or could not complete the Harness operation: " + text
		case "unknown":
			text = "Diagnostic source invalid or unknown; failure origin unknown. " + text
		}
	}
	if d.FieldPath != "" {
		text += " Field: " + contracts.QuotedPath(d.FieldPath) + "."
	}
	if d.Omitted {
		text += " Unsafe diagnostic fields omitted."
	}
	text += d.Facts.Text()
	if d.DetailOmitted != "" {
		text += " Message omitted or bounded (" + d.DetailOmitted + ")."
	}
	if d.CorrectionOmitted != "" {
		text += " Correction omitted or bounded (" + d.CorrectionOmitted + ")."
	}
	if d.FactsOmitted {
		text += " Some failure facts were omitted for safety or size."
	}
	if d.ProducerOmissions != nil {
		for _, o := range *d.ProducerOmissions {
			text += fmt.Sprintf(" Producer omitted %s (%s).", o.Field, o.Reason)
		}
	}
	return text
}
func (d WorkflowDiagnostic) Error() string             { return d.Message() }
func (d WorkflowDiagnostic) DiagnosticMessage() string { return d.Message() }
func DecodeWorkflowDiagnostic(raw []byte) *WorkflowDiagnostic {
	if _, err := contracts.Decode(raw, 32768); err != nil {
		return nil
	}
	var d WorkflowDiagnostic
	if json.Unmarshal(raw, &d) != nil || !d.Valid() {
		return nil
	}
	return &d
}

// Wire fields describe definite predispatch rejection, not an execution receipt.
func (d WorkflowDiagnostic) Wire() json.RawMessage {
	v := map[string]any{"source": d.Source, "stage": d.Stage, "reason": d.Reason, "message": d.Message(), "execution_occurred": "no", "resubmission_safe": true}
	if d.Facts != nil {
		v["facts"] = d.Facts
	}
	if d.UnsupportedFields != nil {
		v["unsupported_fields"] = d.UnsupportedFields
	}
	if d.Constraint != nil {
		v["constraint"] = d.Constraint
	}
	if d.Correction != "" {
		v["correction"] = d.Correction
		v["message"] = "The operation was rejected before execution; follow the correction."
	}
	if d.FieldPath != "" {
		v["field_path"] = d.FieldPath
	}
	omissions := []DiagnosticOmission{}
	if d.ProducerOmissions != nil {
		omissions = append(omissions, (*d.ProducerOmissions)...)
	}
	for _, entry := range []struct{ field, why string }{{"message", d.DetailOmitted}, {"correction", d.CorrectionOmitted}} {
		if entry.why == "" {
			continue
		}
		reason := "invalid"
		switch entry.why {
		case "truncated":
			reason = "oversized"
		case "credential_redacted":
			reason = "sensitive"
		case "not_provided":
			reason = "unavailable"
		}
		omissions = append(omissions, DiagnosticOmission{entry.field, reason})
	}
	if d.FactsOmitted {
		omissions = append(omissions, DiagnosticOmission{"facts", "invalid"})
	}
	if len(omissions) > 8 {
		omissions = omissions[:8]
	}
	if len(omissions) > 0 {
		v["omissions"] = omissions
	}
	raw, _ := json.Marshal(v)
	return raw
}

// Retain the producer's bounded explanation as attributed evidence. Schema-owned
// corrections may replace it only when they describe the same supplied constraint.
func (d *WorkflowDiagnostic) RetainDetails(message, correction string, facts *contracts.FailureFacts) {
	d.Detail, d.DetailOmitted = diagnostictext.Safe(message, 512)
	d.Correction, d.CorrectionOmitted = diagnostictext.Safe(correction, contracts.CorrectionLimit)
	d.Facts, d.FactsOmitted = contracts.SanitizeFailureFacts(facts)
}

// WorkflowFromWire projects a retained operation rejection through the same
// bounded rendering path as diagnostics submitted explicitly by Harness.
func WorkflowFromWire(operation string, raw []byte) *WorkflowDiagnostic {
	if len(raw) > 32768 {
		return nil
	}
	var wire struct {
		Source, Stage, Reason, Message, Correction string
		Omissions                                  *[]DiagnosticOmission `json:"omissions"`
		Path                                       string                `json:"field_path"`
		Facts                                      *contracts.FailureFacts
		Constraint                                 *contracts.SchemaConstraint
		Unsupported                                *contracts.UnsupportedFields `json:"unsupported_fields"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return nil
	}
	d := NewWorkflowDiagnostic(operation, wire.Source, wire.Stage, wire.Reason, wire.Path, "", "")
	d.RetainDetails(wire.Message, wire.Correction, wire.Facts)
	d.ProducerOmissions = wire.Omissions
	d.Constraint = wire.Constraint
	d.UnsupportedFields = wire.Unsupported
	if !d.Valid() {
		return nil
	}
	return &d
}
