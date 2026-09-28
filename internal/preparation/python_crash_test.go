//go:build linux || darwin

package preparation_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
)

type processCrashIdentity struct {
	Root string
	PID  int
}

// Invoked only as a disposable Go host process, never in the ordinary suite.
func TestPythonProcessHostCrashHelper(t *testing.T) {
	name := os.Getenv("OPERATOR_PROCESS_CRASH_READY")
	if name == "" {
		t.Skip("subprocess helper")
	}
	r := newProcessRun(t, processCase{transport: "spool", prompt: "default", block: true})
	select {
	case <-r.provider.entered:
	case <-r.ctx.Done():
		t.Fatal("provider not entered")
	}
	raw := encode(processCrashIdentity{r.root, r.cmd.Process.Pid})
	if e := os.WriteFile(name+".tmp", raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(name+".tmp", name); e != nil {
		t.Fatal(e)
	}
	<-r.ctx.Done()
	t.Fatal("host was not killed by parent")
}

func TestPythonProcessHostCrashRetainsUnknownWork(t *testing.T) {
	if os.Getenv("OPERATOR_HARNESS_INTEGRATION") != "1" {
		t.Skip("opt-in cross-process test")
	}
	root := t.TempDir()
	// SIGKILL intentionally bypasses child cleanup. The test owns this entire tree.
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(name string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(name, 0700)
			}
			return e
		})
	})
	ready := filepath.Join(root, "ready.json")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPythonProcessHostCrashHelper$", "-test.timeout=40s")
	cmd.Env = append(os.Environ(), "OPERATOR_PROCESS_CRASH_READY="+ready, "TMPDIR="+root)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if !waited {
			_ = cmd.Wait()
		}
	})
	var identity processCrashIdentity
	for {
		raw, e := os.ReadFile(ready)
		if e == nil {
			if e = json.Unmarshal(raw, &identity); e != nil {
				t.Fatal(e)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("host startup timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
	python, e := os.FindProcess(identity.PID)
	if e != nil {
		t.Fatal(e)
	}
	defer python.Kill()
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e = cmd.Wait(); e == nil {
		t.Fatal("host did not crash")
	}
	waited = true
	// The independent runtime boundary terminates the surviving harness before
	// cleanup. No Python request is reissued by the recovery path.
	_ = python.Kill()
	intents, exchanges := 0, 0
	inspected, e := campaign.Inspect(identity.Root, "campaign-1", func(e campaign.Event) error {
		if e.Kind == "model.intent" {
			intents++
		}
		if e.Kind == "model.exchange" {
			exchanges++
		}
		return nil
	})
	if e != nil || !inspected.JournalIntact || intents != 1 || exchanges != 0 {
		t.Fatal("crash prefix changed", intents, exchanges, e)
	}
	native := &peer{input: fixture(t), revision: 5}
	first, e := nativerecovery.Run(ctx, identity.Root, "campaign-1", true, native)
	if e != nil {
		t.Fatal(e)
	}
	again, e := nativerecovery.Run(ctx, identity.Root, "campaign-1", true, native)
	if e != nil || first != again {
		t.Fatal(first, again, e)
	}
	for _, op := range native.calls {
		if op == "application.invoke" || op == "attempt.register" {
			t.Fatal("recovery replayed effects")
		}
	}
}
