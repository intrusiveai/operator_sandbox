//go:build linux || darwin

package preparation_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real Harness process and FIFO transport; provider and target are controlled.
func TestPythonUnsupportedFieldRepairAndExport(t *testing.T) {
	var execution, citation string
	r := newProcessRun(t, processCase{transport: "fifo", prompt: "default", reply: func(turn int, q map[string]any) ([]byte, error) {
		switch turn {
		case 1:
			return processResponse([]any{modelCall("attempt", "attempt_execute", processAttempt("scenario", "", []byte(`{"query":"baseline"}`), ""))}), nil
		case 2:
			a, e := modelToolResult(q, "attempt")
			if e != nil {
				return nil, e
			}
			if a["status"] != "completed" {
				return nil, fmt.Errorf("attempt: %v", a)
			}
			execution = a["execution"].(string)
			observation := a["observations"].([]any)[0].(map[string]any)["observation"]
			return processResponse([]any{modelCall("read", "observation_read", map[string]any{"observation": observation})}), nil
		case 3, 4:
			if turn == 3 {
				read, e := modelToolResult(q, "read")
				if e != nil {
					return nil, e
				}
				citation, _ = read["citation"].(string)
				if citation == "" {
					return nil, fmt.Errorf("citation missing: %v", read)
				}
			}
			if turn == 4 {
				rejected, e := modelToolResult(q, "finish-bad")
				if e != nil {
					return nil, e
				}
				d, ok := rejected["diagnostic"].(map[string]any)
				if !ok || d["field_path"] != "/claims/0" || d["execution_occurred"] != "no" || d["resubmission_safe"] != true {
					return nil, fmt.Errorf("immediate diagnostic lost: %v", rejected)
				}
				text := fmt.Sprint(d["correction"])
				for _, want := range []string{`"references"`, `"observations"`, "Inspected observation citation handles", "Required.", "Remove unsupported fields"} {
					if !strings.Contains(text, want) {
						return nil, fmt.Errorf("missing %s in %s", want, text)
					}
				}
			}
			draft := modelDraft([]string{execution}, "")
			claim := map[string]any{"statement": "The system returned an observed response.", "interpretation": "supported", "confidence": "high", "assurance": "direct-observation", "attempts": []string{execution}}
			key, call := "references", "finish-bad"
			if turn == 4 {
				key, call = "observations", "finish-good"
			}
			claim[key] = []string{citation}
			draft["claims"] = []any{claim}
			return processResponse([]any{modelCall(call, "finish", draft)}), nil
		}
		return nil, fmt.Errorf("unexpected turn %d", turn)
	}})
	r.finish(t)
	if !r.service.AssessmentCommitted() || r.cmd.ProcessState.ExitCode() != 0 {
		t.Fatal("finish/exit incomplete")
	}
	r.native.mu.Lock()
	invocations := 0
	for _, op := range r.native.calls {
		if op == "application.invoke" {
			invocations++
		}
	}
	closed := r.native.closed
	r.native.mu.Unlock()
	if invocations != 1 || !closed {
		t.Fatal("replayed target or cleanup incomplete", invocations, closed)
	}
	d := r.service.WorkflowFailure()
	if d == nil || d.UnsupportedFields == nil || len(d.UnsupportedFields.Names) != 1 || d.UnsupportedFields.Names[0] != "references" {
		t.Fatal("retained correction lost", d)
	}
	if err := r.writer.Close(); err != nil {
		t.Fatal(err)
	}
	binary := processCLI(t)
	run := func(args ...string) []byte {
		t.Helper()
		out, e := exec.Command(binary, args...).CombinedOutput()
		if e != nil {
			t.Fatal(e, string(out))
		}
		return out
	}
	raw := run("inspect", "--state-root", r.root, "--campaign", "campaign-1")
	if !strings.Contains(string(raw), "unsupported_fields") || !strings.Contains(string(raw), "Inspected observation citation handles") {
		t.Fatal("inspection lost details", string(raw))
	}
	destination := filepath.Join(t.TempDir(), "evidence")
	run("export", "--state-root", r.root, "--campaign", "campaign-1", "--output", destination)
	report, e := os.ReadFile(filepath.Join(destination, "report.md"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(report), "references") || !strings.Contains(string(report), "Inspected observation citation handles") {
		t.Fatal("report lost details", string(report))
	}
	manifest, e := os.ReadFile(filepath.Join(destination, "result-manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var retained map[string]any
	if json.Unmarshal(manifest, &retained) != nil || !strings.Contains(string(manifest), "unsupported_fields") {
		t.Fatal("export lost details")
	}
}
