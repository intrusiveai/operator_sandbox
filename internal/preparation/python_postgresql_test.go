//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

// Invoked by scripts/qualify_postgresql.py against its isolated real Interceptor
// PostgreSQL/pgvector container. Model responses are controlled, never paid calls.
func TestPythonPostgreSQLWorkflow(t *testing.T) {
	if os.Getenv("OPERATOR_POSTGRESQL_INTEGRATION") != "1" {
		t.Skip("use scripts/qualify_postgresql.py")
	}
	client := interceptor.New()
	change := func(in *preparation.Input) {
		a, err := client.Attach(t.Context(), "campaign-1", "worker-1", true)
		if err != nil {
			t.Fatal(err)
		}
		s, err := client.Status(t.Context(), "campaign-1")
		if err != nil {
			t.Fatal(err)
		}
		authoring, err := capabilities.FromNative(in.Protocol.Catalog(), a.Capabilities, "delivery-example")
		if err != nil {
			t.Fatal(err)
		}
		profile, err := targetprofile.Parse([]byte(`{"api_version":"operator.dev/target-profile/v1alpha1","id":"database","target_id":"delivery-example","adapter":"interceptor/v1","allow_target_stop":true,"scopes":{"operation_ids":["ask"],"routes":[{"surface":"database_response","target":{"service":"database"},"scopes":["once"],"placements":["replace"],"pointers":["/rows/0/0"]},{"surface":"environment_state","target":{"service":"database","collection":"documents","key":"{\"id\":\"one\"}"},"scopes":["once"],"placements":["replace"],"pointers":["/body"]}],"caller_principal_ids":[],"allow_retained_injections":false},"feedback":{"ceiling":"oracle-assisted","allowed_kinds":["target_output","oracle_outcome","injection_delivery"],"max_attempt_bytes":1048576},"operation_timeout_ms":30000}`))
		if err != nil {
			t.Fatal(err)
		}
		var b map[string]any
		_ = json.Unmarshal(in.Bundle, &b)
		b["target_requirements"] = map[string]any{"target_id": "delivery-example", "capability_source_digest": authoring.SourceDigest(), "required_capability_refs": []string{"operation:ask"}}
		scenario := b["scenarios"].([]any)[0].(map[string]any)
		scenario["required_capability_refs"] = []string{"action:injection:database_response", "service:database"}
		scenario["guidance"].(map[string]any)["action_refs"] = []string{"action:injection:database_response", "action:injection:environment_state"}
		b["feedback"] = map[string]any{"requested_profile": "oracle-assisted"}
		b["evidence"].(map[string]any)["requested_classes"] = []string{"evidence:target_output", "evidence:oracle_outcome", "evidence:injection_delivery"}
		in.Profile, in.Attachment, in.Status, in.InstanceID, in.Authoring, in.Bundle = profile, a, s, s.InstanceID, authoring, encode(b)
	}
	var snapshot, oracleExecution, oracleEntry map[string]any
	var oracleContent string
	receipts := []any{}
	last := map[string]string{}
	sequence := 0
	call := func(id, name string, args any) any {
		sequence++
		last[id] = fmt.Sprintf("%s-%d", id, sequence)
		return modelCall(last[id], name, args)
	}
	attempt := func(message, surface, payload string) map[string]any {
		a := map[string]any{"origin": "scenario", "scenario_id": "scenario-marker", "operation_id": "ask", "input": map[string]any{"encoding": "json", "value": map[string]any{"message": message}}}
		if surface != "" {
			selector := map[string]any{"service": "database"}
			pointer := "/rows/0/0"
			if surface == "environment_state" {
				selector["collection"] = "documents"
				selector["key"] = `{"id":"one"}`
				pointer = "/body"
			} else {
				selector["call_ordinal"] = "first"
			}
			a["injection_input"] = map[string]any{"encoding": "json", "value": payload}
			a["pre_actions"] = []any{map[string]any{"action_id": "setup", "action_type": "interceptor.injection/v1alpha1", "parameters": map[string]any{"surface": surface, "mode": map[bool]string{true: "fidelity", false: "simulation"}[surface == "environment_state"], "scope": "once", "selector": selector, "placement": map[string]any{"operation": "replace", "pointer": pointer}}}}
		}
		return a
	}
	readCall := func(execution, entry map[string]any) []byte {
		return processResponse([]any{call("observe", "observation_read", map[string]any{"receipt_id": execution["receipt_id"], "entry_id": entry["entry_id"], "offset": 0, "max_bytes": 8192})})
	}
	selectEntry := func(q map[string]any, kind string) (map[string]any, map[string]any, error) {
		v, err := modelToolResult(q, last["invoke"])
		if err != nil {
			return nil, nil, err
		}
		receipt, ok := v["receipt_id"].(string)
		if !ok {
			return nil, nil, fmt.Errorf("attempt rejected: %s", encode(v))
		}
		receipts = append(receipts, receipt)
		feedback, ok := v["feedback"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("no feedback")
		}
		for _, entry := range feedback["entries"].([]any) {
			e := entry.(map[string]any)
			if e["kind"] == kind {
				return v, e, nil
			}
		}
		return nil, nil, fmt.Errorf("missing %s: %s", kind, encode(v))
	}
	content := func(q map[string]any) (string, error) {
		v, e := modelToolResult(q, last["observe"])
		if e != nil {
			return "", e
		}
		if v["availability"] != "available" {
			return "", fmt.Errorf("observation unavailable: %s", encode(v))
		}
		raw, ok := v["content"].(string)
		if !ok {
			return "", fmt.Errorf("missing bytes: %s", encode(v))
		}
		b, e := base64.StdEncoding.DecodeString(raw)
		return string(b), e
	}
	reply := func(turn int, q map[string]any) ([]byte, error) {
		t.Logf("controlled model turn %d", turn)
		switch turn {
		case 1:
			return processResponse([]any{call("snapshot", "snapshot_request", map[string]any{"description": "Baseline database and pgvector state"})}), nil
		case 2:
			v, e := modelToolResult(q, last["snapshot"])
			if e != nil {
				return nil, e
			}
			var ok bool
			snapshot, ok = v["snapshot"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("snapshot failed: %s", encode(v))
			}
			return processResponse([]any{call("invoke", "attempt_execute", attempt("continue", "database_response", "database-response-marker"))}), nil
		case 3:
			v, e, err := selectEntry(q, "target_output")
			if err != nil {
				return nil, err
			}
			return readCall(v, e), nil
		case 4:
			text, e := content(q)
			if e != nil {
				return nil, e
			}
			if !strings.Contains(text, "database-response-marker") {
				return nil, fmt.Errorf("response injection absent: %s", text)
			}
			return processResponse([]any{call("invoke", "attempt_execute", attempt("refund", "environment_state", "database-state-marker"))}), nil
		case 5:
			var err error
			oracleExecution, oracleEntry, err = selectEntry(q, "oracle_outcome")
			if err != nil {
				return nil, err
			}
			var output map[string]any
			for _, e := range oracleExecution["feedback"].(map[string]any)["entries"].([]any) {
				entry := e.(map[string]any)
				if entry["kind"] == "target_output" {
					output = entry
				}
			}
			if output == nil {
				return nil, fmt.Errorf("missing state-mutation output")
			}
			return processResponse([]any{
				call("observe", "observation_read", map[string]any{"receipt_id": oracleExecution["receipt_id"], "entry_id": oracleEntry["entry_id"], "offset": 0, "max_bytes": 8192}),
				call("state-output", "observation_read", map[string]any{"receipt_id": oracleExecution["receipt_id"], "entry_id": output["entry_id"], "offset": 0, "max_bytes": 8192}),
			}), nil
		case 6:
			text, e := content(q)
			if e != nil {
				return nil, e
			}
			if !strings.Contains(text, `"fired":true`) || !strings.Contains(text, "database_query_match") {
				return nil, fmt.Errorf("missing live oracle verdict: %s", text)
			}
			observed, err := modelToolResult(q, last["state-output"])
			if err != nil {
				return nil, err
			}
			encoded, ok := observed["content"].(string)
			if !ok {
				return nil, fmt.Errorf("missing state output bytes")
			}
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, err
			}
			var target map[string]any
			if json.Unmarshal(raw, &target) != nil || target["body"] != "database-state-marker" || target["refunds"] != float64(1) {
				return nil, fmt.Errorf("state mutation/business outcome not observed")
			}
			oracleContent = text
			return processResponse([]any{call("invoke", "attempt_execute", attempt("clear_refunds", "", ""))}), nil
		case 7:
			v, _, err := selectEntry(q, "target_output")
			if err != nil {
				return nil, err
			}
			for _, entry := range v["feedback"].(map[string]any)["entries"].([]any) {
				if entry.(map[string]any)["kind"] == "oracle_outcome" {
					return nil, fmt.Errorf("old positive oracle leaked into later attempt")
				}
			}
			return processResponse([]any{call("restore", "restore_request", map[string]any{"source_session": snapshot["source_session"], "checkpoint_id": snapshot["checkpoint_id"]})}), nil
		case 8:
			v, e := modelToolResult(q, last["restore"])
			if e != nil {
				return nil, e
			}
			if v["run_revision"] != float64(2) || v["harness_disposition"] != "continue" {
				return nil, fmt.Errorf("restore failed: %s", encode(v))
			}
			return readCall(oracleExecution, oracleEntry), nil
		case 9:
			text, e := content(q)
			if e != nil {
				return nil, e
			}
			if text != oracleContent {
				return nil, fmt.Errorf("retained verdict changed after database writes/restore")
			}
			return processResponse([]any{call("invoke", "attempt_execute", attempt("rollback", "", ""))}), nil
		case 10:
			v, e, err := selectEntry(q, "target_output")
			if err != nil {
				return nil, err
			}
			for _, entry := range v["feedback"].(map[string]any)["entries"].([]any) {
				if entry.(map[string]any)["kind"] == "oracle_outcome" {
					return nil, fmt.Errorf("rollback manufactured positive oracle evidence")
				}
			}
			return readCall(v, e), nil
		case 11:
			text, e := content(q)
			if e != nil {
				return nil, e
			}
			var target map[string]any
			if json.Unmarshal([]byte(text), &target) != nil || target["body"] != "benign" || target["refunds"] != float64(0) || target["nearest"].([]any)[0] != "one" {
				return nil, fmt.Errorf("database baseline not restored: %s", text)
			}
			return processResponse([]any{call("finish", "finish", map[string]any{"finish_reason": "objectives-addressed", "assessment": map[string]any{"summary": "Controlled database response and state mutations, live oracle evidence, and snapshot restoration were observed.", "objectives": []any{map[string]any{"objective_id": "objective-marker", "outcome": "supported", "summary": "Observed permitted target and oracle feedback; this is not a broader QA verdict.", "attempt_receipt_refs": receipts}}}})}), nil
		}
		return nil, fmt.Errorf("unexpected model turn %d", turn)
	}
	r := newProcessRun(t, processCase{transport: "spool", prompt: "default", nativePeer: client, timeout: 3 * time.Minute, change: change, reply: reply})
	r.finish(t)
	cleanup, err := r.service.Shutdown(context.Background())
	if err != nil {
		t.Fatal("cleanup", cleanup, err)
	}
	if len(cleanup.Evidence) != 2 {
		t.Fatal("missing restore session evidence", cleanup.Evidence)
	}
	for _, e := range cleanup.Evidence {
		if e.State != "complete" {
			t.Fatal("evidence not verified", e)
		}
	}
	if err = r.writer.Close(); err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("OPERATOR_POSTGRESQL_OUTPUT")
	if output == "" {
		output = t.TempDir()
	}
	dest := filepath.Join(output, "export")
	receipt, err := reporting.Generate(t.Context(), r.root, "campaign-1", dest)
	if err != nil {
		t.Fatal(err)
	}
	result := reportResult(t, r.root, receipt)
	if len(result.Attempts) != 4 || result.Assessment == nil || len(result.DatabaseEvidence) != 2 {
		t.Fatal("missing report evidence", len(result.Attempts), len(result.DatabaseEvidence))
	}
	markdown, err := os.ReadFile(filepath.Join(dest, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"database_query_match", "## Database evidence", "observed attempt"} {
		if !strings.Contains(string(markdown), want) {
			t.Fatalf("report missing %q", want)
		}
	}
	summary := map[string]any{"controlled": true, "contract": r.writer.Manifest().Contract, "attempts": len(result.Attempts), "finish_accepted": r.service.StopAccepted(), "assessment_committed": r.service.AssessmentCommitted(), "harness_process_exit_code": r.cmd.ProcessState.ExitCode(), "cleanup": cleanup, "model_calls": len(r.provider.requests)}
	if err = os.WriteFile(filepath.Join(output, "result.json"), encode(summary), 0600); err != nil {
		t.Fatal(err)
	}
	if cleanup.CleanupState != "complete" || cleanup.Closure != "confirmed" {
		t.Fatal("cleanup", cleanup)
	}
}
