//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

// SDK worker behavior is exercised in credentials; this verifies its consumer
// path through the real model transport, durable audit and separate CLI export.
func TestOnePasswordCredentialModelAndExportWorkflow(t *testing.T) {
	root, writer := observerFixture(t)
	raw, err := os.ReadFile("../../internal/credentials/testdata/onepassword.json")
	if err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(root, "credentials.json")
	os.WriteFile(cfgFile, raw, 0600)
	config, err := credentials.Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	backend := &journalCredentialBackend{}
	resolver, err := credentials.New(config, map[string]credentials.BackendFactory{"onepassword": func(context.Context, credentials.SecretStoreProfile) (credentials.Backend, error) {
		return backend, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	audited := resolver.WithAudit(func(event credentials.AuditEvent) error {
		raw, _ := json.Marshal(event)
		_, err := writer.Append(campaign.Entry{RunRevision: writer.Revision(), Kind: "credential.resolution", Metadata: raw})
		return err
	})
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer credential-value" {
			t.Error("credential was not used")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"controlled-response","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	settings := modelprovider.Settings{APIVersion: modelprovider.Version, ID: "test", Provider: "openai-chat", Codec: "openai-chat-text-tools-v1", Model: "test-model", Endpoint: server.URL + "/chat/completions", CACertificatesPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), Authentication: "secret-store", CredentialID: "model-key", MaximumPromptTokens: 10000, MaximumCompletionTokens: 100, MaximumResponseBytes: 65536, CodecOptions: json.RawMessage(`{"instruction_role":"developer","response_models":["test-model"]}`)}
	raw, _ = json.Marshal(settings)
	profile, err := modelprovider.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	client, err := modelprovider.New(context.Background(), profile, audited)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request := []byte(`{"model":"test-model","stream":false,"store":false,"max_completion_tokens":10,"messages":[]}`)
	for i := 0; i < 2; i++ {
		if _, err = client.Generate(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	backend.failed = true
	resolver.Invalidate("model-key")
	if _, err = client.Generate(context.Background(), request); err == nil || calls.Load() != 2 {
		t.Fatal("failed credential dispatched model request", err, calls.Load())
	}
	writer.Close()
	export := filepath.Join(t.TempDir(), "export")
	var out, diag bytes.Buffer
	if code := run(context.Background(), []string{"export", "--campaign", writer.Manifest().CampaignID, "--state-root", root, "--output", export}, &out, &diag); code != 0 {
		t.Fatal(code, diag.String())
	}
	var evidence strings.Builder
	err = filepath.Walk(export, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			evidence.Write(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"credential-value", "secret-location", "op://", "private remote error"} {
		if strings.Contains(evidence.String()+out.String()+diag.String(), secret) {
			t.Fatal("export leaked credential details")
		}
	}
	for _, fact := range []string{"onepassword", "model-key", "developer-secrets", `"cache_hit":true`, `"outcome":"failed"`} {
		if !strings.Contains(strings.ReplaceAll(evidence.String(), " ", ""), fact) {
			t.Fatalf("missing audit fact %s", fact)
		}
	}
	code, _, detail := observeCLI(t, context.Background(), "status", root)
	if code != 0 {
		t.Fatal(code, detail)
	}
}
