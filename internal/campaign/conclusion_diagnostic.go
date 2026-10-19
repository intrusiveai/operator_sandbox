//go:build linux || darwin

package campaign

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// ConclusionDiagnostic is host evidence about rejection, never a copy of guest
// prose. Value-only fields keep saved completion records comparable and immutable.
// FieldPath is a JSON Pointer into the conclusion (or the identified request).
type ConclusionDiagnostic struct {
	OperationID       string `json:"operation_id,omitempty"`
	Stage             string `json:"stage"`
	Reason            string `json:"reason"`
	FieldPath         string `json:"field_path"`
	Identifier        string `json:"identifier,omitempty"`
	OperationOmitted  string `json:"operation_omitted,omitempty"`
	PathOmitted       string `json:"path_omitted,omitempty"`
	IdentifierOmitted string `json:"identifier_omitted,omitempty"`
}

var conclusionReasons = map[string][2]string{
	"assessment_budget_exceeded":   {"artifact", "The assessment cannot be retained within the remaining artifact allowance. Use an already committed assessment receipt or finish with an explicit unavailable reason."},
	"evidence_required":            {"evidence", "A supported outcome requires an execution receipt. Use an honest untested or inconclusive outcome when no evidence exists."},
	"invalid_structure":            {"structure", "The conclusion does not match its required structure."},
	"duplicate_entry":              {"structure", "Entry identifiers must be unique within their array."},
	"duplicate_hypothesis":         {"hypotheses", "A local hypothesis ID is defined more than once."},
	"self_parent":                  {"hypotheses", "A hypothesis cannot name itself as its parent."},
	"hypothesis_cycle":             {"hypotheses", "The hypothesis parent chain contains a cycle."},
	"coverage_status":              {"structure", "A completed conclusion cannot declare a coverage-limit gap."},
	"observation_attempt_mismatch": {"references", "The observation's attempt must appear in the associated attempt references."},
	"unresolved_uncertainty":       {"references", "The uncertainty reference is absent from the conclusion."},
	"binding_mismatch":             {"binding", "The conclusion binding does not match the campaign's current launch and revision."},
	"finish_reason_mismatch":       {"binding", "The finish reason does not match the committed conclusion."},
	"artifact_receipt_invalid":     {"artifact", "The receipt does not identify a conclusion artifact in this campaign."},
	"artifact_not_committed":       {"artifact", "The conclusion artifact has not been committed."},
	"artifact_integrity_failed":    {"artifact", "The conclusion bytes do not match the committed artifact."},
	"record_receipt_invalid":       {"receipts", "The receipt does not identify a retained conclusion record in this campaign."},
	"completion_chain_invalid":     {"receipts", "The retained artifact, conclusion record and stop request do not form a matching completion chain."},
	"unresolved_hypothesis":        {"references", "The hypothesis reference is absent from both this conclusion and this campaign's hypothesis records."},
	"unresolved_record":            {"references", "The record reference is absent from this campaign's retained records."},
	"invalid_attempt":              {"references", "The reference does not identify a published attempt in this campaign."},
	"invalid_observation":          {"references", "The observation entry does not belong to the referenced attempt."},
	"observation_not_visible":      {"references", "The observation is not visible under the campaign's feedback policy."},
	"evidence_unavailable":         {"evidence", "The required retained evidence is unavailable or failed integrity verification."},
	"invalid_range":                {"evidence", "The byte range exceeds available evidence or the permitted integer range."},
	"unknown_input":                {"references", "The input entry is absent from this campaign's input manifest."},
	"unknown_objective":            {"references", "The objective does not belong to the submitted campaign bundle."},
	"unknown_scenario":             {"references", "The scenario does not belong to the submitted campaign bundle."},
	"unknown_surface":              {"references", "The surface is absent from the target capabilities."},
}
var conclusionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var conclusionSecret = regexp.MustCompile(`(?i)(sk-|AIza|AKIA|ASIA|eyJ|password|secret|token:|api.key:|credential|bearer)`)
var conclusionIndex = regexp.MustCompile(`^(0|[1-9][0-9]{0,3})$`)

func conclusionSafePath(path string) bool {
	if len(path) > 160 {
		return false
	}
	if path == "" {
		return true
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	allowed := " api_version kind binding campaign_id launch_id run_revision input_tree_digest engine_context_digest prompt_digest skill_set_digest image_digest release_record_digest contract_package_version contract_package_digest status finish_reason summary objectives objective_id objective_refs hypotheses hypothesis_id hypothesis_refs provenance origin scenario_id parent_hypothesis_id surface_ref outcome claims claim_id statement interpretation confidence assurance attempt_receipt_refs attempt_receipt_id parent_attempt_receipt_id observation_refs entry_id range offset length uncertainty_refs coverage alternative reason uncertainties gap_id description input_refs record_refs record_kind record artifact_receipt record_receipt assessment unavailable conclusion artifact purpose digest size_bytes media_type attribution "
	for _, part := range strings.Split(path[1:], "/") {
		if !conclusionIndex.MatchString(part) && !strings.Contains(allowed, " "+part+" ") {
			return false
		}
	}
	return true
}
func diagnosticID(id string) (string, string) {
	if len(id) > 128 {
		return "", "size_limit"
	}
	if !conclusionID.MatchString(id) {
		return "", "invalid_identifier"
	}
	if conclusionSecret.MatchString(id) {
		return "", "credential_like"
	}
	return id, ""
}
func NewConclusionDiagnostic(operation string, issue *contracts.ConclusionError) ConclusionDiagnostic {
	reason := issue.Reason
	fact, ok := conclusionReasons[reason]
	if !ok {
		reason = "invalid_structure"
		fact = conclusionReasons[reason]
	}
	d := ConclusionDiagnostic{Stage: fact[0], Reason: reason}
	d.OperationID, d.OperationOmitted = diagnosticID(operation)
	if conclusionSafePath(issue.FieldPath) {
		d.FieldPath = issue.FieldPath
	} else {
		d.PathOmitted = "unsafe_or_oversized"
	}
	if issue.Identifier != "" {
		d.Identifier, d.IdentifierOmitted = diagnosticID(issue.Identifier)
	}
	return d
}
func (d ConclusionDiagnostic) Valid() bool {
	fact, ok := conclusionReasons[d.Reason]
	if !ok || d.Stage != fact[0] || !conclusionSafePath(d.FieldPath) {
		return false
	}
	for _, field := range [][2]string{{d.OperationID, d.OperationOmitted}, {d.Identifier, d.IdentifierOmitted}} {
		if field[0] != "" {
			v, reason := diagnosticID(field[0])
			if v != field[0] || reason != "" || field[1] != "" {
				return false
			}
		}
		switch field[1] {
		case "", "size_limit", "invalid_identifier", "credential_like":
		default:
			return false
		}
	}
	return (d.OperationID != "" || d.OperationOmitted != "") && (d.PathOmitted == "" || d.PathOmitted == "unsafe_or_oversized" && d.FieldPath == "") && len(d.Message()) <= 512
}
func (d ConclusionDiagnostic) Message() string {
	text := "Conclusion validation failed: " + conclusionReasons[d.Reason][1]
	if d.FieldPath != "" {
		text += " Field: " + d.FieldPath + "."
	} else if d.PathOmitted != "" {
		text += " Field omitted (" + d.PathOmitted + ")."
	}
	if d.Identifier != "" {
		text += fmt.Sprintf(" Reference: %q.", d.Identifier)
	} else if d.IdentifierOmitted != "" {
		text += " Reference omitted (" + d.IdentifierOmitted + ")."
	}
	return text
}
func (d ConclusionDiagnostic) Error() string { return d.Message() }
func DecodeConclusionDiagnostic(raw []byte) *ConclusionDiagnostic {
	if _, err := contracts.Decode(raw, 8192); err != nil {
		return nil
	}
	var d ConclusionDiagnostic
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || !d.Valid() {
		return nil
	}
	return &d
}
