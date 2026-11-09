//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"fmt"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"slices"
)

// WorkflowDiagnostic is a bounded selection of facts, not submitted diagnostic prose.
// Harness reports are attributed as such; dispatch/cleanup remain host evidence.
type WorkflowDiagnostic struct {
	Constraint         *contracts.SchemaConstraint `json:"constraint,omitempty"`
	Correction         string                      `json:"correction,omitempty"`
	Budget             *contracts.ModelTokenBudget `json:"budget,omitempty"`
	TargetFailure      *TargetFailure              `json:"target_failure,omitempty"`
	Timeout            *AttemptTimeout             `json:"timeout,omitempty"`
	AttemptID          string                      `json:"attempt_id,omitempty"`
	Invocation         string                      `json:"target_invocation,omitempty"`
	ResultDelivery     string                      `json:"result_delivery,omitempty"`
	HostFinalized      bool                        `json:"host_finalized,omitempty"`
	Dispatched         int64                       `json:"attempts_dispatched,omitempty"`
	AssessmentAccepted bool                        `json:"assessment_accepted,omitempty"`
	CleanupCompleted   bool                        `json:"cleanup_completed,omitempty"`
	OperationID        string                      `json:"operation_id,omitempty"`
	Source             string                      `json:"source"`
	Stage              string                      `json:"stage"`
	Reason             string                      `json:"reason"`
	FieldPath          string                      `json:"field_path,omitempty"`
	Tool               string                      `json:"tool,omitempty"`
	CallID             string                      `json:"call_id,omitempty"`
	Omitted            bool                        `json:"unsafe_fields_omitted,omitempty"`
	Stopping           string                      `json:"stopping_condition,omitempty"`
	Observed           int64                       `json:"observed_count,omitempty"`
	Limit              int64                       `json:"configured_limit,omitempty"`
}

var workflowReasons = map[string]string{
	"unknown_failure":                 "Failure origin unknown; the diagnostic reason was not recognized.",
	"finalization_capacity_protected": "Harness ended exploration to protect capacity for an assessment request and one correction opportunity; this is not an execution failure.",
	"finalization_action_blocked":     "Protected finalization is active. The action was not executed; finish using retained evidence.",
	"assessment_repair_active":        "Assessment repair is active. The action was not executed; correct the rejected finish using retained evidence.",
	"finish_repair_unresolved":        "Harness terminated with a finish correction unresolved; no corrected assessment was accepted.",
	"context_limit":                   "Mandatory campaign and finalization context could not fit safely after local trimming.",
	"model_call_limit":                "No model-call allowance remains for another assessment or correction request.",
	"invalid_call_limit":              "Harness reached its invalid tool-call limit; execution cannot continue.",
	"snapshot_description_size":       "Shorten the snapshot description to its UTF-8 byte allowance; no snapshot was created.",
	"unknown_uncertainty_label":       "Reference a label declared in top-level uncertainties. Put explanatory prose in that entry's description; do not replay completed attempts.",
	"invalid_progress_accounting":     "No-progress exhaustion requires observed_count to reach configured_limit; report the actual counter.",
	"invalid_json":                    "Supply a valid JSON object with unique keys, safe numbers and bounded nesting.",
	"no_progress_limit":               "Harness reached its consecutive turns without recognized progress limit.",
	"model_reservation_unavailable":   "Harness could not reserve tokens for another model response.",
	"invalid_budget_accounting":       "Model-token accounting must balance and identify the next-request reservation.",
	"finish_choice":                   "Supply inline summary and objectives, or artifact, or unavailable_reason; do not combine them.",
	"finish_field_required":           "Inline finish requires both summary and objectives.",
	"injection_required":              "Nonempty setup requires injection content.",
	"setup_required":                  "Injection content and retention require nonempty setup.",
	"assessment_fields_required":      "Supply interpretation, confidence, assurance and assessment_summary together; gaps requires that complete group.",
	"content_choice":                  "Supply exactly one of json, text, base64 or a returned artifact handle.",
	"artifact_media_override":         "Artifact media comes from its committed descriptor; omit media_type.",
	"invalid_media_type":              "JSON requires application/json. Correct or omit media_type.",
	"media_type_required":             "Supply the media type of decoded base64 bytes.",
	"obsolete_wrapper":                "Place summary and objectives beside finish_reason; omit the assessment wrapper.",
	"unknown_handle":                  "Use a handle returned by a successful tool or verified campaign input.",
	"wrong_handle_kind":               "Select a returned handle of the required resource category.",
	"foreign_handle":                  "Select a resource belonging to this campaign.",
	"stale_handle":                    "Refresh the resource in the current target binding without replaying prior effects.",
	"evidence_not_inspected":          "Read available evidence completely or cite an inspected range; unread evidence may only describe a gap.",
	"unavailable_artifact":            "Use an available committed artifact handle.",
	"invalid_resolution_context":      "Harness could not resolve verified canonical resource fields.",
	"reference_limit":                 "Select at most 32 distinct observations and execution receipts per item.",
	"artifact_purpose":                "Select a committed artifact with the required purpose.",
	"evidence_required":               "Select inspected evidence and real execution handles or report an untested/inconclusive outcome.",
	"invalid_database_key":            "Supply the exact database policy key columns with non-null scalar values.",
	"injection_json_required":         "Supply canonical JSON injection content or a committed JSON artifact.",
	"ambiguous_label":                 "Choose a unique local label distinct from existing handles.",
	"hypothesis_cycle":                "Remove self-parenting or the hypothesis parent cycle.",
	"range_exceeded":                  "Read within the selected handle range or select the whole-entry handle.",
	"expanded_request_invalid":        "The expanded request violates a host constraint; reduce references/content or correct the selected resource.",
	"observation_selection":           "Harness selected verified observation references for an assessment item.",
	"attempt_timeout":                 "Attempt execution exceeded its deadline; execution is closed without replay.",
	"database_setup_denied":           "Use a permitted database service/table, exact key and writable placement. Creation requires explicit permission and matching payload keys. Execution did not occur; correct and resubmit with a new operation ID.",
	"origin_required":                 "Set origin to scenario with a submitted scenario_id, or exploratory without scenario_id. Execution did not occur; correct and resubmit with a new operation ID.",
	"scenario_required":               "Scenario origin requires a scenario_id from the submitted bundle. Execution did not occur; correction is safe with a new operation ID.",
	"scenario_forbidden":              "Exploratory origin forbids scenario_id. Remove it or select scenario origin. Execution did not occur; correction is safe with a new operation ID.",
	"unknown_scenario":                "Select a scenario_id from the submitted bundle, or exploratory origin without scenario_id. Execution did not occur; correction is safe with a new operation ID.",
	"assessment_retention_failed":     "The assessment could not be retained. Finish was not accepted; execution closes for owned cleanup.",
	"target_failed":                   "Operator observed a failed target operation; failure origin unknown. The retained result does not establish an application, Interceptor, or communication failure.",
	"target_outcome_unknown":          "Operator could not confirm the target operation outcome; failure origin unknown. Execution is closed and the operation will not be replayed.",
	"cleanup_failed":                  "Owned cleanup did not complete; retained state identifies the unconfirmed resources.",
	"request_too_large":               "Request exceeds its byte limit. Publish large input or assessment content as an artifact and use its receipt.",
	"policy_denied":                   "The selected operation or setup is not permitted by this campaign target policy.",
	"parent_not_completed":            "Select a parent with verified completed lineage.",
	"invalid_injection_input":         "Injection setup requires canonical application/json input.",
	"ARTIFACT_BUDGET_EXCEEDED":        "Input cannot be retained within the remaining campaign artifact allowance.",
	"invalid_arguments":               "Tool arguments were rejected before execution; supply the required fields and documented types.",
	"invalid_structure":               "Tool arguments do not match the required structure; check the indicated field.",
	"duplicate_json_key":              "A JSON key occurs more than once. Supply each key once.",
	"unsupported_tool":                "The requested tool is not advertised for this campaign.",
	"direct_input_too_large":          "Direct input exceeds the wire safety ceiling. Publish an artifact and use its returned receipt.",
	"configured_size_exceeded":        "The indicated field exceeds its host-configured size allowance in size_limits. Reduce it or use an artifact receipt for direct input. Execution did not occur; correction is safe.",
	"input_depth_exceeded":            "Direct JSON input exceeds 16 nested containers. Reduce nesting or publish an artifact.",
	"invalid_base64":                  "Input is not canonical padded base64. Correct the encoding or use JSON/UTF-8 input.",
	"invalid_artifact_receipt":        "Use direct input or a committed payload/carrier artifact receipt returned in this campaign.",
	"invalid_parent_receipt":          "Use an execution receipt from a completed attempt; a rejected submission has no execution receipt.",
	"invalid_reference":               "A reference does not identify the required retained object. Use returned handles of the documented category.",
	"invalid_range":                   "A range requires offset and length. Omit the optional range to reference the whole entry.",
	"local_error":                     "A local failure was reported; its underlying cause is unknown and unrestricted error text was omitted.",
	"retention_failed":                "Required input retention failed; the target was not dispatched.",
}

func NewWorkflowDiagnostic(operation, source, stage, reason, path, tool, call string) WorkflowDiagnostic {
	d := WorkflowDiagnostic{Source: source, Stage: stage, Reason: reason}
	if source != "harness-local" && source != "operator" && source != "target" && source != "cleanup" && source != "interceptor" && source != "unknown" {
		d.Source = "unknown"
		d.Omitted = true
	}
	if stage != "arguments" && stage != "references" && stage != "dispatch" && stage != "termination" && stage != "retention" && stage != "delivery" && stage != "observation" && stage != "cleanup" && stage != "translation" && stage != "unknown" {
		d.Stage = "unknown"
		d.Omitted = true
	}
	if _, ok := workflowReasons[reason]; !ok {
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
	if len(d.FieldPath) > 160 {
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
	if d.Reason == "finalization_capacity_protected" && (d.Source != "harness-local" || d.Stage != "dispatch" || d.Stopping != "" || d.Budget != nil || d.Observed != 0 || d.Limit != 0 || d.Tool != "" || d.CallID != "" || d.FieldPath != "" || d.Constraint != nil || d.Correction != "") {
		return false
	}
	if len(d.Correction) > 512 {
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
	return d.Dispatched >= 0 && d.Dispatched <= contracts.MaxSafeInteger && (d.HostFinalized || d.Dispatched == 0 && !d.AssessmentAccepted && !d.CleanupCompleted) && (d.OperationID != "" || d.Omitted) && len(d.Message()) <= 1024 && d.Observed >= 0 && d.Observed <= contracts.MaxSafeInteger && d.Limit >= 0 && d.Limit <= contracts.MaxSafeInteger && (d.Stopping == "" || d.Limit > 0 || d.Budget != nil)
}
func (d WorkflowDiagnostic) Message() string {
	text := workflowReasons[d.Reason]
	if d.Correction != "" {
		text = d.Correction
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
			text = "Diagnostic source invalid or unknown. " + text
		}
	}
	if d.FieldPath != "" {
		text += " Field: " + d.FieldPath + "."
	}
	if d.Omitted {
		text += " Unsafe diagnostic fields omitted."
	}
	return text
}
func (d WorkflowDiagnostic) Error() string { return d.Message() }
func DecodeWorkflowDiagnostic(raw []byte) *WorkflowDiagnostic {
	if _, err := contracts.Decode(raw, 8192); err != nil {
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
	if d.Omitted {
		v["omissions"] = []any{map[string]any{"field": "identifier", "reason": "sensitive"}}
	}
	raw, _ := json.Marshal(v)
	return raw
}
