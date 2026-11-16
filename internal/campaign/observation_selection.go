//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"sync"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

var selectionCatalog = sync.OnceValues(func() (*contracts.Catalog, error) { return contracts.LoadCatalog(schemas.Files) })

// DecodeObservationSelections projects only bounded reference facts, never
// submitted prose. Host admission separately checks the referenced evidence.
func DecodeObservationSelections(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	v, err := contracts.Decode(raw, 65536)
	if err != nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok || len(list) == 0 || len(list) > 32 {
		return nil
	}
	catalog, err := selectionCatalog()
	if err != nil {
		return nil
	}
	// Reuse the same closed schema at retention and export boundaries.
	body := map[string]any{"observation_selections": list, "diagnostic": map[string]any{"source": "harness-local", "stage": "references", "reason": "observation_selection", "message": "Selected observation references.", "execution_occurred": "no", "resubmission_safe": true}}
	encoded, _ := json.Marshal(body)
	if _, err = catalog.Validate(contracts.EngineDiagnosticReportRequestSchema, encoded, 65536); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}
