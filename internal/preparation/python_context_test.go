//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPythonProcessContextWarningAndFinish(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		t.Run(transport, func(t *testing.T) {
			var first string
			reply := func(turn int, q map[string]any) ([]byte, error) {
				switch turn {
				case 1:
					var task map[string]any
					if err := json.Unmarshal([]byte(q["messages"].([]any)[1].(map[string]any)["content"].(string)), &task); err != nil {
						return nil, err
					}
					operation := task["target"].(map[string]any)["capabilities"].(map[string]any)["operations"].([]any)[0].(map[string]any)
					detail := modelCall("detail", "reference_read", map[string]any{"reference_id": operation["details_reference"], "offset": 0})
					var response map[string]any
					_ = json.Unmarshal(processResponse([]any{detail, modelCall("first", "attempt_execute", processAttempt("scenario", "", []byte(`{"query":"baseline"}`), ""))}), &response)
					// A completed native group contributes removable model reasoning.
					response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"] = strings.Repeat("Unverified working notes. ", 800)
					return encode(response), nil
				case 2:
					detail, err := modelToolResult(q, "detail")
					if err != nil || detail["encoding"] != "utf8" || detail["raw_length"] == nil {
						return nil, fmt.Errorf("resource detail read failed")
					}
					attempt, err := modelToolResult(q, "first")
					if err != nil {
						return nil, err
					}
					first, _ = attempt["receipt_id"].(string)
					if first == "" {
						return nil, fmt.Errorf("missing executed receipt")
					}
					feedback := attempt["feedback"].(map[string]any)
					entries := feedback["entries"].([]any)
					entry := entries[0].(map[string]any)
					raw := processResponse([]any{modelCall("observe", "observation_read", map[string]any{"receipt_id": first, "entry_id": entry["entry_id"], "offset": 0})})
					var result map[string]any
					_ = json.Unmarshal(raw, &result)
					result["usage"] = map[string]any{"prompt_tokens": 8200, "completion_tokens": 20, "total_tokens": 8220}
					return encode(result), nil
				case 3:
					if q["tool_choice"] != "none" || !strings.Contains(string(encode(q)), first) {
						return nil, fmt.Errorf("warning did not compact with receipt identity")
					}
					return processText("One scenario attempt executed and its permitted observation was read. Receipt " + first + " remains evidence; compare another input. No objective success is asserted."), nil
				case 4:
					if q["tool_choice"] != "auto" || !strings.Contains(string(encode(q)), "model-generated-not-evidence") {
						return nil, fmt.Errorf("compaction provenance missing")
					}
					return processResponse([]any{modelCall("second", "attempt_execute", processAttempt("scenario", "", []byte(`{"query":"followup"}`), first))}), nil
				case 5:
					second, err := modelToolResult(q, "second")
					if err != nil {
						return nil, err
					}
					receipt, _ := second["receipt_id"].(string)
					if receipt == "" {
						return nil, fmt.Errorf("followup rejected")
					}
					return processResponse([]any{modelCall("finish", "finish", finishDraft(modelDraft([]string{first, receipt}, "")))}), nil
				default:
					return nil, fmt.Errorf("unexpected model call %d", turn)
				}
			}
			run := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: reply, timeout: 90 * time.Second})
			run.finish(t)
			if len(run.provider.requests) != 5 {
				t.Fatal("unexpected model calls", len(run.provider.requests))
			}
			if err := run.writer.Close(); err != nil {
				t.Fatal(err)
			}
			receipt, err := reporting.Generate(context.Background(), run.root, "campaign-1", "")
			if err != nil {
				t.Fatal(err)
			}
			report := reportResult(t, run.root, receipt)
			if len(report.Attempts) != 2 || len(report.ModelContexts) != 5 || len(report.ModelRequests) != 5 || !report.ModelContexts[1].Budget.NearPromptLimit || report.ModelContexts[1].Budget.InputTokens != 8200 {
				t.Fatal("missing correlated context evidence", report.ModelContexts)
			}
			if report.Attempts[0].ScenarioID != "scenario-marker" || report.Attempts[1].ReceiptID == report.Attempts[0].ReceiptID {
				t.Fatal("scenario/replay mismatch")
			}
			// Independently generated report files remain readable after execution cleanup.
			found := false
			_ = filepath.WalkDir(run.root, func(path string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
					b, _ := os.ReadFile(path)
					if strings.Contains(string(b), "Model context usage") && strings.Contains(string(b), "8200") {
						found = true
					}
				}
				return nil
			})
			if !found {
				t.Fatal("human-readable context usage missing")
			}
		})
	}
}
