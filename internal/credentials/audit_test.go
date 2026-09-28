//go:build linux || darwin

package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAuditedResolutionCacheFailureAndRedaction(t *testing.T) {
	backend := &fakeBackend{value: []byte(`{"api_key":"private-value"}`), version: "private/backend/path"}
	r, err := New(validConfig(), map[string]BackendFactory{"gcp-secret-manager": func(context.Context, SecretStoreProfile) (Backend, error) { return backend, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var events []AuditEvent
	sinkFailure := false
	a := r.WithAudit(func(e AuditEvent) error {
		events = append(events, e)
		if sinkFailure {
			return errors.New("journal failed")
		}
		return nil
	})
	for i := 0; i < 2; i++ {
		if result, err := a.Resolve(context.Background(), "openai"); err != nil || result.Value != "private-value" {
			t.Fatal(result, err)
		}
	}
	if len(events) != 2 || events[0].CacheHit || !events[1].CacheHit || events[1].VersionDigest == "" {
		t.Fatal(events)
	}
	sinkFailure = true
	if result, err := a.Resolve(context.Background(), "openai"); !errors.Is(err, ErrAudit) || result.Value != "" {
		t.Fatal("unlogged value exposed", result, err)
	}
	sinkFailure = false
	backend.err = errors.New("private-value at private/backend/path")
	if _, err := a.Resolve(context.Background(), "openai"); err == nil {
		t.Fatal("failed refresh used cache")
	}
	if backend.calls != 2 || events[3].Outcome != "failed" {
		t.Fatal(events, backend.calls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Resolve(canceled, "openai"); err == nil || events[4].Code != "canceled" {
		t.Fatal(events, err)
	}
	if _, err := a.Resolve(context.Background(), "private-untrusted-id"); err == nil || events[5].CredentialID != "" {
		t.Fatal(events, err)
	}
	raw, _ := json.Marshal(events)
	for _, secret := range []string{"private-value", "private/backend/path", "private-untrusted-id", "security-prod", "interceptor-openai", "latest", "api_key"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("audit disclosed %q", secret)
		}
	}
}

func TestAuditInitializationAndMissingSink(t *testing.T) {
	r, err := New(validConfig(), map[string]BackendFactory{"gcp-secret-manager": func(context.Context, SecretStoreProfile) (Backend, error) { return nil, errors.New("remote-secret") }})
	if err != nil {
		t.Fatal(err)
	}
	var event AuditEvent
	a := r.WithAudit(func(e AuditEvent) error { event = e; return nil })
	if _, err := a.Resolve(context.Background(), "openai"); err == nil || event.Code != "resolution_failed" {
		t.Fatal(event, err)
	}
	if _, err := r.WithAudit(nil).Resolve(context.Background(), "openai"); !errors.Is(err, ErrAudit) {
		t.Fatal(err)
	}
}
