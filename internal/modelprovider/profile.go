//go:build linux || darwin

// Package modelprovider implements host-selected native provider transports.
// The shared codec validator remains responsible for prompt/tool semantics.
package modelprovider

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

const Version = "operator.dev/model-provider-profile/v1alpha1"

var ErrProfile = errors.New("invalid host model provider profile")
var ErrRequest = errors.New("model request does not match the host provider profile")
var ErrProvider = errors.New("model provider call failed; outcome may be unknown")
var id = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Settings never crosses into guest inputs. The full request endpoint, cloud
// scope and credential reference are administrator-owned and frozen before launch.
type Settings struct {
	APIKeyEnvironment       string          `json:"api_key_env,omitempty"`
	AWSProfile              string          `json:"aws_profile,omitempty"`
	APIVersion              string          `json:"api_version"`
	ID                      string          `json:"id"`
	Provider                string          `json:"provider"`
	Codec                   string          `json:"codec_id"`
	Model                   string          `json:"model"`
	Endpoint                string          `json:"endpoint,omitempty"`
	CACertificatesPEM       string          `json:"ca_certificates_pem,omitempty"`
	Region                  string          `json:"region,omitempty"`
	APIVersionHeader        string          `json:"anthropic_version,omitempty"`
	Authentication          string          `json:"authentication"`
	CredentialID            string          `json:"credential_id,omitempty"`
	ContextWarningPercent   int64           `json:"context_warning_percent,omitempty"`
	MaximumPromptTokens     int64           `json:"maximum_prompt_tokens"`
	MaximumCompletionTokens int64           `json:"maximum_completion_tokens"`
	MaximumResponseBytes    int64           `json:"maximum_response_bytes"`
	CodecOptions            json.RawMessage `json:"codec_options"`
}

type Profile struct {
	settings Settings
	raw      []byte
	digest   string
}

func (p *Profile) Settings() Settings {
	s := p.settings
	s.CodecOptions = bytes.Clone(s.CodecOptions)
	return s
}
func (p *Profile) Digest() string { return p.digest }

// JSON is host-private configuration; callers must not journal or mount it.
func (p *Profile) JSON() []byte { return bytes.Clone(p.raw) }

var codecs = map[string][]string{
	"openai-chat":        {"openai-chat-text-tools-v1"},
	"openai-responses":   {"openai-responses-text-tools-v1"},
	"anthropic-messages": {"anthropic-messages-text-tools-v1"},
	"bedrock-converse":   {"bedrock-converse-text-tools-v1"},
	"gemini-api":         {"gemini-text-tools-v1"},
	"vertex-gemini":      {"gemini-text-tools-v1"},
	"azure-openai":       {"openai-chat-text-tools-v1", "openai-responses-text-tools-v1"},
	"litellm":            {"openai-chat-text-tools-v1", "openai-responses-text-tools-v1"},
}

func Load(name string) (*Profile, error) {
	raw, err := hostconfig.ReadPrivate(name, 64<<10)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}
func Parse(raw []byte) (*Profile, error) {
	var s Settings
	if interceptor.DecodeTypedBody(raw, &s, 64<<10) != nil {
		return nil, profileRule("/", "structure", "Use a JSON profile with the documented fields and types, unique keys, and at most 65536 bytes.")
	}
	if s.APIVersion != Version {
		return nil, profileRule("/api_version", "const", "Use "+Version+".")
	}
	if !id.MatchString(s.ID) {
		return nil, profileRule("/id", "pattern", "Use an identifier matching "+id.String()+".")
	}
	if len(s.Model) < 1 || len(s.Model) > 256 || strings.IndexFunc(s.Model, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
		return nil, profileRule("/model", "format", "Use 1 to 256 bytes without whitespace or control characters.")
	}
	supported := false
	for _, v := range codecs[s.Provider] {
		if v == s.Codec {
			supported = true
		}
	}
	if !supported {
		allowed := codecs[s.Provider]
		if allowed == nil {
			for provider := range codecs {
				allowed = append(allowed, provider)
			}
			sort.Strings(allowed)
			return nil, profileRule("/provider", "enum", "Select one of: "+strings.Join(allowed, ", ")+".")
		}
		return nil, profileRule("/codec_id", "provider_codec", "For this provider select: "+strings.Join(allowed, ", ")+".")
	}
	for _, bound := range []struct {
		field      string
		value, max int64
	}{{"maximum_prompt_tokens", s.MaximumPromptTokens, contracts.MaxSafeInteger / 2}, {"maximum_completion_tokens", s.MaximumCompletionTokens, contracts.MaxSafeInteger / 2}, {"maximum_response_bytes", s.MaximumResponseBytes, contracts.OrdinaryLimit}} {
		if bound.value < 1 || bound.value > bound.max {
			return nil, profileRule("/"+bound.field, "range", fmt.Sprintf("Use an integer from 1 through %d.", bound.max))
		}
	}
	// Resolve omission, but reject an explicitly invalid zero.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if _, present := fields["context_warning_percent"]; !present {
		s.ContextWarningPercent = 80
	}
	if s.ContextWarningPercent < 1 || s.ContextWarningPercent > 99 {
		return nil, profileRule("/context_warning_percent", "range", "Use an integer from 1 through 99.")
	}
	if s.CACertificatesPEM != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(s.CACertificatesPEM)) {
		return nil, profileRule("/ca_certificates_pem", "certificate", "Supply PEM containing a valid X.509 certificate, or omit this field.")
	}
	if s.Authentication == "api-key" {
		s.Authentication = "secret-store"
	}
	if s.AWSProfile != "" && (s.Authentication != "aws-profile" || s.Provider != "bedrock-converse" || !credentials.ValidAWSProfile(s.AWSProfile)) {
		return nil, profileRule("/aws_profile", "authentication", "A valid named AWS profile requires aws-profile authentication and bedrock-converse.")
	}
	if s.APIKeyEnvironment != "" && (s.Authentication != "api-key-env" || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(s.APIKeyEnvironment)) {
		return nil, profileRule("/api_key_env", "authentication", "Use api-key-env authentication and an environment variable name matching [A-Za-z_][A-Za-z0-9_]{0,127}.")
	}
	switch s.Authentication {
	case "none":
		if s.Provider != "litellm" || s.CredentialID != "" {
			return nil, profileRule("/authentication", "combination", "Unauthenticated access requires litellm and no credential_id.")
		}
	case "workload-token":
		if s.Provider != "anthropic-messages" || !id.MatchString(s.CredentialID) {
			return nil, profileRule("/authentication", "combination", "workload-token requires anthropic-messages and a valid credential_id.")
		}
	case "api-key-env":
		if s.Provider == "bedrock-converse" || s.CredentialID != "" || s.APIKeyEnvironment == "" {
			return nil, profileRule("/authentication", "combination", "api-key-env requires api_key_env, no credential_id, and a provider other than bedrock-converse.")
		}
	case "aws-environment":
		if s.Provider != "bedrock-converse" || s.CredentialID != "" {
			return nil, profileRule("/authentication", "combination", "aws-environment requires bedrock-converse and no credential_id.")
		}
	case "aws-profile":
		if s.Provider != "bedrock-converse" || s.CredentialID != "" || !credentials.ValidAWSProfile(s.AWSProfile) {
			return nil, profileRule("/authentication", "combination", "aws-profile requires bedrock-converse, a valid aws_profile, and no credential_id.")
		}
	case "secret-store", "api-key":
		if !id.MatchString(s.CredentialID) || s.Provider == "bedrock-converse" {
			return nil, profileRule("/credential_id", "combination", "Secret-store authentication requires a valid credential_id and a provider other than bedrock-converse.")
		}
	case "google-adc":
		if s.Provider != "vertex-gemini" || s.CredentialID != "" {
			return nil, profileRule("/authentication", "combination", "google-adc requires vertex-gemini and no credential_id.")
		}
	case "azure-cli", "azure-client-secret":
		if s.Provider != "azure-openai" || s.CredentialID != "" {
			return nil, profileRule("/authentication", "combination", "Azure identity authentication requires azure-openai and no credential_id.")
		}
	case "workload-identity":
		if s.CredentialID != "" || (s.Provider != "bedrock-converse" && s.Provider != "vertex-gemini" && s.Provider != "azure-openai") {
			return nil, profileRule("/authentication", "combination", "workload-identity requires bedrock-converse, vertex-gemini, or azure-openai and no credential_id.")
		}
	default:
		return nil, profileRule("/authentication", "unsupported", "Select a documented authentication mode supported by the chosen provider.")
	}
	if s.Provider == "bedrock-converse" {
		if s.Endpoint != "" || !regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`).MatchString(s.Region) {
			return nil, profileRule("/region", "combination", "bedrock-converse requires a region matching [a-z]{2}(-[a-z0-9]+)+-[0-9]+ and no endpoint override.")
		}
	} else {
		if s.Region != "" {
			return nil, profileRule("/region", "forbidden", "Omit region for providers other than bedrock-converse.")
		}
		u, err := url.Parse(s.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || len(s.Endpoint) > 2048 {
			return nil, profileRule("/endpoint", "format", "Use an HTTPS URL with a hostname, no user information, fragment, opaque or escaped path, and at most 2048 bytes.")
		}
		suffix := map[string]string{"openai-chat-text-tools-v1": "/chat/completions", "openai-responses-text-tools-v1": "/responses", "anthropic-messages-text-tools-v1": "/messages", "gemini-text-tools-v1": "/models/" + s.Model + ":generateContent"}[s.Codec]
		if suffix == "" || !strings.HasSuffix(u.Path, suffix) {
			return nil, profileRule("/endpoint", "codec_path", "Use the endpoint path required by the selected codec ("+strings.ReplaceAll(suffix, s.Model, "<model>")+").")
		}
		if s.Codec == "gemini-text-tools-v1" && strings.ContainsAny(s.Model, "/?#%") {
			return nil, profileRule("/model", "format", "Gemini model names must not contain /, ?, # or %.")
		}
		// The complete endpoint is selected by the administrator, never constructed
		// from a guest path. Only Azure's explicit API version query is supported.
		if u.RawQuery != "" {
			q, e := url.ParseQuery(u.RawQuery)
			if e != nil || s.Provider != "azure-openai" || len(q) != 1 || len(q["api-version"]) != 1 || q.Get("api-version") == "" {
				return nil, profileRule("/endpoint", "query", "Only Azure supports a query: exactly one nonempty api-version parameter.")
			}
		}
	}
	if s.Provider == "anthropic-messages" {
		if !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`).MatchString(s.APIVersionHeader) {
			return nil, profileRule("/anthropic_version", "format", "Use a YYYY-MM-DD API version date.")
		}
	} else if s.APIVersionHeader != "" {
		return nil, profileRule("/anthropic_version", "forbidden", "Omit anthropic_version for other providers.")
	}
	if _, err := privatePolicy(s, "sha256:"+strings.Repeat("0", 64), []byte("[]")); err != nil {
		return nil, err
	}
	fields["context_warning_percent"], _ = json.Marshal(s.ContextWarningPercent)
	resolved, err := json.Marshal(fields)
	if err != nil {
		return nil, profileRule("/", "encoding", "Profile could not be encoded.")
	}
	canonical, err := contracts.Canonicalize(resolved, 64<<10)
	if err != nil {
		return nil, profileRule("/", "encoding", "Profile could not be canonicalized.")
	}
	return &Profile{s, canonical, contracts.RawDigest(canonical)}, nil
}

// UsesStoredCredential identifies modes requiring the host secret resolver.
func (s Settings) UsesStoredCredential() bool {
	return s.Authentication == "secret-store" || s.Authentication == "api-key" || s.Authentication == "workload-token"
}

// ProfileError contains only schema/configuration rules, never supplied values.
type ProfileError struct{ Field, Rule, Correction string }

func (e *ProfileError) Error() string {
	return fmt.Sprintf("Model profile %s: %s (%s)", e.Field, e.Correction, e.Rule)
}
func (e *ProfileError) Unwrap() error                  { return ErrProfile }
func profileRule(field, rule, correction string) error { return &ProfileError{field, rule, correction} }
