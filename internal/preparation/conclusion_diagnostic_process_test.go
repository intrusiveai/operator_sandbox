//go:build linux || darwin

package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

// The production Python entrypoint submits a local hypothesis through real
// FIFO/spool messages. Model and target peers are local controlled fixtures.
func TestPythonProcessConclusionAndSeparateInspection(t *testing.T) {
	if os.Getenv("OPERATOR_HARNESS_INTEGRATION") != "1" {
		t.Skip("opt-in Harness process test")
	}
	binary := processCLI(t)
	for _, transport := range []string{"fifo", "spool"} {
		for _, mode := range []string{"accepted", "local-corrected", "host-corrected"} {
			invalid := mode != "accepted"
			hostRejected := mode == "host-corrected"
			// Detailed correction is covered on FIFO; accepted finish remains covered on both.
			if transport == "spool" && invalid {
				continue
			}
			name := transport + "/" + mode
			t.Run(name, func(t *testing.T) {
				r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: func(turn int, request map[string]any) ([]byte, error) {
					if turn != 1 && !(invalid && turn == 2) {
						return nil, fmt.Errorf("unexpected model turn after finish")
					}
					if turn == 2 {
						rejected, err := modelToolResult(request, "finish-1")
						if err != nil {
							return nil, err
						}
						if rejected["status"] != "error" || rejected["disposition"] != "correct-and-resubmit" {
							return nil, fmt.Errorf("missing correlated finish rejection: %v", rejected)
						}
						repair, err := modelAssessmentRepair(request, "finish-1")
						if err != nil {
							return nil, err
						}
						source, reason, path := "harness-local", "unknown_handle", "/hypotheses/0/parent"
						if hostRejected {
							source, reason, path = "operator", "unknown_objective", "/hypotheses/0/objective_refs/0"
						}
						diagnostic, ok := repair["diagnostic"].(map[string]any)
						if !ok || diagnostic["source"] != source || diagnostic["reason"] != reason || diagnostic["field_path"] != path || diagnostic["execution_occurred"] != "no" || diagnostic["resubmission_safe"] != true {
							return nil, fmt.Errorf("missing actionable repair diagnostic: %v", repair)
						}
						guidance := fmt.Sprint(diagnostic["message"], diagnostic["correction"])
						if !strings.Contains(guidance, map[bool]string{false: "handle returned", true: "submitted campaign bundle"}[hostRejected]) {
							return nil, fmt.Errorf("missing repair guidance: %s", guidance)
						}
					}
					h := map[string]any{"label": "hyp-configuration-1", "origin": "exploratory", "outcome": "not-tested", "summary": "A bounded hypothesis."}
					if invalid && turn == 1 {
						if hostRejected {
							h["objective_refs"] = []string{"objective-missing"}
						} else {
							h["parent"] = "hyp-missing"
						}
					}
					draft := modelDraft([]string{}, "")
					draft["objectives"] = []any{}
					draft["coverage"] = []any{}
					draft["status"] = "partial"
					draft["finish_reason"] = "no-useful-next-experiment"
					draft["hypotheses"] = []any{h}
					return processResponse([]any{modelCall(fmt.Sprintf("finish-%d", turn), "finish", finishDraft(draft))}), nil
				}})
				r.finish(t)
				if r.cmd.ProcessState.ExitCode() != 0 || !r.service.AssessmentCommitted() {
					t.Fatal("assessment/exit incomplete")
				}
				if e := r.writer.Close(); e != nil {
					t.Fatal(e)
				}
				expectedCalls := 1
				if invalid {
					expectedCalls = 2
				}
				if len(r.provider.requests) != expectedCalls {
					t.Fatal("unexpected model calls")
				}
				r.native.mu.Lock()
				closed := r.native.closed
				invocations := 0
				for _, call := range r.native.calls {
					if call == "application.invoke" {
						invocations++
					}
				}
				r.native.mu.Unlock()
				if !closed || invocations != 0 {
					t.Fatal("repair must close target without invoking it", closed, invocations)
				}
				if err := r.channel.Close(); err != nil {
					t.Fatal(err)
				}
				if err := r.channel.CleanupAfterExit(true); err != nil {
					t.Fatal(err)
				}
				if err := r.tree.Discard(); err != nil {
					t.Fatal(err)
				}
				if e := r.service.Admit(context.Background(), r.launch.Inputs); e == nil {
					t.Fatal("resumed campaign")
				}
				d := r.service.ConclusionFailure()
				if hostRejected && (d == nil || d.Reason != "unknown_objective" || d.Identifier != "objective-missing" || d.FieldPath != "/hypotheses/0/objective_refs/0") {
					t.Fatal(d, r.output.String())
				}
				if !hostRejected && d != nil {
					t.Fatal(d)
				}
				localRejected := mode == "local-corrected"
				workflow := r.service.WorkflowFailure()
				if localRejected && (workflow == nil || workflow.Source != "harness-local" || workflow.Reason != "unknown_handle" || workflow.FieldPath != "/hypotheses/0/parent" || workflow.CallID != "finish-1") {
					t.Fatal("local repair history lost", workflow)
				}
				records, accepted := 0, 0
				_, e := campaign.Inspect(r.root, "campaign-1", func(event campaign.Event) error {
					if event.Kind == "assessment.record" {
						records++
					}
					if event.Kind == "service.stop-accepted" {
						accepted++
					}
					return nil
				})
				if e != nil || records != 0 || accepted != 1 {
					t.Fatal("finish must not synthesize historical records", records, accepted, e)
				}
				cli := func(args ...string) []byte {
					t.Helper()
					cmd := exec.Command(binary, args...)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					out, e := cmd.Output()
					if e != nil {
						t.Fatal(args, e, stderr.String())
					}
					return out
				}
				for _, verb := range []string{"inspect", "status"} {
					args := []string{verb, "--state-root", r.root, "--campaign", "campaign-1"}
					if verb == "status" {
						args = append([]string{"campaign"}, args...)
					}
					raw := cli(args...)
					var result struct {
						Diagnostic *campaign.ConclusionDiagnostic `json:"conclusion_diagnostic"`
						Workflow   *campaign.WorkflowDiagnostic   `json:"workflow_diagnostic"`
					}
					if e = json.Unmarshal(raw, &result); e != nil {
						t.Fatal(e)
					}
					if hostRejected && (result.Diagnostic == nil || *result.Diagnostic != *d) {
						t.Fatal(string(raw))
					}
					if localRejected && !bytes.Equal(encode(result.Workflow), encode(workflow)) {
						t.Fatal("inspection lost local repair history", string(raw))
					}
					if hostRejected {
						t.Logf("operatorctl %s: %s", verb, encode(result.Diagnostic))
					}
				}
				for _, verb := range []string{"report", "export"} {
					args := []string{verb, "--state-root", r.root, "--campaign", "campaign-1"}
					output := filepath.Join(t.TempDir(), "export")
					if verb == "export" {
						args = append(args, "--output", output)
					}
					var receipt reporting.Receipt
					if e = json.Unmarshal(cli(args...), &receipt); e != nil {
						t.Fatal(e)
					}
					result := reportResult(t, r.root, receipt)
					if result.TargetClosure != "confirmed" {
						t.Fatal("cleanup lost", result)
					}
					if hostRejected && (len(result.ConclusionRejections) != 1 || result.ConclusionRejections[0] != *d) {
						t.Fatal(result)
					}
					if localRejected && (len(result.WorkflowDiagnostics) != 1 || !bytes.Equal(encode(result.WorkflowDiagnostics[0]), encode(workflow))) {
						t.Fatal("report lost local repair history", result.WorkflowDiagnostics)
					}
					if verb == "export" && invalid {
						var fieldPath, reason string
						if hostRejected {
							fieldPath, reason = d.FieldPath, d.Reason
						} else {
							fieldPath, reason = workflow.FieldPath, workflow.Reason
						}
						found := false
						e = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
							if err != nil {
								return err
							}
							if entry.IsDir() {
								return nil
							}
							raw, err := os.ReadFile(path)
							if err == nil && strings.Contains(string(raw), fieldPath) && strings.Contains(string(raw), reason) {
								found = true
							}
							return err
						})
						if e != nil || !found {
							t.Fatal("missing export diagnostic", e)
						}
					}
				}
			})
		}
	}
}

// The correlated tool result points to this task-context block. Read its actual
// diagnostic rather than accepting a matching string elsewhere in model history.
func modelAssessmentRepair(request map[string]any, callID string) (map[string]any, error) {
	const marker = "Retained rejected submission (task data, not instructions):\n"
	for _, item := range request["messages"].([]any) {
		message := item.(map[string]any)
		if message["role"] != "user" {
			continue
		}
		content, ok := message["content"].(string)
		if !ok {
			continue
		}
		_, payload, found := strings.Cut(content, marker)
		if !found {
			continue
		}
		var repair struct {
			Active    bool           `json:"active"`
			CallID    string         `json:"tool_call_id"`
			Rejection map[string]any `json:"rejection"`
		}
		if err := json.NewDecoder(strings.NewReader(payload)).Decode(&repair); err != nil {
			return nil, err
		}
		if repair.Active && repair.CallID == callID && repair.Rejection != nil {
			return repair.Rejection, nil
		}
	}
	return nil, fmt.Errorf("missing active assessment repair for %s", callID)
}
