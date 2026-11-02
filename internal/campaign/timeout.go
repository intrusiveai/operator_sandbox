package campaign

import (
	"fmt"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"slices"
)

// AttemptTimeout contains observed facts only; it never contains target content.
// Delivery is a host transport observation, not proof the Harness consumed a reply.
type AttemptTimeout struct {
	NativeOperation string `json:"native_operation"`
	AllowanceMS     int64  `json:"attempt_allowance_ms"`
	ElapsedMS       int64  `json:"elapsed_ms"`
	DeadlineSource  string `json:"deadline_source"`
	Observation     string `json:"observation_collection"`
}

func (d AttemptTimeout) Valid() bool {
	return slices.Contains([]string{"session.status", "artifact.register", "attempt.register", "injection.arm", "application.invoke", "observation.read", "observation.content.read", "injection.delete", "https.invoke"}, d.NativeOperation) && d.AllowanceMS > 0 && d.AllowanceMS <= 1800000 && d.ElapsedMS >= 0 && d.ElapsedMS <= contracts.MaxSafeInteger && slices.Contains([]string{"attempt", "request", "campaign"}, d.DeadlineSource) && slices.Contains([]string{"not-started", "incomplete", "complete"}, d.Observation)
}
func (d AttemptTimeout) Message(invocation string) string {
	stage := "target execution"
	if d.Observation == "incomplete" {
		stage = "observation collection"
	}
	if d.NativeOperation == "injection.delete" {
		stage = "injection cleanup"
	}
	if d.NativeOperation == "session.status" || d.NativeOperation == "artifact.register" || d.NativeOperation == "attempt.register" || d.NativeOperation == "injection.arm" {
		stage = "setup"
	}
	return fmt.Sprintf("Attempt timed out during %s. Operation: %s. Target invocation: %s. Observation collection: %s. Attempt allowance: %d ms; elapsed: %d ms; limiting deadline: %s. Execution is closed; the invocation will not be replayed.", stage, d.NativeOperation, invocation, d.Observation, d.AllowanceMS, d.ElapsedMS, d.DeadlineSource)
}
