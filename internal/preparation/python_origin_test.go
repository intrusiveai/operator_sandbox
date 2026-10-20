//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func TestPythonExplicitOriginWorkflow(t *testing.T) {
	for _, lane := range []string{"spool", "fifo"} {
		for _, mode := range []string{"scenario", "corrected", "exploratory"} {
			t.Run(lane+"/"+mode, func(t *testing.T) {
				var execution map[string]any
				readDone := false
				reply := func(turn int, q map[string]any) ([]byte, error) {
					if mode == "corrected" {
						if turn == 1 {
							return processResponse([]any{modelCall("invalid-origin", "attempt_execute", map[string]any{"operation_id": "invoke", "input": map[string]any{"encoding": "json", "value": map[string]any{"query": "marker"}}})}), nil
						}
						if turn == 2 {
							bad, err := modelToolResult(q, "invalid-origin")
							if err != nil {
								return nil, err
							}
							encoded := string(encode(bad))
							if !strings.Contains(encoded, "origin_required") || !strings.Contains(encoded, "/origin") {
								return nil, fmt.Errorf("missing origin diagnostic")
							}
						}
						turn--
					}
					switch turn {
					case 1:
						origin := "scenario"
						if mode == "exploratory" {
							origin = "exploratory"
						}
						return processResponse([]any{modelCall("invoke", "attempt_execute", processAttempt(origin, "", []byte(`{"query":"marker"}`), ""))}), nil
					case 2:
						var err error
						execution, err = modelToolResult(q, "invoke")
						if err != nil {
							return nil, err
						}
						feedback, ok := execution["feedback"].(map[string]any)
						if !ok {
							return nil, fmt.Errorf("missing execution feedback")
						}
						entry := feedback["entries"].([]any)[0].(map[string]any)
						return processResponse([]any{modelCall("observe", "observation_read", map[string]any{"receipt_id": execution["receipt_id"], "entry_id": entry["entry_id"], "offset": 0, "max_bytes": 2048})}), nil
					case 3:
						observed, err := modelToolResult(q, "observe")
						if err != nil {
							return nil, err
						}
						if observed["availability"] != "available" {
							return nil, fmt.Errorf("missing observation bytes")
						}
						readDone = true
						return processResponse([]any{modelCall("finish", "finish", map[string]any{"finish_reason": "objectives-addressed", "assessment": map[string]any{"summary": "Observed the controlled target output.", "objectives": []any{map[string]any{"objective_id": "objective-marker", "outcome": "supported", "summary": "Target returned a response.", "attempt_receipt_refs": []any{execution["receipt_id"]}}}}})}), nil
					}
					return nil, fmt.Errorf("unexpected model turn; finish should exit")
				}
				r := newProcessRun(t, processCase{transport: lane, prompt: "default", reply: reply})
				r.finish(t)
				if !readDone || r.cmd.ProcessState.ExitCode() != 0 || !r.service.AssessmentCommitted() {
					t.Fatal("workflow incomplete")
				}
				n := 0
				for _, call := range r.native.calls {
					if call == "application.invoke" {
						n++
					}
				}
				if n != 1 {
					t.Fatal("duplicate dispatch", n)
				}
				cleanup, err := r.service.Shutdown(context.Background())
				if err != nil || cleanup.Closure != "confirmed" || cleanup.CleanupState != "complete" {
					t.Fatal(cleanup, err)
				}
				if err = r.writer.Close(); err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(t.TempDir(), "export")
				receipt, err := reporting.Generate(context.Background(), r.root, "campaign-1", destination)
				if err != nil {
					t.Fatal(err)
				}
				result := reportResult(t, r.root, receipt)
				if len(result.Attempts) != 1 || result.Assessment == nil || len(result.Observations) != 1 || result.Observations[0].State != "available" {
					t.Fatal("missing exported workflow facts", result.Attempts, result.Observations)
				}
				if mode == "exploratory" {
					if result.Attempts[0].Origin != "exploratory" || len(result.UntestedScenarios) != 1 {
						t.Fatal("exploratory coverage changed")
					}
				} else {
					if result.Attempts[0].Origin != "scenario" || len(result.UntestedScenarios) != 0 {
						t.Fatal("scenario coverage lost")
					}
				}
				raw, err := os.ReadFile(filepath.Join(destination, "report.md"))
				if err != nil || !strings.Contains(string(raw), "Observed the controlled target output.") {
					t.Fatal("missing assessment", err)
				}
				// This is an actual Harness OS process with path relocation, not Docker exit
				// qualification. Native Docker lifecycle is verified by TestNativeDockerFinishExit.
				summary, _ := json.Marshal(map[string]any{"origin": result.Attempts[0].Origin, "attempts": n, "finish": r.service.StopAccepted(), "exit_code": r.cmd.ProcessState.ExitCode(), "cleanup": cleanup.CleanupState})
				t.Log(string(summary))
			})
		}
	}
}
