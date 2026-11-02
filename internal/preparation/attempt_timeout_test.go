package preparation_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

func timeoutProfile(t *testing.T, ms int64) func(*preparation.Input) {
	return func(in *preparation.Input) {
		s := in.Profile.Settings()
		s.OperationTimeoutMS = ms
		var err error
		in.Profile, err = targetprofile.Parse(encode(s))
		if err != nil {
			t.Fatal(err)
		}
	}
}
func timeoutRequest(p *peer, w *campaign.Writer, ms int64) []byte {
	var q map[string]any
	_ = json.Unmarshal(attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), &q)
	q["timeout_ms"] = ms
	delete(q["body"].(map[string]any), "observation_selection")
	return encode(q)
}
func TestAttemptTimeoutAfterSuccessfulTarget(t *testing.T) {
	s, p, _, w, launch := serviceFixture(t, timeoutProfile(t, 5000))
	p.delayOperation = "observation.content.read"
	p.delay = 10 * time.Second
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	raw, err := s.Handle(context.Background(), timeoutRequest(p, w, 120000), 0)
	if err != nil {
		t.Fatalf("known outcome lost: %v", err)
	}
	var r struct {
		Result struct {
			Status, Stage string
			Invocation    string                   `json:"invocation_state"`
			Timeout       *campaign.AttemptTimeout `json:"timeout"`
		}
	}
	if json.Unmarshal(raw, &r) != nil || (r.Result.Status != "failed" && r.Result.Status != "unknown") || r.Result.Stage != "observation" || r.Result.Invocation != "succeeded" || r.Result.Timeout == nil {
		t.Fatal(string(raw))
	}
	d := s.WorkflowFailure()
	if d == nil || !d.Valid() || d.Timeout == nil || d.Timeout.NativeOperation != "observation.content.read" || d.Timeout.AllowanceMS != 5000 || d.Timeout.ElapsedMS < 4900 || d.Invocation != "succeeded" || time.Since(started) > 8*time.Second {
		t.Fatalf("%+v", d)
	}
	if _, err = s.Handle(context.Background(), timeoutRequest(p, w, 120000), 1); err == nil {
		t.Fatal("terminal attempt accepted again")
	}
	s.Shutdown(context.Background())
	count := 0
	for _, op := range p.calls {
		if op == "application.invoke" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("invocations=%d", count)
	}
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	w.Close()
	// Independent CLI processes exercise persisted inspection and actual exports.
	binary := processCLI(t)
	for _, verb := range []string{"inspect", "report", "export"} {
		args := []string{verb, "--state-root", root, "--campaign", "campaign-1"}
		out := filepath.Join(t.TempDir(), "export")
		if verb == "export" {
			args = append(args, "--output", out)
		}
		command := exec.Command(binary, args...)
		data, e := command.CombinedOutput()
		if e != nil {
			t.Fatalf("%s: %s %v", verb, data, e)
		}
		if verb == "inspect" && !strings.Contains(string(data), "attempt_timeout") {
			t.Fatalf("lost diagnostic %s: %s", verb, data)
		}
		if verb == "export" {
			for _, file := range []string{"result-manifest.json", "events/000001.jsonl", "report.md"} {
				content, e := os.ReadFile(filepath.Join(out, file))
				if e != nil {
					t.Fatal(e)
				}
				if !strings.Contains(string(content), "observation.content.read") || !strings.Contains(string(content), "succeeded") {
					t.Fatalf("lost facts in %s", file)
				}
			}
		}
	}
}
func TestAttemptLargerBudgetAndShorterDeadlines(t *testing.T) {
	for _, mode := range []string{"long-success", "request", "campaign"} {
		t.Run(mode, func(t *testing.T) {
			campaignBudget := time.Duration(0)
			if mode == "campaign" {
				campaignBudget = 300 * time.Millisecond
			}
			s, p, _, w, launch := serviceWithDeadline(t, campaignBudget, timeoutProfile(t, 90000))
			p.delayOperation = "application.invoke"
			p.delay = 31 * time.Second
			wait := int64(120000)
			if mode != "long-success" {
				p.delay = time.Second
				wait = 200
			}
			if mode == "campaign" {
				wait = 120000
			}
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			raw, err := s.Handle(context.Background(), timeoutRequest(p, w, wait), 0)
			if mode == "long-success" {
				if err != nil || !strings.Contains(string(raw), `"status":"completed"`) || time.Since(started) < 30*time.Second {
					t.Fatalf("%s %v", raw, err)
				}
			} else if time.Since(started) > 2*time.Second {
				t.Fatal("short deadline ignored")
			}
			s.Shutdown(context.Background())
			count := 0
			for _, op := range p.calls {
				if op == "application.invoke" {
					count++
				}
			}
			if count > 1 {
				t.Fatal("replayed invocation")
			}
		})
	}
}
