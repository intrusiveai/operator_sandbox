//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
)

func (r *processRun) failed(t *testing.T) {
	t.Helper()
	e := r.cmd.Wait()
	r.waited = true
	r.cancel()
	<-r.pump
	<-r.serve
	if e == nil || r.service.StopAccepted() {
		t.Fatal("failure looked like accepted completion", e)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if _, e = r.service.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	if e = r.writer.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestPythonProcessInterruptedModelNeverReplays(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		for _, mode := range []string{"host-cancel", "harness-loss", "control", "pressure"} {
			if mode == "pressure" && transport != "spool" {
				continue
			}
			t.Run(transport+"/"+mode, func(t *testing.T) {
				r := newProcessRun(t, processCase{transport: transport, prompt: "default", block: true, spoolMaxBytes: 2 << 20})
				select {
				case <-r.provider.entered:
				case <-r.ctx.Done():
					t.Fatal("model not entered")
				}
				switch mode {
				case "host-cancel":
					r.cancel()
				case "harness-loss":
					if e := r.cmd.Process.Kill(); e != nil {
						t.Fatal(e)
					}
				case "control":
					raw := encode(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "terminate", "seq": 3, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": 1, "body": map[string]any{"reason": "user-request", "exit_required": true}})
					if e := r.channel.Enqueue("control-in", raw); e != nil {
						t.Fatal(e)
					}
				case "pressure":
					f, e := os.Create(filepath.Join(r.ipc, "ordinary-out", "runaway.tmp"))
					if e != nil {
						t.Fatal(e)
					}
					e = f.Truncate(3 << 20)
					f.Close()
					if e != nil {
						t.Fatal(e)
					}
				}
				r.failed(t)
				if len(r.provider.requests) != 1 {
					t.Fatal("provider was retried")
				}
				intents, unknown := 0, 0
				_, e := campaign.Inspect(r.root, "campaign-1", func(e campaign.Event) error {
					if e.Kind == "model.intent" {
						intents++
					}
					if e.Kind == "model.exchange" {
						var m map[string]any
						_ = json.Unmarshal(e.Metadata, &m)
						if m["outcome"] == "unknown" {
							unknown++
						}
					}
					return nil
				})
				if e != nil || intents != 1 || unknown != 1 {
					t.Fatal("uncertain model outcome not durable", intents, unknown, e)
				}
				if e = r.service.Admit(context.Background(), r.launch.Inputs); e == nil {
					t.Fatal("closed service readmitted")
				}
				for i := 0; i < 2; i++ {
					if _, e = nativerecovery.Run(context.Background(), r.root, "campaign-1", true, r.native); e != nil {
						t.Fatal(e)
					}
				}
				if len(r.provider.requests) != 1 {
					t.Fatal("recovery resumed campaign")
				}
			})
		}
	}
}

func TestPythonProcessUnknownRestoreIsTerminal(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		t.Run(transport, func(t *testing.T) {
			reply := func(turn int, q map[string]any) ([]byte, error) {
				if turn == 1 {
					return processResponse([]any{modelCall("snapshot", "snapshot_request", map[string]any{})}), nil
				}
				if turn == 2 {
					v, e := modelToolResult(q, "snapshot")
					if e != nil {
						return nil, e
					}
					s := v["snapshot"].(map[string]any)
					return processResponse([]any{modelCall("restore", "restore_request", map[string]any{"source_session": s["source_session"], "checkpoint_id": s["checkpoint_id"]}), modelCall("must-not-run", "snapshot_request", map[string]any{})}), nil
				}
				return nil, errors.New("model resumed after unknown restore")
			}
			r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: reply, change: snapshotInput(t), native: func(p *peer) { p.restoreMode = "lost" }})
			r.failed(t)
			if r.native.restores != 1 || len(r.native.checkpoints) != 1 || len(r.provider.requests) != 2 {
				t.Fatal("unknown restore repeated or continued")
			}
		})
	}
}
