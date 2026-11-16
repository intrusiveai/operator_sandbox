//go:build linux || darwin

package campaign

// FailureTrigger links the first observed terminal operation failure to its
// existing diagnostic. It is host evidence, not a new Harness wire message.
// Absence in older records means causal order was not retained.
type FailureTrigger struct {
	Kind        string `json:"kind"`
	OperationID string `json:"operation_id"`
}

func (f FailureTrigger) Valid() bool {
	id, why := diagnosticID(f.OperationID)
	return (f.Kind == "model" || f.Kind == "workflow") && f.OperationID != "" && id == f.OperationID && why == ""
}
