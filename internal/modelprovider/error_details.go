//go:build linux || darwin

package modelprovider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// DetailOmissions distinguishes absent fields from unreadable or unsafe fields.
// A truncated value retains a redacted prefix; every other reason omits the value.
// Value fields keep persisted diagnostics comparable and copies independent.
type DetailOmissions struct {
	Message   string `json:"message,omitempty"`
	Type      string `json:"type,omitempty"`
	Code      string `json:"code,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func (o DetailOmissions) valid() bool {
	for _, reason := range []string{o.Message, o.Type, o.Code, o.Parameter, o.RequestID} {
		switch reason {
		case "", "not_provided", "unsupported_value", "truncated", "unsafe_control", "body_size_limit", "unsupported_content_type", "unsupported_encoding", "body_read_failed", "invalid_json", "unrecognized_format", "credential_redacted":
		default:
			return false
		}
	}
	return true
}
func validDetail(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) }) < 0
}

// ProviderDetailsText is safe for a terminal; strings are quoted to distinguish
// provider text from host explanations. Reports render this text as a code block.
func (d Diagnostic) ProviderDetailsText() string {
	var out strings.Builder
	for _, f := range []struct{ name, value, omission string }{
		{"message", d.ProviderMessage, d.Omissions.Message},
		{"type", d.ProviderType, d.Omissions.Type},
		{"code", d.ProviderCode, d.Omissions.Code},
		{"parameter", d.ProviderParameter, d.Omissions.Parameter},
		{"request ID", "", d.Omissions.RequestID},
	} {
		if f.value != "" {
			fmt.Fprintf(&out, "Provider %s: %q\n", f.name, f.value)
		}
		if f.omission != "" {
			fmt.Fprintf(&out, "Provider %s omission: %s\n", f.name, f.omission)
		}
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// Only these selected error fields survive. A body is never retained, including
// when it cannot be parsed; each affected field then records why it was omitted.
func (d *Diagnostic) readErrorDetails(r *http.Response, maximum int64, secrets []string) {
	redact := newDetailRedactor(secrets)
	// Bedrock can supply the error type in its designated header even if the
	// body is unreadable. Keep that independently observed, sanitized fact.
	if d.Provider == "bedrock-converse" {
		values := r.Header.Values("X-Amzn-Errortype")
		if len(values) == 1 {
			d.ProviderType, d.Omissions.Type = selectDetail(values[0], 128, redact)
		}
		if len(values) > 1 {
			d.Omissions.Type = "unsupported_value"
		}
	}
	omit := func(reason string) {
		d.Omissions.Message = reason
		if d.ProviderType == "" && d.Omissions.Type == "" {
			d.Omissions.Type = reason
		}
		d.Omissions.Code = reason
		d.Omissions.Parameter = reason
	}
	limit := min(maximum, int64(16<<10))
	if r.ContentLength > limit {
		omit("body_size_limit")
		return
	}
	if encoding := r.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		omit("unsupported_encoding")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "application/json" && media != "application/x-amz-json-1.1") {
		omit("unsupported_content_type")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if int64(len(raw)) > limit {
		omit("body_size_limit")
		return
	}
	if err != nil {
		omit("body_read_failed")
		return
	}
	value, err := contracts.Decode(raw, int(limit))
	root, ok := value.(map[string]any)
	if err != nil || !ok || !utf8.Valid(raw) {
		omit("invalid_json")
		return
	}
	fields := root
	if nested, ok := root["error"].(map[string]any); ok {
		fields = nested
	} else if value := root["error"]; value != nil {
		if _, ok := value.(string); !ok {
			omit("unsupported_value")
			return
		}
	}
	message := first(fields, "message", "Message")
	// OpenAI-compatible gateways can use a string error/detail instead of an object.
	if message == nil {
		if v, ok := root["error"].(string); ok {
			message = v
		} else if v, ok := root["detail"].(string); ok {
			message = v
		}
	}
	kind := first(fields, "type", "__type")
	code := first(fields, "code", "Code")
	parameter := first(fields, "param", "parameter", "target")
	if d.Provider == "gemini-api" || d.Provider == "vertex-gemini" {
		if kind == nil {
			kind = fields["status"]
		}
		if parameter == nil {
			parameter = googleErrorParameters(fields)
		}
	}
	d.ProviderMessage, d.Omissions.Message = selectDetail(message, 1024, redact)
	if kind != nil || (d.ProviderType == "" && d.Omissions.Type == "") {
		d.ProviderType, d.Omissions.Type = selectDetail(kind, 128, redact)
	}
	d.ProviderCode, d.Omissions.Code = selectDetail(code, 128, redact)
	d.ProviderParameter, d.Omissions.Parameter = selectDetail(parameter, 256, redact)
}
func first(m map[string]any, names ...string) any {
	for _, name := range names {
		if value := m[name]; value != nil {
			return value
		}
	}
	return nil
}
func googleErrorParameters(fields map[string]any) any {
	details, _ := fields["details"].([]any)
	var names []string
	for _, value := range details {
		detail, ok := value.(map[string]any)
		if !ok || detail["@type"] != "type.googleapis.com/google.rpc.BadRequest" {
			continue
		}
		violations, valid := detail["fieldViolations"].([]any)
		if !valid && detail["fieldViolations"] != nil {
			return false
		}
		for _, value := range violations {
			field, ok := value.(map[string]any)
			if !ok {
				return false
			}
			if name, ok := field["field"].(string); ok && name != "" {
				names = append(names, name)
			} else if field["field"] != nil {
				return false
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return strings.Join(names, ", ")
}
func selectDetail(value any, limit int, redact func(string) string) (string, string) {
	if value == nil {
		return "", "not_provided"
	}
	text, ok := value.(string)
	if number, numeric := value.(json.Number); numeric {
		text = number.String()
		ok = true
	}
	if !ok {
		return "", "unsupported_value"
	}
	if text == "" {
		return "", "not_provided"
	}
	// Redact before bounding, so truncation can never expose a credential prefix.
	text = redact(text)
	if text == "[REDACTED]" || text == "[REDACTED_URL]" || text == "[REDACTED_AUTHORIZATION]" {
		return "", "credential_redacted"
	}
	text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	if !validDetail(text, len(text)) {
		return "", "unsafe_control"
	}
	if len(text) > limit {
		text = text[:limit]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		return text, "truncated"
	}
	return text, ""
}

var sensitiveURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var credentialToken = regexp.MustCompile(`(?i)\b(?:sk-[a-z0-9_*.-]+|AIza[a-z0-9_-]+|(?:AKIA|ASIA)[A-Z0-9]{16}|eyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+)`)
var credentialAssignment = regexp.MustCompile(`(?i)\b(authorization|api[-_ ]?key|access[-_ ]?token|refresh[-_ ]?token|session[-_ ]?token|password|secret[-_ ]?(?:access[-_ ]?)?key|credential|signature)\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
var bearerCredential = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[^\s,"';]+`)

func newDetailRedactor(secrets []string) func(string) string {
	// Credentials stay in this request-local closure, never in diagnostic records.
	var variants []string
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		variants = append(variants, secret, url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret)))
	}
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	return func(text string) string {
		for _, secret := range variants {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
		text = sensitiveURL.ReplaceAllString(text, "[REDACTED_URL]")
		text = credentialToken.ReplaceAllString(text, "[REDACTED]")
		text = bearerCredential.ReplaceAllString(text, "[REDACTED_AUTHORIZATION]")
		return credentialAssignment.ReplaceAllString(text, "$1=[REDACTED]")
	}
}

// Gather only credential-bearing request headers; ordinary headers are not copied.
func diagnosticSecrets(req *http.Request, token string, signed []string) []string {
	secrets := append([]string{token}, signed...)
	for _, header := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key", "X-Amz-Security-Token"} {
		secrets = append(secrets, req.Header.Get(header))
	}
	return secrets
}
