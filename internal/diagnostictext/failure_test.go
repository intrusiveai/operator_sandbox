package diagnostictext

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIntegrationContextPreservesPrimaryAndBounds(t *testing.T) {
	primary := New("permission denied while scanning the mutable root")
	err := Context(errors.Join(primary, context.DeadlineExceeded), "Interceptor", "snapshot.create", "req-1")
	f := FailureFromError(err)
	if f == nil || !f.Valid() || f.Category != "operation_failed" || !strings.Contains(f.Message, "permission denied") || !errors.Is(err, primary) {
		t.Fatal(f, err)
	}
	// An unmarked error must not acquire its secondary cleanup error's category.
	f = FailureFromError(Context(errors.Join(errors.New("private detail"), context.DeadlineExceeded), "Docker", "remove", ""))
	if f.Category != "operation_failed" || f.Message != "" || !strings.Contains(f.Omissions, "no_safe_producer_detail") {
		t.Fatal(f)
	}
	f = FailureFromError(Context(context.DeadlineExceeded, "Interceptor", "status", "req-2"))
	if f.Category != "deadline_exceeded" {
		t.Fatal(f)
	}
	bounded := (Failure{Component: "Interceptor", Category: "remote_rejection", Message: strings.Repeat("x", 600) + " api_key=sk-secret", HTTPStatus: 42}).Bounded()
	if !bounded.Valid() || bounded.HTTPStatus != 0 || len(bounded.Message) > 512 || !strings.Contains(bounded.Omissions, "truncated") || strings.Contains(bounded.DiagnosticMessage(), "sk-secret") {
		t.Fatal(bounded)
	}
}
