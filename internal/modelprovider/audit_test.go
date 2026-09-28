//go:build linux || darwin

package modelprovider

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

type auditBackend struct{}

func (auditBackend) Read(context.Context, credentials.SecretStoreProfile, credentials.Locator) ([]byte, string, error) {
	return []byte("synthetic-key"), "7", nil
}

type auditTransport struct{ calls int }

func (t *auditTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, errors.New("test network refused")
}

func TestAuditFailurePreventsProviderDispatch(t *testing.T) {
	config := credentials.Config{Profiles: []credentials.SecretStoreProfile{{APIVersion: credentials.StoreProfileVersion, Kind: "SecretStoreProfile", ID: "store", BackendKind: "gcp-secret-manager", AllowedLocatorPrefixes: []string{"gcp://projects/test/secrets/"}}}, Credentials: []credentials.HostCredentialRef{{APIVersion: credentials.CredentialRefVersion, Kind: "HostCredentialRef", CredentialID: "model-key", StoreProfileID: "store", Locator: credentials.Locator{BackendKind: "gcp-secret-manager", Project: "test", SecretName: "key", Version: "latest"}, Value: credentials.ValueSelection{Format: "utf8"}}}}
	resolver, err := credentials.New(config, map[string]credentials.BackendFactory{"gcp-secret-manager": func(context.Context, credentials.SecretStoreProfile) (credentials.Backend, error) {
		return auditBackend{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	writes := 0
	fail := true
	audited := resolver.WithAudit(func(credentials.AuditEvent) error {
		writes++
		if fail {
			return errors.New("disk full")
		}
		return nil
	})
	client, err := New(context.Background(), profile(t, "openai-chat", "https://unused.example"), audited)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	transport := &auditTransport{}
	client.http.Transport = transport
	if _, err := client.Generate(context.Background(), nativeRequest("openai-chat-text-tools-v1")); err == nil || transport.calls != 0 || writes != 1 {
		t.Fatal("unaudited provider dispatch", transport.calls, writes, err)
	}
	fail = false
	if _, err := client.Generate(context.Background(), nativeRequest("openai-chat-text-tools-v1")); err == nil || transport.calls != 1 || writes != 2 {
		t.Fatal("audited provider did not dispatch once", transport.calls, writes, err)
	}
}
