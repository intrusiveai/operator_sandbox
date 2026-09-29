//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/livequalification"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestQualificationCLIOptInAndEvidenceExclusion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob("../../release/templates/qualification/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		raw, _ := os.ReadFile(name)
		raw = []byte(strings.ReplaceAll(string(raw), "/REPLACE/private", dir))
		if err = os.WriteFile(filepath.Join(dir, filepath.Base(name)), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := filepath.Join(dir, "hashicorp-vault-plan.json")
	var out, diagnostics bytes.Buffer
	if code := run(context.Background(), []string{"qualify", "--plan", plan}, &out, &diagnostics); code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	if !strings.Contains(out.String(), "local_configuration_only") || strings.Contains(out.String(), "vault.example") {
		t.Fatal(out.String())
	}
	evidence := filepath.Join(dir, "evidence.jsonl")
	os.WriteFile(evidence, []byte("preserve"), 0600)
	for _, args := range [][]string{{"qualify", "--plan", plan, "--output", evidence}, {"qualify", "--plan", plan, "--live", "--output", evidence}} {
		out.Reset()
		diagnostics.Reset()
		if code := run(context.Background(), args, &out, &diagnostics); code == 0 {
			t.Fatal("accepted unsafe invocation")
		}
	}
	raw, _ := os.ReadFile(evidence)
	if string(raw) != "preserve" {
		t.Fatal("overwrote evidence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output := filepath.Join(dir, "canceled.jsonl")
	if code := run(ctx, []string{"qualify", "--plan", plan, "--live", "--output", output}, &out, &diagnostics); code != 1 {
		t.Fatal(code)
	}
	raw, _ = os.ReadFile(output)
	var last map[string]any
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != 2 {
		t.Fatal(string(raw))
	}
	json.Unmarshal(lines[1], &last)
	if last["status"] != "failed" || last["code"] != "canceled" {
		t.Fatal(last)
	}
}

func TestQualificationCommandThroughRealVaultTLS(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Header.Get("X-Vault-Token") != "" {
			t.Error("proxy sent ambient token")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "-missing") {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"errors":["PRIVATE-REMOTE-ERROR"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"data":{"value":"PRIVATE-SECRET-VALUE"},"metadata":{"version":1}}}`))
	}))
	defer server.Close()
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	raw, err := os.ReadFile("../../release/templates/qualification/hashicorp-vault-credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	var config credentials.Config
	if json.Unmarshal(raw, &config) != nil {
		t.Fatal("fixture")
	}
	config.Profiles[0].VaultURL = server.URL
	config.Profiles[0].VaultCACertificate = ca
	config.Credentials[0].CacheTTLSeconds = 1
	cfg := filepath.Join(dir, "credentials.json")
	raw, _ = json.Marshal(config)
	os.WriteFile(cfg, raw, 0600)
	plan := livequalification.Plan{APIVersion: livequalification.Version, CaseID: "vault-tls-fixture", Kind: "secret-store", DeclaredAuthMode: "proxy", CredentialsFile: cfg, CredentialID: "qualification-secret", FailureCredentialID: "qualification-missing", TimeoutSeconds: 30}
	planPath := filepath.Join(dir, "plan.json")
	raw, _ = json.Marshal(plan)
	os.WriteFile(planPath, raw, 0600)
	var out, diagnostics bytes.Buffer
	output := filepath.Join(dir, "evidence.jsonl")
	code := run(context.Background(), []string{"qualify", "--plan", planPath, "--live", "--output", output}, &out, &diagnostics)
	if code != 0 {
		t.Fatal(code, diagnostics.String())
	}
	raw, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 4 {
		t.Fatal("unexpected backend reads", reads.Load())
	}
	for _, private := range []string{"PRIVATE-SECRET-VALUE", "PRIVATE-REMOTE-ERROR", server.URL, cfg} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("evidence leak")
		}
	}
	if !bytes.Contains(raw, []byte(`"code":"expired_entry_refreshed"`)) {
		t.Fatal("expiry missing")
	}
}
