//go:build linux || darwin

package credentials

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/aws/smithy-go"
	vault "github.com/hashicorp/vault/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCredentialFailureRetainsTypedFactsWithoutBackendText(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		category, code string
		status         int
	}{
		{"aws", &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "SYNTHETIC_SECRET"}, "unknown", "AccessDeniedException", 0},
		{"azure", &azcore.ResponseError{StatusCode: 403, ErrorCode: "Forbidden"}, "permission-denied", "Forbidden", 403},
		{"google", status.Error(codes.NotFound, "SYNTHETIC_SECRET"), "not-found", "NotFound", 0},
		{"vault", &vault.ResponseError{StatusCode: 429, Errors: []string{"SYNTHETIC_SECRET"}}, "resource-exhausted", "", 429},
		{"timeout", context.DeadlineExceeded, "timeout", "", 0},
		{"unknown", errors.New("SYNTHETIC_SECRET"), "unknown", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := failed("key", "profile", "aws-secrets-manager", "secret-read", tc.err)
			var f Failure
			if !errors.As(err, &f) || !f.Valid() || f.Category != tc.category || f.BackendCode != tc.code || f.HTTPStatus != tc.status || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
				t.Fatal(err, f)
			}
			if !errors.Is(err, tc.err) {
				t.Fatal("cause lost in memory")
			}
		})
	}
}

func TestAuditRetainsTypedFailureFacts(t *testing.T) {
	for _, setup := range []bool{false, true} {
		cause := status.Error(codes.PermissionDenied, "SYNTHETIC_SECRET")
		b := &fakeBackend{err: cause}
		r, err := New(validConfig(), map[string]BackendFactory{"gcp-secret-manager": func(context.Context, SecretStoreProfile) (Backend, error) {
			if setup {
				return nil, cause
			}
			return b, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		var event AuditEvent
		_, err = r.WithAudit(func(e AuditEvent) error { event = e; return nil }).Resolve(context.Background(), "openai")
		r.Close()
		if err == nil || event.Category != "permission-denied" || event.BackendCode != "PermissionDenied" || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
			t.Fatal(event, err)
		}
	}
}
