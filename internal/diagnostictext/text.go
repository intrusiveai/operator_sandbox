// Package diagnostictext contains the common bounded credential-filtering rules
// for provider, target and host-collected diagnostic text.
package diagnostictext

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func Valid(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) }) < 0
}

func Select(value any, limit int, redact func(string) string) (string, string) {
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
	if !Valid(text, len(text)) {
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

func Redactor(secrets []string) func(string) string {
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

func Safe(text string, limit int) (string, string) {
	if text == "" {
		return "", ""
	}
	return Select(text, limit, Redactor(nil))
}
