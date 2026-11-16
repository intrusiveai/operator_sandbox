package diagnostictext

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Failure is a bounded, producer-selected integration observation. It does not
// decide admission, effects, retry safety, or which failure caused shutdown.
// Native codes are data, not an enumerated catalogue of product messages.
type Failure struct {
	Component       string `json:"component"`
	Operation       string `json:"operation"`
	RequestID       string `json:"request_id,omitempty"`
	Category        string `json:"category"`
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
	RemoteRequestID string `json:"remote_request_id,omitempty"`
	HTTPStatus      int    `json:"http_status,omitempty"`
	ResponseDigest  string `json:"response_digest,omitempty"`
	Omissions       string `json:"omissions,omitempty"`
}

var categoryToken = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// Bounded must be called at the producer boundary, before retaining remote data.
func (f Failure) Bounded() Failure {
	if !categoryToken.MatchString(f.Category) {
		f.Category = "unclassified_failure"
		f.Omissions += "category:invalid;"
	}
	for _, field := range []struct {
		name  string
		value *string
		limit int
	}{
		{"component", &f.Component, 64}, {"operation", &f.Operation, 128}, {"request_id", &f.RequestID, 128},
		{"category", &f.Category, 64}, {"code", &f.Code, 128}, {"message", &f.Message, 512}, {"remote_request_id", &f.RemoteRequestID, 128},
	} {
		original := *field.value
		value, omission := Safe(original, field.limit)
		if value != original && omission == "" {
			omission = "redacted"
		}
		*field.value = value
		if omission != "" {
			f.Omissions += field.name + ":" + omission + ";"
		}
	}
	if f.HTTPStatus != 0 && (f.HTTPStatus < 100 || f.HTTPStatus > 599) {
		f.HTTPStatus = 0
		f.Omissions += "http_status:invalid;"
	}
	if f.ResponseDigest != "" && !validHash(f.ResponseDigest) {
		f.ResponseDigest = ""
		f.Omissions += "response_digest:invalid;"
	}
	f.Omissions, _ = Safe(f.Omissions, 512)
	return f
}
func validHash(s string) bool {
	if len(s) != 71 || !strings.HasPrefix(s, "sha256:") {
		return false
	}
	for _, c := range s[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (f Failure) Valid() bool { return f.Component != "" && f.Category != "" && f == f.Bounded() }
func (f Failure) DiagnosticMessage() string {
	text := fmt.Sprintf("%s operation %q: %s", f.Component, f.Operation, f.Category)
	if f.HTTPStatus != 0 {
		text += fmt.Sprintf(" (HTTP %d)", f.HTTPStatus)
	}
	if f.Code != "" {
		text += "; code " + fmt.Sprintf("%q", f.Code)
	}
	if f.Message != "" {
		text += "; reported detail: " + fmt.Sprintf("%q", f.Message)
	} else {
		text += "; no safe error description is available"
	}
	if f.RequestID != "" {
		text += "; request " + fmt.Sprintf("%q", f.RequestID)
	}
	if f.RemoteRequestID != "" {
		text += "; remote request " + fmt.Sprintf("%q", f.RemoteRequestID)
	}
	if f.ResponseDigest != "" {
		text += "; observed response digest " + f.ResponseDigest
	}
	if f.Omissions != "" {
		text += "; omitted/bounded details: " + f.Omissions
	}
	return text
}
func (f Failure) Error() string              { return f.DiagnosticMessage() }
func (f Failure) DiagnosticFailure() Failure { return f }

type failureError struct {
	failure Failure
	cause   error
}

func (e *failureError) Error() string              { return e.failure.DiagnosticMessage() }
func (e *failureError) DiagnosticMessage() string  { return e.failure.DiagnosticMessage() }
func (e *failureError) DiagnosticFailure() Failure { return e.failure }
func (e *failureError) Unwrap() error              { return e.cause }

func WithFailure(err error, f Failure) error {
	if err == nil {
		return nil
	}
	return &failureError{f.Bounded(), err}
}

// FailureFromError follows only the primary error, never promotes joined cleanup.
func FailureFromError(err error) *Failure {
	for err != nil {
		if producer, ok := err.(interface{ DiagnosticFailure() Failure }); ok {
			f := producer.DiagnosticFailure().Bounded()
			if f.Valid() {
				return &f
			}
		}
		err = unwrapPrimary(err)
	}
	return nil
}

// Context applies to any integration implementing the producer diagnostic
// convention. Unknown errors stay private; an existing structured cause wins.
func Context(err error, component, operation, request string) error {
	if err == nil {
		return nil
	}
	if f := FailureFromError(err); f != nil {
		if f.Operation == "" {
			f.Operation = operation
		}
		if f.RequestID == "" {
			f.RequestID = request
		}
		return WithFailure(err, *f)
	}
	message, ok := FromError(err)
	f := Failure{Component: component, Operation: operation, RequestID: request, Category: "operation_failed", Message: message}
	if !ok {
		if errors.Is(primaryCause(err), context.DeadlineExceeded) {
			f.Category = "deadline_exceeded"
			f.Message = "The operation deadline expired."
		} else if errors.Is(primaryCause(err), context.Canceled) {
			f.Category = "canceled"
			f.Message = "The operation was canceled."
		} else {
			f.Omissions = "message:no_safe_producer_detail;"
		}
	}
	return WithFailure(err, f)
}

// primaryCause excludes secondary joined failures when classifying cancellation.
func primaryCause(err error) error {
	for err != nil {
		next := unwrapPrimary(err)
		if next == nil {
			return err
		}
		err = next
	}
	return nil
}
