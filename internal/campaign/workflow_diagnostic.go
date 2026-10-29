//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"fmt"
	"github.com/intrusiveai/operator_sandbox/contracts"
)

// WorkflowDiagnostic is a bounded selection of facts, not submitted diagnostic prose.
// Harness reports are attributed as such; dispatch/cleanup remain host evidence.
type WorkflowDiagnostic struct {
	HostFinalized      bool   `json:"host_finalized,omitempty"`
	Dispatched         int64  `json:"attempts_dispatched,omitempty"`
	AssessmentAccepted bool   `json:"assessment_accepted,omitempty"`
	CleanupCompleted   bool   `json:"cleanup_completed,omitempty"`
	OperationID        string `json:"operation_id,omitempty"`
	Source             string `json:"source"`
	Stage              string `json:"stage"`
	Reason             string `json:"reason"`
	FieldPath          string `json:"field_path,omitempty"`
	Tool               string `json:"tool,omitempty"`
	CallID             string `json:"call_id,omitempty"`
	Omitted            bool   `json:"unsafe_fields_omitted,omitempty"`
	Stopping           string `json:"stopping_condition,omitempty"`
	Observed           int64  `json:"observed_count,omitempty"`
	Limit              int64  `json:"configured_limit,omitempty"`
}

var workflowReasons = map[string]string{
	"database_setup_denied":       "Use a permitted database service/table, exact key and writable placement. Creation requires explicit permission and matching payload keys. Execution did not occur; correct and resubmit with a new operation ID.",
	"origin_required":             "Set origin to scenario with a submitted scenario_id, or exploratory without scenario_id. Execution did not occur; correct and resubmit with a new operation ID.",
	"scenario_required":           "Scenario origin requires a scenario_id from the submitted bundle. Execution did not occur; correction is safe with a new operation ID.",
	"scenario_forbidden":          "Exploratory origin forbids scenario_id. Remove it or select scenario origin. Execution did not occur; correction is safe with a new operation ID.",
	"unknown_scenario":            "Select a scenario_id from the submitted bundle, or exploratory origin without scenario_id. Execution did not occur; correction is safe with a new operation ID.",
	"assessment_retention_failed": "The assessment could not be retained. Finish was not accepted; execution closes for owned cleanup.",
	"target_failed":               "The target operation failed. Inspect the retained attempt errors and permitted observations.",
	"target_outcome_unknown":      "The target operation outcome is uncertain. Execution is closed and the operation will not be replayed.",
	"cleanup_failed":              "Owned cleanup did not complete; retained state identifies the unconfirmed resources.",
	"request_too_large":           "Request exceeds its byte limit. Publish large input or assessment content as an artifact and use its receipt.",
	"policy_denied":               "The selected operation or setup is not permitted by this campaign target policy.",
	"parent_not_completed":        "Select a parent with verified completed lineage.",
	"invalid_injection_input":     "Injection setup requires canonical application/json input.",
	"ARTIFACT_BUDGET_EXCEEDED":    "Input cannot be retained within the remaining campaign artifact allowance.",
	"invalid_arguments":           "Tool arguments were rejected before execution; supply the required fields and documented types.",
	"invalid_structure":           "Tool arguments do not match the required structure; check the indicated field.",
	"duplicate_json_key":          "A JSON key occurs more than once. Supply each key once.",
	"unsupported_tool":            "The requested tool is not advertised for this campaign.",
	"direct_input_too_large":      "Direct input exceeds 32768 bytes. Publish an artifact and use its returned receipt.",
	"input_depth_exceeded":        "Direct JSON input exceeds 16 nested containers. Reduce nesting or publish an artifact.",
	"invalid_base64":              "Input is not canonical padded base64. Correct the encoding or use JSON/UTF-8 input.",
	"invalid_artifact_receipt":    "Use direct input or a committed payload/carrier artifact receipt returned in this campaign.",
	"invalid_parent_receipt":      "Use an execution receipt from a completed attempt; a rejected submission has no execution receipt.",
	"invalid_reference":           "A reference does not identify the required retained object. Use returned handles of the documented category.",
	"invalid_range":               "A range requires offset and length. Omit the optional range to reference the whole entry.",
	"local_error":                 "Harness reported a local failure; unrestricted error text was omitted.",
	"retention_failed":            "Required input retention failed; the target was not dispatched.",
}

func NewWorkflowDiagnostic(operation, source, stage, reason, path, tool, call string) WorkflowDiagnostic {
	d := WorkflowDiagnostic{Source: source, Stage: stage, Reason: reason}
	if source != "harness-local" && source != "operator" && source != "target" && source != "cleanup" {
		d.Source = "operator"
		d.Omitted = true
	}
	if stage != "arguments" && stage != "references" && stage != "dispatch" && stage != "termination" && stage != "retention" && stage != "delivery" && stage != "observation" && stage != "cleanup" && stage != "translation" {
		d.Stage = "arguments"
		d.Omitted = true
	}
	if _, ok := workflowReasons[reason]; !ok {
		d.Reason = "local_error"
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
func (d WorkflowDiagnostic) Valid() bool {
	v := NewWorkflowDiagnostic(d.OperationID, d.Source, d.Stage, d.Reason, d.FieldPath, d.Tool, d.CallID)
	if v.OperationID != d.OperationID || v.Source != d.Source || v.Stage != d.Stage || v.Reason != d.Reason || v.FieldPath != d.FieldPath || v.Tool != d.Tool || v.CallID != d.CallID {
		return false
	}
	switch d.Stopping {
	case "", "invalid-call-limit", "tool-call-limit", "model-turn-limit", "wall-time-limit", "context-limit", "budget-limit", "local-error":
	default:
		return false
	}
	return d.Dispatched >= 0 && d.Dispatched <= contracts.MaxSafeInteger && (d.HostFinalized || d.Dispatched == 0 && !d.AssessmentAccepted && !d.CleanupCompleted) && (d.OperationID != "" || d.Omitted) && len(d.Message()) <= 512 && d.Observed >= 0 && d.Observed <= contracts.MaxSafeInteger && d.Limit >= 0 && d.Limit <= contracts.MaxSafeInteger && (d.Stopping == "" || d.Limit > 0)
}
func (d WorkflowDiagnostic) Message() string {
	text := workflowReasons[d.Reason]
	if d.Stopping != "" {
		text = fmt.Sprintf("Harness stopped: %s (observed %d; configured limit %d). %s", d.Stopping, d.Observed, d.Limit, text)
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
	if d.FieldPath != "" {
		v["field_path"] = d.FieldPath
	}
	if d.Omitted {
		v["omissions"] = []any{map[string]any{"field": "identifier", "reason": "sensitive"}}
	}
	raw, _ := json.Marshal(v)
	return raw
}
