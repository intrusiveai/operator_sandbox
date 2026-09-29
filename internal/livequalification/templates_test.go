//go:build linux || darwin

package livequalification

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

func installedTemplates(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files, err := filepath.Glob("../../release/templates/qualification/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		raw = []byte(strings.ReplaceAll(string(raw), "/REPLACE/private", dir))
		if err = os.WriteFile(filepath.Join(dir, filepath.Base(name)), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func TestEveryDistributedPlanValidatesOffline(t *testing.T) {
	dir := installedTemplates(t)
	plans, _ := filepath.Glob(filepath.Join(dir, "*-plan.json"))
	if len(plans) != 16 {
		t.Fatalf("expected 12 provider and 4 secret plans, got %d", len(plans))
	}
	for _, plan := range plans {
		t.Run(filepath.Base(plan), func(t *testing.T) {
			if _, err := Load(plan); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAllStoresCacheExpiryRotationAndFailure(t *testing.T) {
	for _, backend := range []string{"aws-secrets-manager", "azure-key-vault", "gcp-secret-manager", "hashicorp-vault"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			dir := installedTemplates(t)
			q, err := Load(filepath.Join(dir, backend+"-plan.json"))
			if err != nil {
				t.Fatal(err)
			}
			q.plan.TimeoutSeconds = 30
			q.plan.RotationWaitSeconds = 1
			q.cacheTTL = 1
			q.config.Credentials[0].CacheTTLSeconds = 1
			calls := 0
			factories := map[string]credentials.BackendFactory{backend: func(context.Context, credentials.SecretStoreProfile) (credentials.Backend, error) {
				return backendFunc(func(_ context.Context, _ credentials.SecretStoreProfile, l credentials.Locator) ([]byte, string, error) {
					calls++
					if strings.HasSuffix(l.SecretID+l.SecretName+l.Path, "-missing") {
						return nil, "", errors.New("private backend failure")
					}
					if calls >= 4 {
						return []byte(`{"value":"rotated-secret"}`), "v2", nil
					}
					return []byte(`{"value":"original-secret"}`), "v1", nil
				}), nil
			}}
			checks := map[string]string{}
			if err = q.run(context.Background(), Identity{}, func(r Record) error { checks[r.Check] = r.Status; return nil }, factories); err != nil {
				t.Fatal(err, checks)
			}
			for _, name := range []string{"cache_hit", "cache_expiry", "rotation", "expected_failure"} {
				if checks[name] != "passed" {
					t.Fatal(name, checks)
				}
			}
			if calls != 5 {
				t.Fatal(calls)
			}
		})
	}
}
func TestStrictPlanLoading(t *testing.T) {
	p := secretPlan(t)
	raw, _ := json.Marshal(p)
	name := filepath.Join(t.TempDir(), "plan.json")
	for _, bad := range [][]byte{append(raw, raw...), []byte(strings.Replace(string(raw), `"case_id":`, `"unknown":true,"case_id":`, 1)), []byte(strings.Replace(string(raw), `"case_id":`, `"case_id":"duplicate","case_id":`, 1))} {
		os.WriteFile(name, bad, 0600)
		if _, err := Load(name); err == nil {
			t.Fatal("accepted malformed plan")
		}
	}
}
