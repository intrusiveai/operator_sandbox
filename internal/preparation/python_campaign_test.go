//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func modelCall(id, name string, args any) any {
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(encode(args))}}
}
func modelToolResult(q map[string]any, id string) (map[string]any, error) {
	for _, item := range q["messages"].([]any) {
		m := item.(map[string]any)
		if m["role"] == "tool" && m["tool_call_id"] == id {
			var result map[string]any
			err := json.Unmarshal([]byte(m["content"].(string)), &result)
			return result, err
		}
	}
	return nil, fmt.Errorf("missing correlated result %s", id)
}
func modelDraft(receipts []string, record string) map[string]any {
	refs := []string{}
	if record != "" {
		refs = append(refs, record)
	}
	return map[string]any{"status": "completed", "finish_reason": "objectives-addressed", "summary": "Observed bounded application responses; no internal effect is asserted.", "objectives": []any{map[string]any{"objective_id": "objective-marker", "outcome": "inconclusive", "summary": "Variants completed with external response evidence only.", "attempt_receipt_refs": receipts, "record_refs": refs}}, "hypotheses": []any{}, "claims": []any{}, "coverage": []any{map[string]any{"objective_id": "objective-marker", "status": "tested", "reason": "Compared two serial variants.", "attempt_receipt_refs": receipts}}, "uncertainties": []any{}, "record_refs": refs}
}
func processAttempt(origin, record string, raw []byte, parent string) map[string]any {
	q := map[string]any{"origin": origin, "thread_id": "thread-process", "generation": 1, "payload": map[string]any{"digest": contracts.RawDigest(raw), "size_bytes": len(raw), "media_type": "application/json", "canonicalization": "jcs-v1"}, "pre_actions": []any{}, "invocation": map[string]any{"operation_id": "invoke", "input_source": "payload", "media_type": "application/json"}, "cleanup": map[string]any{"delete_actions_after_observation": true}, "strategy_provenance_ref": record, "rationale": "Compare a bounded input variant against observed feedback."}
	if origin == "scenario" {
		q["scenario_id"] = "scenario-marker"
	}
	if parent != "" {
		q["parent_attempt_id"] = parent
		q["generation"] = 2
	}
	return q
}
func publishCall(id, query string) any {
	return modelCall(id, "artifact_publish", map[string]any{"purpose": "payload", "media_type": "application/json", "content": map[string]any{"encoding": "json", "value": map[string]any{"query": query}}})
}

func TestPythonProcessCampaigns(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		for _, mode := range []string{"objectives-only", "scenario", "exploratory", "https"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				origin := "exploratory"
				if mode == "scenario" {
					origin = "scenario"
				}
				var record string
				var attempts []map[string]any
				var reads int
				reply := func(turn int, q map[string]any) ([]byte, error) {
					calls := []any{}
					switch turn {
					case 1:
						provenance := map[string]any{"origin": origin}
						if origin == "scenario" {
							provenance["scenario_id"] = "scenario-marker"
						}
						calls = append(calls, modelCall("hypothesis", "record_append", map[string]any{"record_kind": "hypothesis", "record": map[string]any{"hypothesis_id": "hypothesis-marker", "objective_refs": []string{"objective-marker"}, "provenance": provenance, "statement": "A changed input may affect the response.", "assumptions": []string{}, "predicted_observations": []string{"A permitted response."}, "record_refs": []string{}}}), publishCall("payload-1", "baseline"))
						var task map[string]any
						_ = json.Unmarshal([]byte(q["messages"].([]any)[1].(map[string]any)["content"].(string)), &task)
						for _, v := range task["reference_handles"].([]any) {
							e := v.(map[string]any)
							if e["role"] == "reference" {
								calls = append(calls, modelCall("reference", "reference_read", map[string]any{"reference_id": e["entry_id"], "offset": 0, "max_bytes": 4096}))
								break
							}
						}
					case 2:
						h, e := modelToolResult(q, "hypothesis")
						if e != nil {
							return nil, e
						}
						record, _ = h["receipt_id"].(string)
						if record == "" {
							return nil, fmt.Errorf("hypothesis rejected: %v", h)
						}
						ref, e := modelToolResult(q, "reference")
						if e != nil || ref["raw_length"] == nil {
							return nil, fmt.Errorf("reference not read: %v %v", ref, e)
						}
						calls = append(calls, modelCall("attempt-1", "attempt_execute", processAttempt(origin, record, []byte(`{"query":"baseline"}`), "")))
					case 3, 5:
						id := "attempt-1"
						if turn == 5 {
							id = "attempt-2"
						}
						a, e := modelToolResult(q, id)
						if e != nil {
							return nil, e
						}
						if a["status"] != "completed" {
							return nil, fmt.Errorf("attempt failed: %v", a)
						}
						attempts = append(attempts, a)
						entries := a["feedback"].(map[string]any)["entries"].([]any)
						if len(entries) != 1 {
							return nil, fmt.Errorf("feedback missing: %v", a)
						}
						calls = append(calls, modelCall("read-"+id, "observation_read", map[string]any{"receipt_id": a["receipt_id"], "entry_id": entries[0].(map[string]any)["entry_id"], "offset": 0, "max_bytes": 4096}))
					case 4, 6:
						id := "read-attempt-1"
						if turn == 6 {
							id = "read-attempt-2"
						}
						o, e := modelToolResult(q, id)
						if e != nil {
							return nil, e
						}
						raw, e := base64.StdEncoding.DecodeString(fmt.Sprint(o["content"]))
						if e != nil || string(raw) != `{"answer":"test"}` {
							return nil, fmt.Errorf("wrong observation bytes: %v", o)
						}
						reads++
						if turn == 4 {
							calls = append(calls, publishCall("payload-2", "refined"), modelCall("attempt-2", "attempt_execute", processAttempt(origin, record, []byte(`{"query":"refined"}`), attempts[0]["attempt_id"].(string))))
						} else {
							calls = append(calls, modelCall("finish", "request_stop", modelDraft([]string{attempts[0]["receipt_id"].(string), attempts[1]["receipt_id"].(string)}, record)))
						}
					default:
						return nil, fmt.Errorf("unexpected model turn %d", turn)
					}
					return processResponse(calls), nil
				}
				var server *httptest.Server
				if mode == "https" {
					server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"answer":{"answer":"test"}}`)) }))
					defer server.Close()
				}
				r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: reply, change: func(in *preparation.Input) {
					if server != nil {
						httpsInput(t, in, server)
					}
					if mode == "objectives-only" {
						var b map[string]any
						_ = json.Unmarshal(in.Bundle, &b)
						b["scenarios"] = []any{}
						in.Bundle = encode(b)
					}
				}})
				r.finish(t)
				if reads != 2 || len(attempts) != 2 || len(r.provider.requests) != 6 {
					t.Fatal(reads, len(attempts), len(r.provider.requests))
				}
				if attempts[0]["attempt_id"] == attempts[1]["attempt_id"] {
					t.Fatal("attempt identity reused")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if _, e := r.service.Shutdown(ctx); e != nil {
					t.Fatal(e)
				}
				if e := r.writer.Close(); e != nil {
					t.Fatal(e)
				}
				receipt, e := reporting.Generate(ctx, r.root, "campaign-1", "")
				if e != nil {
					t.Fatal(e)
				}
				report := reportResult(t, r.root, receipt)
				if len(report.Attempts) != 2 || report.Attempts[1].State != "succeeded" {
					t.Fatal(report)
				}
				if mode == "https" && report.Assurance != "declared-observer" {
					t.Fatal("HTTPS assurance", report.Assurance)
				}
				if slices.Contains(r.native.calls, "injection.arm") {
					t.Fatal("unexpected injection")
				}
			})
		}
	}
}
