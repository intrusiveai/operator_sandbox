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
