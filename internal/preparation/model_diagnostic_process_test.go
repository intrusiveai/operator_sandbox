//go:build linux || darwin

package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
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

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

type diagnosticCredential struct{}

func (diagnosticCredential) Resolve(context.Context, string) (credentials.Resolution, error) {
	return credentials.Resolution{Value: "synthetic-provider-key"}, nil
}

// Production Python bootstrap/dispatch + Go campaign service + real HTTPS client,
// followed by independent CLI processes. Docker/Interceptor are controlled peers;
// this does not claim native Docker or live provider qualification.
func TestPythonProcessModelDiagnosticInspectionAndExport(t *testing.T) {
	if os.Getenv("OPERATOR_HARNESS_INTEGRATION") != "1" {
		t.Skip("set OPERATOR_HARNESS_INTEGRATION=1 with sibling Harness and Python environment")
	}
	operator, _ := filepath.Abs("../..")
	binary := filepath.Join(t.TempDir(), "operatorctl")
	build := exec.Command("go", "build", "-o", binary, "./cmd/operatorctl")
	build.Dir = operator
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, transport := range []string{"fifo", "spool"} {
		for _, status := range []int{401, 200} {
			t.Run(fmt.Sprintf("%s/%d", transport, status), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Request-Id", "req_0123456789abcdef0123456789abcdef")
					w.Header().Set("X-Private", "synthetic-private-header")
					w.WriteHeader(status)
					fmt.Fprint(w, `{"error":{"code":"invalid_api_key","message":"synthetic-provider-key synthetic-private-body"}}`)
				}))
				defer server.Close()
				p, e := modelprovider.Parse(encode(map[string]any{
					"api_version": modelprovider.Version, "id": "test", "provider": "openai-chat", "codec_id": "openai-chat-text-tools-v1", "model": "test-model",
					"endpoint": server.URL + "/v1/chat/completions", "authentication": "secret-store", "credential_id": "fixture-key",
					"maximum_prompt_tokens": 10000, "maximum_completion_tokens": 4096, "maximum_response_bytes": 65536,
					"ca_certificates_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})),
					"codec_options":       map[string]any{"instruction_role": "developer", "response_models": []string{"test-model"}},
				}))
				if e != nil {
					t.Fatal(e)
				}
				client, e := modelprovider.New(context.Background(), p, diagnosticCredential{})
				if e != nil {
					t.Fatal(e)
				}
				defer client.Close()
				r := newProcessRun(t, processCase{transport: transport, prompt: "default", modelProvider: client})
				r.failed(t)
				d := r.service.ModelFailure()
				if d == nil || d.HTTPStatus != status || d.Provider != "openai-chat" || d.OperationID != "model-generate-0000000000000001" || d.RequestID == "" || calls.Load() != 1 {
					t.Fatalf("diagnostic=%+v calls=%d", d, calls.Load())
				}
				category := "http_rejection"
				if status == 200 {
					category = "response_validation"
				}
				if d.Category != category {
					t.Fatal(d)
				}
				r.native.mu.Lock()
				closed := r.native.closed
				nativeCalls := append([]string{}, r.native.calls...)
				r.native.mu.Unlock()
				if !closed {
					t.Fatal("target left open")
				}
				for _, call := range nativeCalls {
					if strings.Contains(call, "attempt") || strings.Contains(call, "injection.arm") {
						t.Fatal("target invoked", call)
					}
				}
				if e = r.channel.Close(); e != nil {
					t.Fatal(e)
				}
				if e = r.channel.CleanupAfterExit(true); e != nil {
					t.Fatal(e)
				}
				if e = r.tree.Discard(); e != nil {
					t.Fatal(e)
				}
				if _, e = os.Stat(r.ipc); !os.IsNotExist(e) {
					t.Fatal("transport not removed", e)
				}
				if e = r.service.Admit(context.Background(), r.launch.Inputs); e == nil {
					t.Fatal("failed campaign resumed")
				}
				count := 0
				_, e = campaign.Inspect(r.root, "campaign-1", func(event campaign.Event) error {
					if event.Kind == "model.exchange" {
						count++
						if len(event.Content) != 0 {
							t.Fatal("failed provider body retained")
						}
					}
					return nil
				})
				if e != nil || count != 1 {
					t.Fatal(count, e)
				}
				cli := func(args ...string) []byte {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, args...)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					out, err := cmd.Output()
					if err != nil {
						t.Fatal(args, err, stderr.String(), string(out))
					}
					return out
				}
				for _, verb := range []string{"inspect", "status"} {
					args := []string{verb, "--state-root", r.root, "--campaign", "campaign-1"}
					if verb == "status" {
						args = append([]string{"campaign"}, args...)
					}
					raw := cli(args...)
					var inspected struct {
						Diagnostic *modelprovider.Diagnostic `json:"model_diagnostic"`
					}
					if e = json.Unmarshal(raw, &inspected); e != nil || inspected.Diagnostic == nil || *inspected.Diagnostic != *d {
						t.Fatal("separate inspection differs", string(raw), e)
					}
					t.Logf("operatorctl %s --campaign campaign-1: %s", verb, encode(inspected.Diagnostic))
				}
				for _, verb := range []string{"report", "export"} {
					args := []string{verb, "--state-root", r.root, "--campaign", "campaign-1"}
					output := filepath.Join(t.TempDir(), "export")
					if verb == "export" {
						args = append(args, "--output", output)
					}
					raw := cli(args...)
					var receipt reporting.Receipt
					if e = json.Unmarshal(raw, &receipt); e != nil {
						t.Fatal(e, string(raw))
					}
					result := reportResult(t, r.root, receipt)
					if len(result.ModelFailures) != 1 || result.ModelFailures[0] != *d || len(result.Attempts) != 0 || result.TargetClosure != "confirmed" {
						t.Fatalf("bad result %+v", result)
					}
					if result.Usage["model_tokens"] != 0 || result.Usage["model_tokens_unconfirmed_reserved"] <= 0 || result.Usage["model_tokens_budget_charged"] != result.Usage["model_tokens_unconfirmed_reserved"] {
						t.Fatal("reservation presented as usage", result.Usage)
					}
					if verb == "export" {
						diagnosticFound := false
						e = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
							if err != nil {
								return err
							}
							if entry.IsDir() {
								return nil
							}
							b, err := os.ReadFile(path)
							if err != nil {
								return err
							}
							if bytes.Contains(b, []byte(`"diagnostic"`)) && bytes.Contains(b, []byte(d.OperationID)) {
								diagnosticFound = true
							}
							for _, secret := range []string{"synthetic-provider-key", "synthetic-private-header", "synthetic-private-body"} {
								if bytes.Contains(b, []byte(secret)) {
									t.Fatal("export leaked secret", path)
								}
							}
							return nil
						})
						if e != nil || !diagnosticFound {
							t.Fatal("missing exported event diagnostic", e)
						}
					}
				}
				e = filepath.WalkDir(r.root, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() {
						return nil
					}
					b, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					for _, secret := range []string{"synthetic-provider-key", "synthetic-private-header", "synthetic-private-body"} {
						if bytes.Contains(b, []byte(secret)) {
							t.Fatal("retained secret", path)
						}
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			})
		}
	}
}
