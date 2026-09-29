//go:build linux || darwin

package livequalification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

type backendFunc func(context.Context, credentials.SecretStoreProfile, credentials.Locator) ([]byte, string, error)

func (f backendFunc) Read(c context.Context, p credentials.SecretStoreProfile, l credentials.Locator) ([]byte, string, error) {
	return f(c, p, l)
}
func TestSecretProbesAndSinkFailure(t *testing.T) {
	q, err := Prepare(secretPlan(t))
	if err != nil {
		t.Fatal(err)
	}
	q.plan.TimeoutSeconds = 1 // expiry must be recorded not_run rather than exceed budget
	calls := 0
	factories := map[string]credentials.BackendFactory{"hashicorp-vault": func(context.Context, credentials.SecretStoreProfile) (credentials.Backend, error) {
		return backendFunc(func(context.Context, credentials.SecretStoreProfile, credentials.Locator) ([]byte, string, error) {
			calls++
			return []byte(`{"value":"DO_NOT_RECORD"}`), "SECRET_VERSION", nil
		}), nil
	}}
	var records []Record
	err = q.run(context.Background(), Identity{}, func(r Record) error { records = append(records, r); return nil }, factories)
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	raw, _ := json.Marshal(records)
	for _, forbidden := range []string{"DO_NOT_RECORD", "SECRET_VERSION", "private.example"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("evidence leak")
		}
	}
	checks := map[string]string{}
	for _, r := range records {
		checks[r.Check] = r.Status
	}
	if checks["cache_hit"] != "passed" || checks["pre_canceled_resolution"] != "passed" || checks["cache_expiry"] != "not_run" || checks["rotation"] != "not_run" {
		t.Fatal(checks)
	}
	calls = 0
	if q.run(context.Background(), Identity{}, func(Record) error { return errors.New("disk full") }, factories) == nil || calls != 0 {
		t.Fatal("dispatch despite evidence failure")
	}
	calls = 0
	if q.run(context.Background(), Identity{}, func(r Record) error {
		if r.Check == "refresh" {
			return errors.New("disk full")
		}
		return nil
	}, factories) == nil || calls != 1 {
		t.Fatal("continued after mid-run evidence failure")
	}
}
func TestSecretFailureDoesNotExposeBackendError(t *testing.T) {
	q, _ := Prepare(secretPlan(t))
	var records []Record
	f := map[string]credentials.BackendFactory{"hashicorp-vault": func(context.Context, credentials.SecretStoreProfile) (credentials.Backend, error) {
		return nil, errors.New("DO_NOT_RECORD")
	}}
	if q.run(context.Background(), Identity{}, func(r Record) error { records = append(records, r); return nil }, f) == nil {
		t.Fatal("failure accepted")
	}
	raw, _ := json.Marshal(records)
	if strings.Contains(string(raw), "DO_NOT_RECORD") {
		t.Fatal("error leaked")
	}
}
