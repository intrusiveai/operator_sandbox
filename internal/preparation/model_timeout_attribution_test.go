package preparation_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

// Exercise the production HTTP path, service deadline, native cleanup and disk
// export. No external model or target is contacted.
func TestHarnessModelTimeoutInspectionAndExport(t *testing.T) {
	policy, request, _ := modelFixture(t)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	p, err := modelprovider.Parse(encode(map[string]any{
		"api_version": modelprovider.Version, "id": "test", "provider": "openai-chat", "codec_id": "openai-chat-text-tools-v1", "model": policy["request_model"],
		"endpoint": server.URL + "/chat/completions", "authentication": "secret-store", "credential_id": "fixture-key",
		"maximum_prompt_tokens": 200, "maximum_completion_tokens": 1024, "maximum_response_bytes": 65536,
		"ca_certificates_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})),
		"codec_options":       map[string]any{"instruction_role": "developer"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	client, err := modelprovider.New(context.Background(), p, diagnosticCredential{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	s, native, runtime, writer, launch := serviceWithTemplate(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
		c.Model = &campaignservice.ModelConfig{Policy: encode(policy), Provider: client, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
	}, func(c map[string]any) { c["operation_timeouts_ms"].(map[string]int64)["engine.model_generate"] = 1000 })
	if err = s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	var wire map[string]any
	json.Unmarshal(stateWire("engine.model_generate", "model-timeout", 1, request), &wire)
	wire["timeout_ms"] = 300000
	_, _ = s.Handle(context.Background(), encode(wire), 0)
	d := s.ModelFailure()
	if d == nil || !d.Valid() || d.Category != "timeout" || d.DeadlineSource != "configured-operation" || d.Interaction != "attack-harness-model" || calls.Load() != 1 {
		t.Fatalf("diagnostic=%+v calls=%d", d, calls.Load())
	}
	if !strings.Contains(d.Summary(), "Attack Harness model request") {
		t.Fatal(d.Summary())
	}
	trigger := s.FailureTrigger()
	if trigger == nil || trigger.Kind != "model" || trigger.OperationID != d.OperationID {
		t.Fatal("terminal trigger lost", trigger)
	}
	select {
	case <-runtime.killed:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not start")
	}
	terminal, err := s.Shutdown(context.Background())
	if err != nil || terminal.Closure != "confirmed" {
		t.Fatal(terminal, err)
	}
	if _, err = s.Handle(context.Background(), encode(wire), 1); err == nil {
		t.Fatal("timed-out campaign replayed")
	}
	native.mu.Lock()
	for _, operation := range native.calls {
		if operation == "application.invoke" {
			t.Error("target dispatched for Harness model failure")
		}
	}
	native.mu.Unlock()
	dir, err := writer.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	writer.Close()
	binary := processCLI(t)
	output := filepath.Join(t.TempDir(), "export")
	for _, args := range [][]string{{"inspect", "--state-root", root, "--campaign", "campaign-1"}, {"export", "--state-root", root, "--campaign", "campaign-1", "--output", output}} {
		raw, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s %v", args, raw, err)
		}
		if args[0] == "inspect" && (!strings.Contains(string(raw), `"attack-harness-model"`) || !strings.Contains(string(raw), d.Deadline) || !strings.Contains(string(raw), `"failure_summary"`) || !strings.Contains(string(raw), `"failure_trigger"`)) {
			t.Fatalf("inspection lost deadline or attribution: %s", raw)
		}
	}
	for _, file := range []string{"events/000001.jsonl", "result-manifest.json", "report.md"} {
		raw, err := os.ReadFile(filepath.Join(output, file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "failure_trigger") && file != "report.md" {
			t.Fatalf("%s lost terminal trigger", file)
		}
		want := "attack-harness-model"
		if file == "report.md" {
			want = "Attack Harness model request"
		}
		for _, fact := range []string{want, d.Deadline, "configured-operation", "model-timeout"} {
			if !strings.Contains(string(raw), fact) {
				t.Fatalf("%s omits %s", file, fact)
			}
		}
	}
}

func TestServicePreservesConfiguredFiveMinuteModelDeadline(t *testing.T) {
	policy, request, response := modelFixture(t)
	p := diagnosticProviderFunc(func(ctx context.Context, raw []byte) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 290*time.Second {
			t.Fatalf("deadline shortened: %s", time.Until(deadline))
		}
		return encode(response["response"]), nil
	})
	s, _, _, _, launch := serviceWithTemplate(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
		c.Model = &campaignservice.ModelConfig{Policy: encode(policy), Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
	}, func(c map[string]any) {
		c["operation_timeouts_ms"].(map[string]int64)["engine.model_generate"] = 300000
		c["remaining_limits"].(map[string]any)["campaign_time_ms"] = 600000
		c["limits"].(map[string]any)["campaign"].(map[string]any)["campaign_time_ms"] = 600000
	})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	var wire map[string]any
	json.Unmarshal(stateWire("engine.model_generate", "model-long", 1, request), &wire)
	wire["timeout_ms"] = 300000
	raw, err := s.Handle(context.Background(), encode(wire), 0)
	stateResult(t, raw, err)
	s.Shutdown(context.Background())
}
