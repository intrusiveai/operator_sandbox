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
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// Real production Python + Go FIFO/spool; controlled native peer, no provider calls.
func TestPythonAttemptTimeoutResultBeforeHarnessDeadline(t *testing.T) {
	for _, lane := range []string{"fifo", "spool"} {
		t.Run(lane, func(t *testing.T) {
			r := newProcessRun(t, processCase{transport: lane, prompt: "default", change: timeoutProfile(t, 5000), native: func(p *peer) { p.delayOperation = "observation.content.read"; p.delay = 10 * time.Second }, reply: func(turn int, q map[string]any) ([]byte, error) {
				if turn != 1 {
					return nil, fmt.Errorf("unexpected model retry")
				}
				return processResponse([]any{modelCall("invoke", "attempt_execute", processAttempt("scenario", "", []byte(`{"query":"marker"}`), ""))}), nil
			}})
			started := time.Now()
			r.failed(t)
			d := r.service.WorkflowFailure()
			if d == nil || d.Timeout == nil || d.Invocation != "succeeded" || d.Timeout.NativeOperation != "observation.content.read" || d.ResultDelivery != "published" {
				t.Fatalf("diagnostic=%+v stderr=%s", d, r.output.String())
			}
			if time.Since(started) > 15*time.Second || strings.Contains(r.output.String(), "deadline-exceeded") {
				t.Fatal("Harness expired before timeout reply", r.output.String())
			}
			if !strings.Contains(r.output.String(), "attempt-unknown") && !strings.Contains(r.output.String(), "attempt-failed") {
				t.Fatal("Harness did not interpret the terminal attempt", r.output.String())
			}
			calls := 0
			for _, op := range r.native.calls {
				if op == "application.invoke" {
					calls++
				}
			}
			if calls != 1 || len(r.provider.requests) != 1 {
				t.Fatal("invocation or model replayed")
			}
			requested := false
			_, err := campaign.Inspect(r.root, "campaign-1", func(e campaign.Event) error {
				if e.Kind != "service.request" {
					return nil
				}
				for _, ref := range e.Content {
					// Already closed writer: verify via retained evidence file as other process tests do.
					raw, e := os.ReadFile(filepath.Join(r.root, "campaigns", "campaign-1", filepath.FromSlash(ref.Path)))
					if e != nil {
						return e
					}
					var q struct {
						Operation string
						Timeout   int64 `json:"timeout_ms"`
					}
					if json.Unmarshal(raw, &q) == nil && q.Operation == "engine.attempt_execute" {
						requested = q.Timeout == 120000
					}
				}
				return nil
			})
			if err != nil || !requested {
				t.Fatal("Harness did not use registry timeout", err)
			}
			cleanup, err := r.service.Shutdown(context.Background())
			if err != nil || cleanup.Closure != "confirmed" || cleanup.CleanupState != "complete" {
				t.Fatal(cleanup, err)
			}
		})
	}
}
