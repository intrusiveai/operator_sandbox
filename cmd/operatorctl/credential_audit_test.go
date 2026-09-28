//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

type journalCredentialBackend struct{ failed bool }

func (b *journalCredentialBackend) Read(context.Context, credentials.SecretStoreProfile, credentials.Locator) ([]byte, string, error) {
	if b.failed {
		return nil, "", errors.New("remote secret-location leak")
	}
	return []byte("credential-value"), "secret-location/version", nil
}
func TestCredentialAuditJournalContainsOnlyPermittedMetadata(t *testing.T) {
	root, writer := observerFixture(t)
	config := credentials.Config{Profiles: []credentials.SecretStoreProfile{{APIVersion: credentials.StoreProfileVersion, Kind: "SecretStoreProfile", ID: "store", BackendKind: "gcp-secret-manager", AllowedLocatorPrefixes: []string{"gcp://projects/test/secrets/"}}}, Credentials: []credentials.HostCredentialRef{{APIVersion: credentials.CredentialRefVersion, Kind: "HostCredentialRef", CredentialID: "key", StoreProfileID: "store", Locator: credentials.Locator{BackendKind: "gcp-secret-manager", Project: "test", SecretName: "secret-location", Version: "latest"}, Value: credentials.ValueSelection{Format: "utf8"}}}}
	backend := &journalCredentialBackend{}
	resolver, err := credentials.New(config, map[string]credentials.BackendFactory{"gcp-secret-manager": func(context.Context, credentials.SecretStoreProfile) (credentials.Backend, error) {
		return backend, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	audited := resolver.WithAudit(func(event credentials.AuditEvent) error {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		_, err = writer.Append(campaign.Entry{RunRevision: writer.Revision(), Kind: "credential.resolution", Metadata: raw})
		return err
	})
	if _, err := audited.Resolve(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	backend.failed = true
	if _, err := audited.Resolve(context.Background(), "key"); err == nil {
		t.Fatal("failed read accepted")
	}
	writer.Close()
	outcomes := []string{}
	_, err = campaign.Inspect(root, writer.Manifest().CampaignID, func(event campaign.Event) error {
		raw, _ := json.Marshal(event)
		for _, secret := range []string{"credential-value", "secret-location", "gcp://", "remote secret"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("journal leaked secret material")
			}
		}
		if event.Kind == "credential.resolution" {
			var value credentials.AuditEvent
			if err := json.Unmarshal(event.Metadata, &value); err != nil {
				return err
			}
			outcomes = append(outcomes, value.Outcome)
		}
		return nil
	})
	if err != nil || strings.Join(outcomes, ",") != "resolved,failed" {
		t.Fatal(outcomes, err)
	}
	backend.failed = false
	if r, err := audited.Resolve(context.Background(), "key"); !errors.Is(err, credentials.ErrAudit) || r.Value != "" {
		t.Fatal("closed journal exposed value", err)
	}
}
