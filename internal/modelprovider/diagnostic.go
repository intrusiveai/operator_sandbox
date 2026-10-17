//go:build linux || darwin

package modelprovider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// Diagnostic contains selected facts only. HTTPStatus==0 means no HTTP status was
// observed. Message is host-authored; arbitrary provider messages are never kept.
type Diagnostic struct {
	Category     string `json:"category"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	OperationID  string `json:"operation_id,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
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
var safeCodes = map[string]bool{}

func init() {
	for _, v := range strings.Fields("invalid_api_key invalid_request_error invalid_request invalid_value unsupported_parameter missing_required_parameter model_not_found context_length_exceeded insufficient_quota rate_limit_exceeded rate_limit_error authentication_error permission_error permission_denied not_found_error overloaded_error server_error internal_server_error invalid_prompt content_filter content_policy_violation invalid_request_body InvalidRequest InvalidApiKey Unauthorized Forbidden BadRequest TooManyRequests InternalServerError ServiceUnavailable AccessDeniedException ValidationException ThrottlingException ModelTimeoutException ModelErrorException ServiceUnavailableException InternalServerException ResourceNotFoundException INVALID_ARGUMENT UNAUTHENTICATED PERMISSION_DENIED RESOURCE_EXHAUSTED NOT_FOUND INTERNAL UNAVAILABLE DEADLINE_EXCEEDED") {
		safeCodes[v] = true
	}
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
	if d.ProviderCode != "" && !safeCodes[d.ProviderCode] {
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
func responseMetadata(provider, model string, r *http.Response, secret string) Diagnostic {
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
	if len(values) == 1 && requestIDPattern.MatchString(values[0]) && (secret == "" || !strings.Contains(values[0], secret)) {
		d.RequestID = values[0]
	}
	return d
}

// Error bodies are read only to a small independent ceiling. Neither body text
// nor provider messages survive parsing. Unknown codes are omitted.
func errorCode(r *http.Response, maximum int64, secret string) string {
	limit := min(maximum, int64(16<<10))
	if r.ContentLength > limit || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return ""
	}
	value, err := contracts.Decode(raw, int(limit))
	m, ok := value.(map[string]any)
	if err != nil || !ok {
		return ""
	}
	candidate := []any{}
	if e, ok := m["error"].(map[string]any); ok {
		candidate = append(candidate, e["code"], e["type"], e["status"])
	}
	candidate = append(candidate, m["__type"], m["type"])
	for _, v := range candidate {
		code, ok := v.(string)
		if ok && safeCodes[code] && (secret == "" || !strings.Contains(code, secret)) {
			return code
		}
	}
	return ""
}

// DecodeDiagnostic accepts only the exact selected structure, rejecting unknown
// fields rather than passing arbitrary journal metadata into exported reports.
func DecodeDiagnostic(raw []byte) *Diagnostic {
	var d Diagnostic
	value, err := contracts.Decode(raw, 4096)
	if err != nil {
		return nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for k := range m {
		switch k {
		case "category", "provider", "model", "operation_id", "http_status", "request_id", "provider_code", "reason", "message":
		default:
			return nil
		}
	}
	if json.Unmarshal(raw, &d) != nil || !d.Valid() {
		return nil
	}
	return &d
}
