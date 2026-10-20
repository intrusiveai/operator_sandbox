package contracts

// ValidateAttemptOrigin never infers origin. A nil scenario set checks structure
// only; the host supplies its submitted scenario set before retaining/dispatching.
func ValidateAttemptOrigin(body map[string]any, scenarios map[string]bool) error {
	origin, _ := body["origin"].(string)
	if origin != "scenario" && origin != "exploratory" {
		return argumentIssue("origin_required", "/origin", "Set origin to scenario with a submitted scenario_id, or exploratory without scenario_id. Execution did not occur; correct and resubmit with a new operation ID.")
	}
	scenario, present := body["scenario_id"]
	if origin == "exploratory" && present {
		return argumentIssue("scenario_forbidden", "/scenario_id", "Exploratory origin forbids scenario_id. Remove it or select scenario origin. Execution did not occur; correction is safe with a new operation ID.")
	}
	if origin == "scenario" {
		id, ok := scenario.(string)
		if !present || !ok || id == "" {
			return argumentIssue("scenario_required", "/scenario_id", "Scenario origin requires a scenario_id from the submitted bundle. Execution did not occur; correction is safe with a new operation ID.")
		}
		if scenarios != nil && !scenarios[id] {
			return argumentIssue("unknown_scenario", "/scenario_id", "Select a scenario_id from the submitted bundle, or exploratory origin without scenario_id. Execution did not occur; correction is safe with a new operation ID.")
		}
	}
	return nil
}
