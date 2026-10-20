package contracts

import (
	"strconv"
	"strings"
)

// SafeArgumentPath permits only contract field names, never arbitrary input keys.
// A missing path is explicit; error messages must not echo JSON validator text.
func SafeArgumentPath(path string) (string, bool) {
	if len(path) > 256 {
		return "", false
	}
	if path == "" {
		return "", true
	}
	if !strings.HasPrefix(path, "/") {
		return "", false
	}
	allowed := " name arguments input injection_input value encoding text data media_type artifact_receipt operation_id scenario_id parent_attempt_receipt_id caller_principal_id pre_actions action_id action_type parameters surface mode scope selector placement operation pointer retain_injections observation_selection rationale strategy_provenance_ref technique_refs finish_reason assessment unavailable reason status summary objectives objective_id outcome hypotheses hypothesis_id objective_refs provenance origin parent_hypothesis_id claims claim_id interpretation assurance confidence attempt_receipt_refs attempt_receipt_id observation_refs entry_id range offset length uncertainty_refs coverage alternative uncertainties gap_id description input_refs record_refs record_kind record payload carrier digest generation thread_id request_id attempt_id attempt_index binding api_version kind artifact record_receipt tool_name call_ordinal method path predicates equals contains max_bytes receipt_id reference_id source_session checkpoint_id label continuation limit "
	for _, part := range strings.Split(path[1:], "/") {
		if n, err := strconv.Atoi(part); err == nil && n >= 0 && n < 10000 {
			continue
		}
		if !strings.Contains(allowed, " "+part+" ") {
			return "", false
		}
	}
	return path, true
}
