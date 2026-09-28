//go:build linux || darwin

package preparation_test

import (
	"fmt"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
	"testing"
)

func TestPythonProcessRestoreBatchBoundary(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		t.Run(transport, func(t *testing.T) {
			var snapshot map[string]any
			reply := func(turn int, q map[string]any) ([]byte, error) {
				switch turn {
				case 1:
					return processResponse([]any{modelCall("create", "snapshot_request", map[string]any{"label": "baseline", "description": "Try serial variants without losing the harness conversation."})}), nil
				case 2:
					v, e := modelToolResult(q, "create")
					if e != nil {
						return nil, e
					}
					var ok bool
					snapshot, ok = v["snapshot"].(map[string]any)
					if !ok {
						return nil, fmt.Errorf("snapshot failed: %v", v)
					}
					return processResponse([]any{modelCall("list", "snapshot_list", map[string]any{}), modelCall("inspect", "snapshot_inspect", map[string]any{"source_session": snapshot["source_session"], "checkpoint_id": snapshot["checkpoint_id"]})}), nil
				case 3:
					v, e := modelToolResult(q, "inspect")
					if e != nil {
						return nil, e
					}
					if v["snapshot"].(map[string]any)["description"] != snapshot["description"] {
						return nil, fmt.Errorf("snapshot metadata lost")
					}
					return processResponse([]any{modelCall("restore", "restore_request", map[string]any{"source_session": snapshot["source_session"], "checkpoint_id": snapshot["checkpoint_id"]}), modelCall("skipped-attempt", "attempt_execute", map[string]any{"invalid": "must never be decoded"}), modelCall("skipped-read", "snapshot_list", map[string]any{}), modelCall("skipped-stop", "request_stop", map[string]any{})}), nil
				case 4:
					restored, e := modelToolResult(q, "restore")
					if e != nil {
						return nil, e
					}
					if restored["run_revision"] != float64(2) || restored["harness_disposition"] != "continue" {
						return nil, fmt.Errorf("bad restore: %v", restored)
					}
					for _, id := range []string{"skipped-attempt", "skipped-read", "skipped-stop"} {
						v, e := modelToolResult(q, id)
						if e != nil {
							return nil, e
						}
						if v["status"] != "not_executed" || v["code"] != "TARGET_REVISION_CHANGED" || v["transition_receipt"] != restored["transition_receipt"] {
							return nil, fmt.Errorf("bad skipped result %s: %v", id, v)
						}
					}
					return processResponse(nil), nil
				default:
					return nil, fmt.Errorf("unexpected turn %d", turn)
				}
			}
			r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: reply, change: snapshotInput(t)})
			r.finish(t)
			if r.native.restores != 1 || len(r.provider.requests) != 4 {
				t.Fatal("harness restarted or restore repeated")
			}
			for _, op := range r.native.calls {
				if op == "application.invoke" || op == "attempt.register" {
					t.Fatal("skipped effect executed")
				}
			}
		})
	}
}

func TestPythonProcessRestoredInjectionCleanupAndLineage(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		t.Run(transport, func(t *testing.T) {
			payload := []byte(`{"query":"baseline"}`)
			var record string
			var attempt map[string]any
			reply := func(turn int, q map[string]any) ([]byte, error) {
				switch turn {
				case 1:
					return processResponse([]any{modelCall("record", "record_append", map[string]any{"record_kind": "hypothesis", "record": map[string]any{"hypothesis_id": "hypothesis-marker", "objective_refs": []string{"objective-marker"}, "provenance": map[string]any{"origin": "scenario", "scenario_id": "scenario-marker"}, "statement": "Compare retained setup before and after restore.", "assumptions": []string{}, "predicted_observations": []string{}, "record_refs": []string{}}}), publishCall("payload", "baseline"), modelCall("carrier", "artifact_publish", map[string]any{"purpose": "carrier", "media_type": "application/json", "content": map[string]any{"encoding": "json", "value": map[string]any{"query": "baseline"}}})}), nil
				case 2:
					v, e := modelToolResult(q, "record")
					if e != nil {
						return nil, e
					}
					record, _ = v["receipt_id"].(string)
					a := processAttempt("scenario", record, payload, "")
					a["carrier"] = a["payload"]
					a["invocation"].(map[string]any)["input_source"] = "carrier"
					a["cleanup"].(map[string]any)["delete_actions_after_observation"] = false
					a["pre_actions"] = []any{map[string]any{"action_id": "setup", "action_type": "interceptor.injection/v1alpha1", "parameters": map[string]any{"surface": "mcp_tool_result", "mode": "simulation", "scope": "once", "selector": map[string]any{"service": "tickets", "tool_name": "get_ticket", "call_ordinal": "first"}, "placement": map[string]any{"operation": "replace", "pointer": "/structuredContent/description"}, "payload_digest": contracts.RawDigest(payload)}}}
					return processResponse([]any{modelCall("attempt", "attempt_execute", a)}), nil
				case 3:
					var e error
					attempt, e = modelToolResult(q, "attempt")
					if e != nil || attempt["status"] != "completed" {
						return nil, fmt.Errorf("retained attempt: %v %v", attempt, e)
					}
					return processResponse([]any{modelCall("snapshot", "snapshot_request", map[string]any{"description": "Retained injection baseline"})}), nil
				case 4:
					v, e := modelToolResult(q, "snapshot")
					if e != nil {
						return nil, e
					}
					s := v["snapshot"].(map[string]any)
					return processResponse([]any{modelCall("restore", "restore_request", map[string]any{"source_session": s["source_session"], "checkpoint_id": s["checkpoint_id"]})}), nil
				case 5:
					v, e := modelToolResult(q, "restore")
					if e != nil || v["run_revision"] != float64(2) {
						return nil, fmt.Errorf("restore failed %v %v", v, e)
					}
					return processResponse([]any{modelCall("delete", "injection_delete", map[string]any{"attempt_receipt_id": attempt["receipt_id"], "action_id": "setup"}), modelCall("child", "attempt_execute", processAttempt("scenario", record, payload, attempt["attempt_id"].(string)))}), nil
				case 6:
					d, e := modelToolResult(q, "delete")
					if e != nil || d["outcome"] != "deleted" {
						return nil, fmt.Errorf("delete failed %v %v", d, e)
					}
					a, e := modelToolResult(q, "child")
					if e != nil || a["status"] != "completed" {
						return nil, fmt.Errorf("child failed %v %v", a, e)
					}
					return processResponse(nil), nil
				default:
					return nil, fmt.Errorf("unexpected turn %d", turn)
				}
			}
			r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: reply, change: func(in *preparation.Input) {
				snapshotInput(t)(in)
				s := in.Profile.Settings()
				s.Scopes.AllowRetainedInjections = true
				s.Scopes.Routes = []attemptadapter.Route{{Surface: "mcp_tool_result", Target: attemptadapter.Target{Service: "tickets", ToolName: "get_ticket"}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{"/structuredContent/description"}}}
				var e error
				in.Profile, e = targetprofile.Parse(encode(s))
				if e != nil {
					t.Fatal(e)
				}
			}})
			r.finish(t)
			arms, deletes, invocations := 0, 0, 0
			for _, op := range r.native.calls {
				switch op {
				case "injection.arm":
					arms++
				case "injection.delete":
					deletes++
				case "application.invoke":
					invocations++
				}
			}
			// Explicit deletion is verified by the next model turn. Terminal
			// cleanup may also idempotently confirm this known handle's absence.
			if arms != 1 || deletes < 1 || deletes > 2 || invocations != 2 || r.native.restores != 1 {
				t.Fatal("effects or lineage repeated", r.native.calls)
			}
		})
	}
}
