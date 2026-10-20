//go:build linux || darwin

package preparation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

// Exercise the real Python dispatcher and Go host after provider acceptance.
// Declarations may describe a superset; invalid cross-field combinations must
// never reach record handlers or target execution, and may not refund indexes.
func TestPythonProcessToolSchemaRejections(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		t.Run(transport, func(t *testing.T) {
			var record string
			var completed bool
			hypothesis := map[string]any{"hypothesis_id": "hypothesis-marker", "objective_refs": []string{"objective-marker"}, "provenance": map[string]any{"origin": "exploratory"}, "statement": "Compare a bounded response.", "assumptions": []any{}, "predicted_observations": []any{}, "record_refs": []any{}}
			r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: func(turn int, q map[string]any) ([]byte, error) {
				// Mimic the provider's root-schema restriction on every real request.
				for _, value := range q["tools"].([]any) {
					tool := value.(map[string]any)["function"].(map[string]any)
					parameters := tool["parameters"].(map[string]any)
					if parameters["type"] != "object" {
						return nil, fmt.Errorf("non-object parameters for %s", tool["name"])
					}
					for _, key := range []string{"oneOf", "anyOf", "allOf", "enum", "const", "not"} {
						if _, found := parameters[key]; found {
							return nil, fmt.Errorf("root %s in %s", key, tool["name"])
						}
					}
				}
				switch turn {
				case 1:
					attempt := processAttempt("scenario", "", []byte(`{"query":"baseline"}`), "")
					delete(attempt, "input")
					return processResponse([]any{modelCall("bad-record", "record_append", map[string]any{"record_kind": "progress", "record": hypothesis}), modelCall("bad-attempt", "attempt_execute", attempt)}), nil
				case 2:
					badRecord, err := modelToolResult(q, "bad-record")
					if err != nil || badRecord["code"] != "INVALID_ARGUMENTS" || badRecord["effect_state"] != "none" {
						return nil, fmt.Errorf("record mismatch reached handler: %v %v", badRecord, err)
					}
					badAttempt, err := modelToolResult(q, "bad-attempt")
					if err != nil || badAttempt["code"] != "INVALID_ARGUMENTS" || badAttempt["effect_state"] != "none" || badAttempt["diagnostic"] == nil {
						return nil, fmt.Errorf("attempt condition not enforced locally: %v %v", badAttempt, err)
					}

					return processResponse([]any{modelCall("hypothesis", "record_append", map[string]any{"record_kind": "hypothesis", "record": hypothesis}), publishCall("payload", "baseline")}), nil
				case 3:
					h, err := modelToolResult(q, "hypothesis")
					if err != nil {
						return nil, err
					}
					record, _ = h["receipt_id"].(string)
					if record == "" {
						return nil, fmt.Errorf("valid record rejected: %v", h)
					}
					return processResponse([]any{modelCall("attempt", "attempt_execute", processAttempt("exploratory", record, []byte(`{"query":"baseline"}`), ""))}), nil
				case 4:
					a, err := modelToolResult(q, "attempt")
					if err != nil || a["status"] != "completed" {
						return nil, fmt.Errorf("valid attempt rejected: %v %v", a, err)
					}
					completed = true
					return processResponse([]any{modelCall("finish", "finish", finishDraft(modelDraft([]string{a["receipt_id"].(string)}, record)))}), nil
				default:
					return nil, fmt.Errorf("unexpected extra model turn %d", turn)
				}
			}})
			r.finish(t)
			invocations := 0
			for _, op := range r.native.calls {
				if op == "application.invoke" {
					invocations++
				}
			}
			if !completed || invocations != 1 || len(r.provider.requests) != 4 {
				t.Fatal("invalid call executed or valid call failed", completed, r.native.calls)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := r.service.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if err := r.writer.Close(); err != nil {
				t.Fatal(err)
			}
			receipt, err := reporting.Generate(ctx, r.root, "campaign-1", "")
			if err != nil {
				t.Fatal(err)
			}
			report := reportResult(t, r.root, receipt)
			if len(report.Attempts) != 1 || report.Attempts[0].Index != 1 || report.Attempts[0].State != "succeeded" {
				t.Fatal("rejected submission was executed or host index incorrectly allocated", report.Attempts)
			}
		})
	}
}
