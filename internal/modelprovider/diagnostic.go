//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// Diagnostic contains selected facts only. HTTPStatus==0 means no HTTP status was
// observed. Message is host-authored; ProviderMessage is bounded and redacted.
type Diagnostic struct {
	Category          string          `json:"category"`
	Provider          string          `json:"provider,omitempty"`
	Model             string          `json:"model,omitempty"`
	OperationID       string          `json:"operation_id,omitempty"`
	HTTPStatus        int             `json:"http_status,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	ProviderCode      string          `json:"provider_code,omitempty"`
	ProviderMessage   string          `json:"provider_message,omitempty"`
	ProviderType      string          `json:"provider_type,omitempty"`
	ProviderParameter string          `json:"provider_parameter,omitempty"`
	Omissions         DetailOmissions `json:"omissions,omitempty,omitzero"`
	Reason            string          `json:"reason,omitempty"`
	Message           string          `json:"message,omitempty"`
}

var reasons = map[string]string{
	"deadline_exceeded":         "The model request exceeded its deadline.",
	"canceled":                  "The model request was canceled.",
	"credential_unavailable":    "Model credentials could not be prepared.",
	"request_invalid":           "The model request failed local validation.",
	"no_response":               "No HTTP response was received.",
	"read_failed":               "The provider response could not be read completely.",
	"response_too_large":        "The provider response exceeded its size limit.",
	"invalid_media_type":        "The provider did not return supported JSON content.",
	"invalid_json":              "The provider response was not valid bounded JSON.",
	"contract_invalid":          "The provider response failed the model contract checks.",
	"usage_invalid":             "The provider response did not contain valid usage accounting.",
	"usage_unknown":             "Confirmed token usage was unavailable.",
	"usage_exceeds_reservation": "Provider token usage exceeded the reserved allowance.",
	"provider_failed":           "The model provider failed without further safe details.",
	"http_rejected":             "The provider returned an unsuccessful HTTP status.",
}
var requestIDPattern = regexp.MustCompile(`^(req_[a-fA-F0-9]{16,64}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

func (d Diagnostic) Valid() bool {
	switch d.Category {
	case "credential_preparation", "request_validation", "connection", "tls", "timeout", "cancellation", "http_rejection", "response_read", "response_validation", "unknown":
	default:
		return false
	}
	if d.Provider != "" {
		if _, ok := codecs[d.Provider]; !ok {
			return false
		}
	}
	if len(d.Model) > 256 || strings.IndexFunc(d.Model, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 || len(d.OperationID) > 128 || (d.OperationID != "" && !id.MatchString(d.OperationID)) {
		return false
	}
	if d.HTTPStatus != 0 && (d.HTTPStatus < 100 || d.HTTPStatus > 599) {
		return false
	}
	if d.RequestID != "" && !requestIDPattern.MatchString(d.RequestID) {
		return false
	}
	if !validDetail(d.ProviderCode, 128) || !validDetail(d.ProviderType, 128) || !validDetail(d.ProviderParameter, 256) || !validDetail(d.ProviderMessage, 1024) || !d.Omissions.valid() {
		return false
	}
	return reasons[d.Reason] != "" && d.Message == reasons[d.Reason]
}
func (d Diagnostic) WithFailure(category, reason string) Diagnostic {
	d.Category, d.Reason, d.Message = category, reason, reasons[reason]
	return d
}
func (d Diagnostic) Summary() string {
	text := "Model request failed"
	if d.Provider != "" {
		text = d.Provider
	}
	if d.HTTPStatus != 0 {
		text += fmt.Sprintf(" returned HTTP %d", d.HTTPStatus)
	}
	if d.ProviderCode != "" {
		text += " (" + d.ProviderCode + ")"
	}
	return text + ": " + d.Message
}

type Failure struct {
	Diagnostic Diagnostic
	cause      error
}

func (e *Failure) Error() string { return e.Diagnostic.Summary() }
func (e *Failure) Unwrap() error { return e.cause }

// FailureError attaches host-selected diagnostics without retaining a raw error.
func FailureError(d Diagnostic) error {
	if !d.Valid() {
		d = Diagnostic{}.WithFailure("unknown", "provider_failed")
	}
	return &Failure{d, ErrProvider}
}
func Details(err error) *Diagnostic {
	var failure *Failure
	if !errors.As(err, &failure) || !failure.Diagnostic.Valid() {
		return nil
	}
	d := failure.Diagnostic
	return &d
}
func failureCategory(ctx context.Context, err error, fallback string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "cancellation"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var record tls.RecordHeaderError
	if errors.Is(err, http.ErrSchemeMismatch) || errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &record) {
		return "tls"
	}
	return fallback
}
func responseMetadata(provider, model string, r *http.Response, secrets []string) Diagnostic {
	d := Diagnostic{Provider: provider, Model: model, HTTPStatus: r.StatusCode}
	header := "X-Request-Id"
	switch provider {
	case "anthropic-messages":
		header = "Request-Id"
	case "azure-openai":
		header = "Apim-Request-Id"
	case "bedrock-converse":
		header = "X-Amzn-Requestid"
	case "gemini-api", "vertex-gemini":
		header = "X-Goog-Request-Id"
	}
	values := r.Header.Values(header)
	switch {
	case len(values) == 0:
		d.Omissions.RequestID = "not_provided"
	case len(values) != 1:
		d.Omissions.RequestID = "unsupported_value"
	case newDetailRedactor(secrets)(values[0]) != values[0]:
		d.Omissions.RequestID = "credential_redacted"
	case !requestIDPattern.MatchString(values[0]):
		d.Omissions.RequestID = "unrecognized_format"
	default:
		d.RequestID = values[0]
	}
	return d
}

// DecodeDiagnostic accepts only the exact selected structure, rejecting unknown
// fields rather than passing arbitrary journal metadata into exported reports.
func DecodeDiagnostic(raw []byte) *Diagnostic {
	var d Diagnostic
	value, err := contracts.Decode(raw, 16<<10)
	if err != nil {
		return nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for k := range m {
		switch k {
		case "category", "provider", "model", "operation_id", "http_status", "request_id", "provider_code", "provider_message", "provider_type", "provider_parameter", "omissions", "reason", "message":
		default:
			return nil
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || !d.Valid() {
		return nil
	}
	return &d
}
