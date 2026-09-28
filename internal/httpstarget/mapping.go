// Package httpstarget implements administrator-owned declarative HTTPS operations.
package httpstarget

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

const Adapter = "https/v1"
const Version = "operator.dev/https-mapping/v1alpha1"
const Limit = 64 << 10

var ErrMapping = errors.New("invalid administrator HTTPS mapping")
var ErrInput = errors.New("HTTPS input does not match the configured operation")
var ErrResponse = errors.New("HTTPS response does not match the configured selection")
var id = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var header = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

type Authentication struct {
	Mode         string `json:"mode"`
	CredentialID string `json:"credential_id,omitempty"`
	Header       string `json:"header,omitempty"`
}
type Input struct {
	Format string         `json:"format"`
	Field  []string       `json:"field,omitempty"`
	Fixed  map[string]any `json:"fixed,omitempty"`
}
type Response struct {
	Format string   `json:"format"`
	Field  []string `json:"field,omitempty"`
}
type Operation struct {
	ID                   string   `json:"id"`
	Method               string   `json:"method"`
	Path                 string   `json:"path"`
	Input                Input    `json:"input"`
	Response             Response `json:"response"`
	MaximumInputBytes    int64    `json:"maximum_input_bytes"`
	MaximumRequestBytes  int64    `json:"maximum_request_bytes"`
	MaximumResponseBytes int64    `json:"maximum_response_bytes"`
}
type Settings struct {
	APIVersion          string         `json:"api_version"`
	Origin              string         `json:"origin"`
	AllowedPrivateCIDRs []string       `json:"allowed_private_cidrs"`
	CACertificatesPEM   string         `json:"ca_certificates_pem,omitempty"`
	Authentication      Authentication `json:"authentication"`
	Operations          []Operation    `json:"operations"`
}
type Mapping struct {
	settings Settings
	raw      []byte
	digest   string
}

func Parse(raw []byte) (*Mapping, error) {
	var s Settings
	if interceptor.DecodeTypedBody(raw, &s, Limit) != nil || s.APIVersion != Version || len(s.Operations) == 0 || len(s.Operations) > 64 || len(s.AllowedPrivateCIDRs) > 32 {
		return nil, ErrMapping
	}
	u, e := url.Parse(s.Origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || len(s.Origin) > 2048 || strings.ContainsAny(u.Host, "%\\\r\n") {
		return nil, ErrMapping
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, ErrMapping
		}
	}
	for _, raw := range s.AllowedPrivateCIDRs {
		prefix, e := netip.ParsePrefix(raw)
		if e != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return nil, ErrMapping
		}
		// A configured private range may never widen the public or special-use policy.
		last := prefix.Addr()
		if !privateRange(prefix) || !(last.IsPrivate() || last.IsLoopback()) {
			return nil, ErrMapping
		}
	}
	auth := s.Authentication
	switch auth.Mode {
	case "none":
		if auth.CredentialID != "" || auth.Header != "" {
			return nil, ErrMapping
		}
	case "bearer":
		if !id.MatchString(auth.CredentialID) || auth.Header != "" {
			return nil, ErrMapping
		}
	case "api-key":
		if !id.MatchString(auth.CredentialID) || !header.MatchString(auth.Header) {
			return nil, ErrMapping
		}
		h := strings.ToLower(auth.Header)
		if slices.Contains([]string{"authorization", "proxy-authorization", "host", "connection", "content-length", "content-type", "transfer-encoding", "te", "trailer", "upgrade", "cookie", "set-cookie", "accept", "accept-encoding", "expect", "forwarded"}, h) || strings.HasPrefix(h, "x-forwarded-") {
			return nil, ErrMapping
		}
	default:
		return nil, ErrMapping
	}
	seen := map[string]bool{}
	for _, o := range s.Operations {
		if !id.MatchString(o.ID) || seen[o.ID] || !slices.Contains([]string{"POST", "PUT", "PATCH"}, o.Method) || !strings.HasPrefix(o.Path, "/") || path.Clean(o.Path) != o.Path || strings.HasPrefix(o.Path, "//") || strings.ContainsAny(o.Path, "%?#\\\r\n\x00") || len(o.Path) > 2048 {
			return nil, ErrMapping
		}
		seen[o.ID] = true
		if o.MaximumInputBytes < 1 || o.MaximumInputBytes > 1<<20 || o.MaximumRequestBytes < o.MaximumInputBytes || o.MaximumRequestBytes > 2<<20 || o.MaximumResponseBytes < 1 || o.MaximumResponseBytes > 8<<20 {
			return nil, ErrMapping
		}
		if !fieldPath(o.Input.Field) || !fieldPath(o.Response.Field) {
			return nil, ErrMapping
		}
		switch o.Input.Format {
		case "text":
			if len(o.Input.Field) > 0 || len(o.Input.Fixed) > 0 {
				return nil, ErrMapping
			}
		case "json":
			if len(o.Input.Field) == 0 && len(o.Input.Fixed) > 0 {
				return nil, ErrMapping
			}
			if _, e := construct(o, []byte(""), "text/plain"); e != nil {
				return nil, ErrMapping
			}
		default:
			return nil, ErrMapping
		}
		if !slices.Contains([]string{"text", "json"}, o.Response.Format) || o.Response.Format == "text" && len(o.Response.Field) > 0 {
			return nil, ErrMapping
		}
	}
	canonical, e := contracts.Canonicalize(raw, Limit)
	if e != nil {
		return nil, ErrMapping
	}
	return &Mapping{s, canonical, contracts.RawDigest(canonical)}, nil
}
func fieldPath(fields []string) bool {
	if len(fields) > 16 {
		return false
	}
	for _, f := range fields {
		if f == "" || len(f) > 256 || !utf8.ValidString(f) || strings.ContainsAny(f, "\x00\r\n") {
			return false
		}
	}
	return true
}
func privateRange(p netip.Prefix) bool {
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "fc00::/7", "::1/128"} {
		r := netip.MustParsePrefix(s)
		if p.Bits() >= r.Bits() && r.Contains(p.Addr()) {
			return true
		}
	}
	return false
}
func (m *Mapping) Settings() Settings { var s Settings; _ = json.Unmarshal(m.raw, &s); return s }
func (m *Mapping) JSON() []byte       { return bytes.Clone(m.raw) }
func (m *Mapping) Digest() string     { return m.digest }
func (m *Mapping) Operation(id string) (Operation, bool) {
	for _, o := range m.Settings().Operations {
		if o.ID == id {
			return o, true
		}
	}
	return Operation{}, false
}
func (m *Mapping) AllowsAddress(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
		return false
	}
	// Special-use ranges include cloud platform endpoints, mapped/translated space,
	// documentation, benchmarking and shared-address space; explicit CIDRs do not override.
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "168.63.129.16/32", "fd00:ec2::254/128", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	if a.IsPrivate() || a.IsLoopback() {
		for _, s := range m.settings.AllowedPrivateCIDRs {
			if netip.MustParsePrefix(s).Contains(a) {
				return true
			}
		}
		return false
	}
	return a.IsGlobalUnicast() && (a.Is4() || netip.MustParsePrefix("2000::/3").Contains(a))
}
func (m *Mapping) Request(id string, payload []byte, media string) (Operation, []byte, error) {
	o, ok := m.Operation(id)
	if !ok || int64(len(payload)) > o.MaximumInputBytes || !utf8.Valid(payload) {
		return o, nil, ErrInput
	}
	raw, e := construct(o, payload, media)
	if e != nil || int64(len(raw)) > o.MaximumRequestBytes {
		return o, nil, ErrInput
	}
	return o, raw, nil
}
func construct(o Operation, payload []byte, media string) ([]byte, error) {
	var value any
	switch media {
	case "text/plain":
		value = string(payload)
	case "application/json":
		var e error
		value, e = contracts.Decode(payload, 1<<20)
		if e != nil {
			return nil, ErrInput
		}
	default:
		return nil, ErrInput
	}
	if o.Input.Format == "text" {
		if media != "text/plain" {
			return nil, ErrInput
		}
		return bytes.Clone(payload), nil
	}
	if len(o.Input.Field) == 0 {
		return json.Marshal(value)
	}
	fixed, _ := json.Marshal(o.Input.Fixed)
	var root map[string]any
	_ = json.Unmarshal(fixed, &root)
	if root == nil {
		root = map[string]any{}
	}
	at := root
	for i, key := range o.Input.Field {
		if i == len(o.Input.Field)-1 {
			if _, exists := at[key]; exists {
				return nil, ErrInput
			}
			at[key] = value
			break
		}
		next, exists := at[key]
		if !exists {
			next = map[string]any{}
			at[key] = next
		}
		var ok bool
		at, ok = next.(map[string]any)
		if !ok {
			return nil, ErrInput
		}
	}
	return json.Marshal(root)
}
func SelectResponse(o Operation, raw []byte) ([]byte, string, error) {
	if int64(len(raw)) > o.MaximumResponseBytes || !utf8.Valid(raw) {
		return nil, "", ErrResponse
	}
	if o.Response.Format == "text" {
		return bytes.Clone(raw), "text/plain", nil
	}
	value, e := contracts.Decode(raw, int(o.MaximumResponseBytes))
	if e != nil {
		return nil, "", ErrResponse
	}
	for _, field := range o.Response.Field {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, "", ErrResponse
		}
		value, ok = obj[field]
		if !ok {
			return nil, "", ErrResponse
		}
	}
	if s, ok := value.(string); ok {
		return []byte(s), "text/plain", nil
	}
	out, e := json.Marshal(value)
	return out, "application/json", e
}
