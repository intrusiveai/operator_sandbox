//go:build linux || darwin

package credentials

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	vault "github.com/hashicorp/vault/api"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// Failure contains selected configuration identifiers and the failed step, never
// a secret locator, value, or backend error string.
type Failure struct {
	CredentialID string `json:"credential_id,omitempty"`
	ProfileID    string `json:"profile_id,omitempty"`
	Backend      string `json:"backend,omitempty"`
	Step         string `json:"step"`
	Category     string `json:"category,omitempty"`
	BackendCode  string `json:"backend_code,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
}

func (f Failure) Valid() bool {
	return (f.Category == "" || safeBackendCode.MatchString(f.Category)) && (f.BackendCode == "" || safeBackendCode.MatchString(f.BackendCode)) && (f.HTTPStatus == 0 || f.HTTPStatus >= 100 && f.HTTPStatus <= 599) && stableID.MatchString(f.CredentialID) && stableID.MatchString(f.ProfileID) && slices.Contains([]string{"aws-secrets-manager", "azure-key-vault", "gcp-secret-manager", "hashicorp-vault", "onepassword"}, f.Backend) && slices.Contains([]string{"backend-initialization", "secret-read", "value-selection", "audit-retention"}, f.Step)
}
func (f Failure) Error() string {
	text := fmt.Sprintf("Operator credential %s via profile %s (%s): %s failed", f.CredentialID, f.ProfileID, f.Backend, f.Step)
	if f.Category != "" {
		text += "; category: " + f.Category
	} else {
		text += "; cause unavailable"
	}
	if f.BackendCode != "" {
		text += "; backend code: " + f.BackendCode
	}
	if f.HTTPStatus != 0 {
		text += fmt.Sprintf("; HTTP %d", f.HTTPStatus)
	}
	return text
}

func (f Failure) DiagnosticMessage() string { return f.Error() }

var safeBackendCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// Only typed status/code interfaces are inspected; error messages can contain
// locators and secret values. The original cause stays in memory for errors.Is.
type failureCause struct {
	Failure
	cause error
}

func (e failureCause) DiagnosticMessage() string {
	message := e.Failure.Error()
	if detail, ok := diagnostictext.FromError(e.cause); ok {
		message += "; " + detail
	}
	return message
}

func (e failureCause) Unwrap() []error { return []error{e.Failure, e.cause} }
func failed(id, profile, backend, step string, cause error) error {
	f := Failure{CredentialID: id, ProfileID: profile, Backend: backend, Step: step, Category: "unknown"}
	var timeout net.Error
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		f.Category = "timeout"
	case errors.Is(cause, context.Canceled):
		f.Category = "canceled"
	case errors.Is(cause, os.ErrPermission):
		f.Category = "permission-denied"
	case errors.Is(cause, os.ErrNotExist):
		f.Category = "not-found"
	case errors.As(cause, &timeout) && timeout.Timeout():
		f.Category = "timeout"
	}
	var apiCode interface{ ErrorCode() string }
	if errors.As(cause, &apiCode) {
		f.BackendCode = apiCode.ErrorCode()
	}
	var httpCode interface{ HTTPStatusCode() int }
	if errors.As(cause, &httpCode) {
		f.HTTPStatus = httpCode.HTTPStatusCode()
	}
	var azure *azcore.ResponseError
	if errors.As(cause, &azure) {
		f.HTTPStatus, f.BackendCode = azure.StatusCode, azure.ErrorCode
	}
	var google *googleapi.Error
	if errors.As(cause, &google) {
		f.HTTPStatus = google.Code
	}
	var vaultError *vault.ResponseError
	if errors.As(cause, &vaultError) {
		f.HTTPStatus = vaultError.StatusCode
	}
	if st, ok := status.FromError(cause); ok {
		f.BackendCode = st.Code().String()
		switch st.Code() {
		case codes.PermissionDenied:
			f.Category = "permission-denied"
		case codes.Unauthenticated:
			f.Category = "authentication"
		case codes.NotFound:
			f.Category = "not-found"
		case codes.DeadlineExceeded:
			f.Category = "timeout"
		case codes.Canceled:
			f.Category = "canceled"
		case codes.ResourceExhausted:
			f.Category = "resource-exhausted"
		case codes.Unavailable:
			f.Category = "unavailable"
		}
	}
	if f.HTTPStatus < 100 || f.HTTPStatus > 599 {
		f.HTTPStatus = 0
	}
	clean, omission := diagnostictext.Safe(f.BackendCode, 64)
	if !safeBackendCode.MatchString(f.BackendCode) || clean != f.BackendCode || omission != "" {
		f.BackendCode = ""
	}
	if f.Category == "unknown" {
		switch f.HTTPStatus {
		case 401:
			f.Category = "authentication"
		case 403:
			f.Category = "permission-denied"
		case 404:
			f.Category = "not-found"
		case 408, 504:
			f.Category = "timeout"
		case 429:
			f.Category = "resource-exhausted"
		default:
			if f.HTTPStatus >= 500 {
				f.Category = "unavailable"
			}
		}
	}
	return failureCause{f, cause}
}

// Preserve typed causes without exposing SDK error text at identity boundaries.
type backendContextError struct {
	operation string
	cause     error
}

func (e backendContextError) Error() string { return e.operation + " failed" }
func (e backendContextError) Unwrap() error { return e.cause }
func (e backendContextError) DiagnosticMessage() string {
	message := e.Error()
	if detail, ok := diagnostictext.FromError(e.cause); ok {
		message += "; " + detail
	}
	return message
}
