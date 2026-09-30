//go:build linux || darwin

package livequalification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

func secretPlan(t *testing.T) Plan {
	t.Helper()
	c := credentials.Config{Profiles: []credentials.SecretStoreProfile{{APIVersion: credentials.StoreProfileVersion, Kind: "SecretStoreProfile", ID: "store", BackendKind: "hashicorp-vault", VaultURL: "https://private.example", VaultProxy: true, AllowedLocatorPrefixes: []string{"vault://secret/"}}}, Credentials: []credentials.HostCredentialRef{{APIVersion: credentials.CredentialRefVersion, Kind: "HostCredentialRef", CredentialID: "probe", StoreProfileID: "store", Locator: credentials.Locator{BackendKind: "hashicorp-vault", Mount: "secret", Path: "probe"}, Value: credentials.ValueSelection{Format: "json-string-field", Field: "value"}, CacheTTLSeconds: 30}}}
	raw, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return Plan{APIVersion: Version, CaseID: "case-1", Kind: "secret-store", DeclaredAuthMode: "proxy", CredentialsFile: path, CredentialID: "probe", TimeoutSeconds: 60}
}
func TestPlanOfflineAndSanitized(t *testing.T) {
	p := secretPlan(t)
	q, err := Prepare(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(q.Check(Identity{}))
	for _, forbidden := range []string{"private.example", "vault://", p.CredentialsFile, "probe"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("private configuration in evidence")
		}
	}
	for _, change := range []func(*Plan){func(p *Plan) { p.TimeoutSeconds = 601 }, func(p *Plan) { p.DeclaredAuthMode = "token-sink" }, func(p *Plan) { p.CredentialsFile = "relative" }, func(p *Plan) { p.CredentialID = "missing" }, func(p *Plan) { p.RotationWaitSeconds = 60 }, func(p *Plan) { p.MaximumModelCalls = 3 }} {
		copy := p
		change(&copy)
		if _, err := Prepare(copy); err == nil {
			t.Fatal("accepted invalid plan")
		}
	}
}

func TestLocalAuthenticationPlansMustMatchSelection(t *testing.T) {
	dir := installedTemplates(t)
	for _, tc := range []struct{ file, mode, wrong string }{
		{"gcp-secret-manager-plan.json", "google-adc", "external-account"},
		{"azure-key-vault-plan.json", "azure-cli", "managed-identity"},
		{"azure-key-vault-plan.json", "azure-client-secret", "azure-cli"},
	} {
		raw, _ := os.ReadFile(filepath.Join(dir, tc.file))
		var p Plan
		json.Unmarshal(raw, &p)
		data, _ := os.ReadFile(p.CredentialsFile)
		var cfg credentials.Config
		json.Unmarshal(data, &cfg)
		cfg.Profiles[0].Authentication = tc.mode
		data, _ = json.Marshal(cfg)
		os.WriteFile(p.CredentialsFile, data, 0600)
		p.DeclaredAuthMode = tc.mode
		if _, err := Prepare(p); err != nil {
			t.Fatal(tc.mode, err)
		}
		p.DeclaredAuthMode = tc.wrong
		if _, err := Prepare(p); err == nil {
			t.Fatal("mismatched store auth accepted")
		}
	}
	for _, tc := range []struct{ provider, codec, mode string }{
		{"vertex-gemini", "gemini-text-tools-v1", "google-adc"},
		{"azure-openai", "openai-chat-text-tools-v1", "azure-cli"},
		{"azure-openai", "openai-responses-text-tools-v1", "azure-client-secret"},
	} {
		q, _, _ := providerFixture(t, tc.codec, tc.provider, tc.mode)
		p := q.plan
		p.DeclaredAuthMode = tc.mode
		p.ModelProfileFile = filepath.Join(t.TempDir(), "model.json")
		os.WriteFile(p.ModelProfileFile, q.profile.JSON(), 0600)
		if _, err := Prepare(p); err != nil {
			t.Fatal(tc.mode, err)
		}
		p.DeclaredAuthMode = "workload-identity"
		if _, err := Prepare(p); err == nil {
			t.Fatal("mismatched provider auth accepted")
		}
	}
}
