package contracts

import "encoding/json"

// ModelTokenBudget describes admission failure, not an upstream provider quota.
// Values are Harness-reported accounting facts, never estimated evidence bytes.
type ModelTokenBudget struct {
	Kind                   string `json:"kind"`
	TotalAllowance         int64  `json:"total_allowance"`
	Consumed               int64  `json:"consumed"`
	Remaining              int64  `json:"remaining"`
	NextRequestReservation int64  `json:"next_request_reservation"`
}

func (b ModelTokenBudget) Valid() bool {
	return b.Kind == "model-tokens" && b.TotalAllowance > 0 && b.TotalAllowance <= MaxSafeInteger && b.Consumed >= 0 && b.Consumed <= b.TotalAllowance && b.Remaining == b.TotalAllowance-b.Consumed && b.NextRequestReservation > b.Remaining && b.NextRequestReservation <= MaxSafeInteger
}
func validateDiagnosticBudget(body map[string]any) error {
	stopping, _ := body["stopping"].(map[string]any)
	if stopping["condition"] == "no-progress-limit" && number(stopping["observed_count"]) < number(stopping["configured_limit"]) {
		return argumentIssue("invalid_progress_accounting", "/stopping/observed_count", "No-progress exhaustion requires observed_count to reach configured_limit; report the actual counter.")
	}
	if value, ok := stopping["budget"]; ok {
		raw, _ := json.Marshal(value)
		var budget ModelTokenBudget
		if json.Unmarshal(raw, &budget) != nil || !budget.Valid() {
			return argumentIssue("invalid_budget_accounting", "/stopping/budget", "Model tokens require consumed + remaining = total_allowance and remaining < next_request_reservation; use actual token accounting, not another budget counter.")
		}
	}
	return nil
}
