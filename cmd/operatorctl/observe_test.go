//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

func observerFixture(t *testing.T) (string, *campaign.Writer) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := campaign.Create(root, savedManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return root, w
}
func observerAppend(t *testing.T, w *campaign.Writer, kind string) {
	t.Helper()
	if _, err := w.Append(campaign.Entry{RunRevision: 3, Kind: kind, Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}
func observeCLI(t *testing.T, ctx context.Context, action, root string, extra ...string) (int, []json.RawMessage, string) {
	t.Helper()
	args := append([]string{"campaign", action, "--campaign", "campaign-1", "--state-root", root}, extra...)
	var out, errOut bytes.Buffer
	code := run(ctx, args, &out, &errOut)
	var records []json.RawMessage
	d := json.NewDecoder(&out)
	for d.More() {
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		records = append(records, raw)
	}
	return code, records, errOut.String()
}
func observationLast(t *testing.T, records []json.RawMessage) observationReceipt {
	t.Helper()
	if len(records) == 0 {
		t.Fatal("missing receipt")
	}
	var r observationReceipt
	if err := json.Unmarshal(records[len(records)-1], &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestCampaignObserversActiveAndTerminal(t *testing.T) {
	root, w := observerFixture(t)
	observerAppend(t, w, "campaign.preparation-adopted")
	code, rows, detail := observeCLI(t, context.Background(), "status", root)
	r := observationLast(t, rows)
	if code != 0 || len(rows) != 1 || !r.Verified || r.Sequence != 1 || r.ExecutionState != "unknown" || r.TerminalRecorded {
		t.Fatalf("%d %+v %s", code, r, detail)
	}
	observerAppend(t, w, "launch.start-intent")
	observerAppend(t, w, "launch.terminal")
	code, rows, detail = observeCLI(t, context.Background(), "logs", root, "--limit", "2")
	r = observationLast(t, rows)
	if code != 0 || len(rows) != 3 || !r.More || r.NextAfter != 2 || r.Sequence != 3 || !r.TerminalRecorded {
		t.Fatalf("page: %d %+v %s", code, r, detail)
	}
	code, rows, detail = observeCLI(t, context.Background(), "logs", root, "--after", "2", "--limit", "2")
	r = observationLast(t, rows)
	if code != 0 || len(rows) != 2 || r.More || r.NextAfter != 3 || r.Emitted != 1 {
		t.Fatalf("next page: %d %+v %s", code, r, detail)
	}
	code, rows, detail = observeCLI(t, context.Background(), "wait", root, "--timeout", "1s")
	r = observationLast(t, rows)
	if code != 0 || len(rows) != 1 || r.ExecutionState != "closed" {
		t.Fatalf("wait: %d %+v %s", code, r, detail)
	}
	// Observation never takes ownership of or fences this active writer.
	observerAppend(t, w, "report.finished")
}
func TestObserverTimeoutAndCorruptionDoNotStopWriter(t *testing.T) {
	root, w := observerFixture(t)
	observerAppend(t, w, "launch.start-intent")
	code, rows, _ := observeCLI(t, context.Background(), "wait", root, "--timeout", "10ms")
	if code != 1 || observationLast(t, rows).TerminalRecorded {
		t.Fatal("timeout reported completion")
	}
	observerAppend(t, w, "still.writing")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, rows, _ = observeCLI(t, ctx, "wait", root)
	if code != 1 || observationLast(t, rows).Verified {
		t.Fatal("cancelled scan accepted")
	}
	observerAppend(t, w, "still.writing")
	p := filepath.Join(root, "campaigns/campaign-1/journals/0000000000000003/events-000001.jsonl")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("still.writing"), []byte("tampered.data"), 1)
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	code, rows, _ = observeCLI(t, context.Background(), "logs", root)
	if code != 1 || len(rows) != 2 || observationLast(t, rows).Verified {
		t.Fatal("corrupt prefix reported complete")
	}
}
func TestObserverArgumentValidation(t *testing.T) {
	root, _ := observerFixture(t)
	for _, tc := range []struct {
		action string
		args   []string
	}{
		{"logs", []string{"--after", "-1"}}, {"logs", []string{"--after", "9007199254740992"}},
		{"logs", []string{"--limit", "10001"}}, {"logs", []string{"--limit", "0"}},
		{"wait", []string{"--timeout", "0"}}, {"wait", []string{"--timeout", "25h"}},
		{"status", []string{"--config", ""}}, {"status", []string{"--state-root", "relative"}},
		{"status", []string{"--after", "1"}}, {"status", []string{"unexpected"}},
	} {
		t.Run(tc.action+strings.Join(tc.args, " "), func(t *testing.T) {
			code, _, _ := observeCLI(t, context.Background(), tc.action, root, tc.args...)
			if code != 2 {
				t.Fatal(code)
			}
		})
	}
}
