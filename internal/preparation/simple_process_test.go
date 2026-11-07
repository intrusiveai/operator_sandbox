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
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func TestSimplePythonSpoolAndSeparateCLI(t *testing.T) {
	if os.Getenv("OPERATOR_CONTROLLED_PEER_TEST") != "1" {
		t.Skip("opt-in controlled Python process peer")
	}
	operator, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("OPERATOR_TEST_PYTHON")
	if python == "" {
		python = filepath.Join(operator, ".venv/bin/python")
	}
	binary := filepath.Join(t.TempDir(), "operatorctl")
	build := exec.Command("go", "build", "-o", binary, "./cmd/operatorctl")
	build.Dir = operator
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, mode := range []string{"direct", "corrected", "qa-corrected", "invalid-limit", "pagination-corrected", "budget-001", "budget-002"} {
		t.Run(mode, func(t *testing.T) {
			budgetMode := strings.HasPrefix(mode, "budget-")
			var root string
			operations := append(append([]string{}, completionRoutes...), "engine.attempt_execute", "engine.observation_read", "engine.diagnostic_report")
			s, p, _, w, launch := serviceWithTemplate(t, 0, operations, func(c *campaignservice.Config) { root = c.StateRoot }, func(v map[string]any) {
				v["limits"].(map[string]any)["campaign"].(map[string]any)["observation_bytes"] = 4096
				v["remaining_limits"].(map[string]any)["observation_bytes"] = 4096
				// This fixture covers four invocations; the generic startup
				// fixture intentionally budgets only one observation read.
				v["limits"].(map[string]any)["campaign"].(map[string]any)["observation_reads"] = 16
				v["remaining_limits"].(map[string]any)["observation_reads"] = 16
			})
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			var child *exec.Cmd
			var output bytes.Buffer
			serviceSpool(t, s, p, w, launch, func(dir string) {
				child = exec.CommandContext(ctx, python, "-I", "-B", filepath.Join(operator, "internal/preparation/testdata/simple_peer.py"), operator, dir, mode)
				child.Stdout = &output
				child.Stderr = &output
				if e := child.Start(); e != nil {
					t.Fatal(e)
				}
			})
			if e := child.Wait(); e != nil {
				t.Fatal(e, output.String(), w.Fence().Err())
			}
			var result struct {
				Dispatched int
				Calls      int `json:"ordinary_calls"`
			}
			if e := json.Unmarshal(output.Bytes(), &result); e != nil {
				t.Fatal(e, output.String())
			}
			expected := 1
			if mode == "qa-corrected" {
				expected = 4
			}
			if mode == "invalid-limit" {
				expected = 0
			}
			if result.Dispatched != expected || !s.StopAccepted() {
				t.Fatal(output.String())
			}
			if mode == "direct" && result.Calls != 5 {
				t.Fatal("ordinary workflow requires extra steps", output.String())
			} // Three model calls, one internal selection audit and duplicate-finish test.
			terminal, e := s.Shutdown(context.Background())
			if e != nil || terminal.Closure != "confirmed" || terminal.CleanupState != "complete" {
				t.Fatal(terminal, e)
			}
			if e = w.Close(); e != nil {
				t.Fatal(e)
			}
			nativeCalls := 0
			p.mu.Lock()
			for _, op := range p.calls {
				if op == "application.invoke" {
					nativeCalls++
				}
			}
			p.mu.Unlock()
			if nativeCalls != expected {
				t.Fatal("unexpected dispatch or replay", nativeCalls)
			}
			publicArtifacts, records := 0, 0
			_, e = campaign.Inspect(root, "campaign-1", func(event campaign.Event) error {
				if event.Kind == "assessment.record" {
					records++
				}
				if event.Kind == "service.request" {
					for _, ref := range event.Content {
						raw, readErr := os.ReadFile(filepath.Join(root, "campaigns", "campaign-1", filepath.FromSlash(ref.Path)))
						if readErr != nil {
							return readErr
						}
						var request struct{ Operation string }
						_ = json.Unmarshal(raw, &request)
						if strings.HasPrefix(request.Operation, "engine.artifact_") {
							publicArtifacts++
						}
					}
				}
				return nil
			})
			if e != nil || publicArtifacts != 0 || records != 0 {
				t.Fatal("ordinary path required artifacts or records", publicArtifacts, records, e)
			}
			for _, verb := range []string{"inspect", "report", "export"} {
				args := []string{verb, "--state-root", root, "--campaign", "campaign-1"}
				destination := filepath.Join(t.TempDir(), "export")
				if verb == "export" {
					args = append(args, "--output", destination)
				}
				command := exec.CommandContext(ctx, binary, args...)
				raw, e := command.CombinedOutput()
				if e != nil {
					t.Fatal(args, e, string(raw))
				}
				if verb == "report" || verb == "export" {
					var receipt reporting.Receipt
					if json.Unmarshal(raw, &receipt) != nil {
						t.Fatal(string(raw))
					}
					result := reportResult(t, root, receipt)
					if mode == "invalid-limit" {
						if len(result.WorkflowDiagnostics) != 1 || result.WorkflowDiagnostics[0].Stopping != "invalid-call-limit" || len(result.Attempts) != 0 {
							t.Fatal(result)
						}
					} else if budgetMode {
						if len(result.WorkflowDiagnostics) != 2 || result.WorkflowDiagnostics[1].Budget == nil || result.HarnessClaims != 0 {
							t.Fatal("missing token budget facts", result)
						}
					} else if result.HarnessClaims != 1 {
						t.Fatal("accepted assessment missing from report", result)
					}
					if verb == "export" {
						data, e := os.ReadFile(filepath.Join(destination, "result-manifest.json"))
						if e != nil {
							t.Fatal(e)
						}
						if mode != "invalid-limit" && !budgetMode {
							files, err := filepath.Glob(filepath.Join(destination, "events", "*.jsonl"))
							if err != nil {
								t.Fatal(err)
							}
							selections := 0
							for _, file := range files {
								raw, err := os.ReadFile(file)
								if err != nil {
									t.Fatal(err)
								}
								for _, line := range bytes.Split(raw, []byte("\n")) {
									if len(line) == 0 {
										continue
									}
									var event struct {
										Kind     string
										Metadata struct {
											Selections []map[string]any `json:"observation_selections"`
										}
									}
									if err = json.Unmarshal(line, &event); err != nil {
										t.Fatal(err)
									}
									if event.Kind == "workflow.diagnostic" {
										selections += len(event.Metadata.Selections)
									}
								}
							}
							if selections != expected && mode != "pagination-corrected" {
								t.Fatalf("exported %d observation selections; want %d", selections, expected)
							}
						}
						if mode == "invalid-limit" && !bytes.Contains(data, []byte("invalid-call-limit")) {
							t.Fatal("export lost stopping cause")
						}
					}
				}
				if budgetMode {
					consumed, remaining := "493460", "30828"
					if mode == "budget-002" {
						consumed, remaining = "502198", "22090"
					}
					if verb == "inspect" {
						for _, fact := range []string{`"kind":"model-tokens"`, consumed, remaining, "36864", "524288"} {
							compact := bytes.ReplaceAll(raw, []byte(" "), nil)
							if !bytes.Contains(compact, []byte(fact)) {
								t.Fatalf("inspection lost %s: %s", fact, raw)
							}
						}
					}
					if verb == "export" {
						markdown, err := os.ReadFile(filepath.Join(destination, "report.md"))
						if err != nil {
							t.Fatal(err)
						}
						events, err := os.ReadFile(filepath.Join(destination, "events", "000001.jsonl"))
						if err != nil {
							t.Fatal(err)
						}
						for _, fact := range []string{consumed, remaining, "36864", "524288"} {
							if !bytes.Contains(markdown, []byte(fact)) || !bytes.Contains(events, []byte(fact)) {
								t.Fatalf("export lost %s", fact)
							}
						}
						if !bytes.Contains(markdown, []byte("Harness could not request another model response")) || !bytes.Contains(events, []byte("invalid_structure")) {
							t.Fatal("lost separate causes")
						}
						t.Log("budget summary retained in report and event export")
					}
				}
				if mode == "invalid-limit" && verb == "inspect" {
					var inspect struct {
						Diagnostic *campaign.WorkflowDiagnostic `json:"workflow_diagnostic"`
					}
					if json.Unmarshal(raw, &inspect) != nil || inspect.Diagnostic == nil || inspect.Diagnostic.Stopping != "invalid-call-limit" || inspect.Diagnostic.Observed != 5 || inspect.Diagnostic.Limit != 5 || inspect.Diagnostic.FieldPath != "" {
						t.Fatal(string(raw))
					}
					t.Log("separate inspection:", inspect.Diagnostic.Message())
				}
			}
			t.Log("controlled peer:", output.String())
		})
	}
}
