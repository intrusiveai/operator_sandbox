//go:build linux || darwin

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test exchanges real files with the sibling Python transport. It does not
// qualify container file sharing, guest confinement or startup input validation.
func TestPythonSpoolInteroperability(t *testing.T) {
	testPythonPeer(t, false)
}

func TestPythonFIFOInteroperability(t *testing.T) {
	testPythonPeer(t, true)
}

func testPythonPeer(t *testing.T, fifo bool) {
	if os.Getenv("OPERATOR_PYTHON_PEER_TEST") != "1" {
		t.Skip("set OPERATOR_PYTHON_PEER_TEST=1 with the sibling Attack Harness checkout")
	}
	operator, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(filepath.Dir(operator), "attack_harness")
	if override := os.Getenv("OPERATOR_TEST_HARNESS_SOURCE"); override != "" {
		harness = override
	}
	f := fixtures(t)
	dir := directory(t)
	create := NewSpool
	if fifo {
		create = NewFIFO
	}
	s, err := create(dir, config(f))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pump := make(chan error, 1)
	go func() { pump <- s.Run(ctx) }()
	defer func() { cancel(); <-pump }()
	python := os.Getenv("OPERATOR_TEST_PYTHON")
	if python == "" {
		python = filepath.Join(operator, ".venv/bin/python")
	}
	cmd := exec.CommandContext(ctx, python, filepath.Join(harness, "tests/spool_peer.py"), dir, filepath.Join(operator, "schemas"))
	if fifo {
		cmd.Args = append(cmd.Args, "fifo")
	}
	cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(harness, "src")+":"+filepath.Join(operator, "contracts/python"))
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			cmd.Wait()
		}
	}()
	raw, err := os.ReadFile(filepath.Join(operator, "schemas/fixtures/startup-example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var transcript []json.RawMessage
	if err := json.Unmarshal(raw, &transcript); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 1} {
		if fifo {
			break
		}
		transcript[index] = change(transcript[index], func(m map[string]any) {
			b := m["body"].(map[string]any)
			b["host_platform"] = "darwin/arm64"
			b["transport"] = "spool"
		})
	}
	receive := func(lane string) []byte {
		t.Helper()
		for {
			if raw, ok := s.Receive(lane); ok {
				return raw
			}
			select {
			case <-ctx.Done():
				t.Fatal("Python peer receive timed out")
			case <-time.After(time.Millisecond):
			}
		}
	}
	for _, index := range []int{0, 2, 4} {
		for {
			err := s.Enqueue("control-in", transcript[index])
			if err == nil {
				break
			}
			if !errors.Is(err, ErrNotReady) {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("FIFO rendezvous timeout")
			case <-time.After(time.Millisecond):
			}
		}
		if index < 4 {
			actual := receive("control-out")
			var a, b any
			json.Unmarshal(actual, &a)
			json.Unmarshal(transcript[index+1], &b)
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if !bytes.Equal(aa, bb) {
				t.Fatal("Python changed startup")
			}
			if index == 0 {
				if err := s.BeginInitialization(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for !s.Published("control-in", 2) {
		select {
		case <-ctx.Done():
			t.Fatal("admission not published")
		case <-time.After(time.Millisecond):
		}
	}
	if err := s.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	if actual := receive("ordinary-out"); !bytes.Equal(actual, f.frames["ordinary-out"]) {
		t.Fatal("Python changed request")
	}
	if err := s.Enqueue("ordinary-in", f.frames["ordinary-in"]); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || strings.TrimSpace(output.String()) != "transport complete" {
		t.Fatal(err, output.String())
	}
}
