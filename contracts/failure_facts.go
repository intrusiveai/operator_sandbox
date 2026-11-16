package contracts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FailureFacts contains bounded producer-selected facts, not request/response bodies.
// Names describe observations (for example requested_bytes), not rendering templates.
type FailureFacts map[string]any

var factName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var reasonName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func SanitizeFailureFacts(in *FailureFacts) (*FailureFacts, bool) {
	if in == nil {
		return nil, false
	}
	out := FailureFacts{}
	omitted := false
	keys := make([]string, 0, len(*in))
	for k := range *in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(out) >= 16 {
			omitted = true
			break
		}
		v := (*in)[k]
		if !factName.MatchString(k) {
			omitted = true
			continue
		}
		switch n := v.(type) {
		case string:
			if len(n) <= 256 && utf8.ValidString(n) && !sensitiveFieldName.MatchString(n) && !strings.Contains(n, "://") && strings.IndexFunc(n, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) }) < 0 {
				out[k] = n
			} else {
				omitted = true
			}
		case bool:
			out[k] = n
		case int:
			if int64(n) >= -MaxSafeInteger && int64(n) <= MaxSafeInteger {
				out[k] = n
			} else {
				omitted = true
			}
		case int64:
			if n >= -MaxSafeInteger && n <= MaxSafeInteger {
				out[k] = n
			} else {
				omitted = true
			}
		case float64:
			if n >= -MaxSafeInteger && n <= MaxSafeInteger && n == float64(int64(n)) {
				out[k] = n
			} else {
				omitted = true
			}
		case json.Number:
			i, e := n.Int64()
			if e == nil && i >= -MaxSafeInteger && i <= MaxSafeInteger {
				out[k] = i
			} else {
				omitted = true
			}
		default:
			omitted = true
		}
	}
	return &out, omitted
}
func (f *FailureFacts) Valid() bool {
	if f == nil {
		return true
	}
	_, omitted := SanitizeFailureFacts(f)
	return !omitted
}
func (f *FailureFacts) Text() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	return " Facts (data): " + jsonData(f) + "."
}
func (e *ArgumentError) WithFacts(f FailureFacts) *ArgumentError { e.Facts = &f; return e }

// ShortCorrection keeps long guidance in the structured diagnostic, not the envelope headline.
func ShortCorrection(correction string) string {
	if len(correction) <= 512 {
		return correction
	}
	return "The operation was rejected before execution; follow the structured correction."
}
func QuotedPath(path string) string { return fmt.Sprintf("%q", path) }
func SafeReason(reason string) bool {
	return len(reason) <= 64 && reasonName.MatchString(reason)
}
