//go:build linux || darwin

package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	binary := filepath.Join(t.TempDir(), "operatorctl")
	build := exec.Command("go", "build", "-o", binary, "./cmd/operatorctl")
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, transport := range []string{"fifo", "spool"} {
		for _, invalid := range []bool{false, true} {
			name := transport + "/accepted"
			if invalid {
				name = transport + "/rejected"
			}
			t.Run(name, func(t *testing.T) {
				r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: func(turn int, request map[string]any) ([]byte, error) {
					if turn != 1 {
						t.Fatal("unexpected model retry")
					}
					h := localHypothesis("hyp-configuration-1", "")
					if invalid {
						h["provenance"].(map[string]any)["parent_hypothesis_id"] = "hyp-missing"
					}
					draft := modelDraft([]string{}, "")
					draft["objectives"] = []any{}
					draft["coverage"] = []any{}
					draft["status"] = "partial"
					draft["finish_reason"] = "no-useful-next-experiment"
					draft["hypotheses"] = []any{h}
					return processResponse([]any{modelCall("finish", "finish", finishDraft(draft))}), nil
				}})
				if invalid {
					r.failed(t)
				} else {
					r.finish(t)
					if e := r.writer.Close(); e != nil {
						t.Fatal(e)
					}
				}
				if len(r.provider.requests) != 1 {
					t.Fatal("replayed model request")
				}
				r.native.mu.Lock()
				closed := r.native.closed
				r.native.mu.Unlock()
				if !closed {
					t.Fatal("target not closed")
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
				if invalid && (d == nil || d.Reason != "unresolved_hypothesis" || d.Identifier != "hyp-missing" || d.FieldPath != "/hypotheses/0/provenance/parent_hypothesis_id") {
					t.Fatal(d, r.output.String())
				}
				if !invalid && d != nil {
					t.Fatal(d)
				}
				records := 0
				_, e := campaign.Inspect(r.root, "campaign-1", func(event campaign.Event) error {
					if event.Kind == "assessment.record" {
						records++
						var m map[string]any
						_ = json.Unmarshal(event.Metadata, &m)
						if m["record_kind"] != "conclusion" {
							t.Fatal("synthesized history", m)
						}
					}
					return nil
				})
				if e != nil || (!invalid && records != 1) || (invalid && records != 0) {
					t.Fatal(records, e)
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
					}
					if e = json.Unmarshal(raw, &result); e != nil {
						t.Fatal(e)
					}
					if invalid && (result.Diagnostic == nil || *result.Diagnostic != *d) {
						t.Fatal(string(raw))
					}
					if invalid {
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
					if invalid && (len(result.ConclusionRejections) != 1 || result.ConclusionRejections[0] != *d) {
						t.Fatal(result)
					}
					if verb == "export" && invalid {
						found := false
						e = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
							if err != nil {
								return err
							}
							if entry.IsDir() {
								return nil
							}
							raw, err := os.ReadFile(path)
							if err == nil && strings.Contains(string(raw), d.FieldPath) && strings.Contains(string(raw), d.Reason) {
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
