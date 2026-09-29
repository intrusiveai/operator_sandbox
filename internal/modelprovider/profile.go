//go:build linux || darwin

// Package modelprovider implements host-selected native provider transports.
// The shared codec validator remains responsible for prompt/tool semantics.
package modelprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
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
	AWSProfile              string          `json:"aws_profile,omitempty"`
	APIVersion              string          `json:"api_version"`
	ID                      string          `json:"id"`
	Provider                string          `json:"provider"`
	Codec                   string          `json:"codec_id"`
	Model                   string          `json:"model"`
	Endpoint                string          `json:"endpoint,omitempty"`
	Region                  string          `json:"region,omitempty"`
	APIVersionHeader        string          `json:"anthropic_version,omitempty"`
	Authentication          string          `json:"authentication"`
	CredentialID            string          `json:"credential_id,omitempty"`
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
	if interceptor.DecodeTypedBody(raw, &s, 64<<10) != nil || s.APIVersion != Version || !id.MatchString(s.ID) || len(s.Model) < 1 || len(s.Model) > 256 || strings.IndexFunc(s.Model, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
		return nil, ErrProfile
	}
	supported := false
	for _, v := range codecs[s.Provider] {
		if v == s.Codec {
			supported = true
		}
	}
	if !supported || s.MaximumPromptTokens < 1 || s.MaximumCompletionTokens < 1 || s.MaximumPromptTokens > contracts.MaxSafeInteger/2 || s.MaximumCompletionTokens > contracts.MaxSafeInteger/2 || s.MaximumResponseBytes < 1 || s.MaximumResponseBytes > contracts.OrdinaryLimit {
		return nil, ErrProfile
	}
	if s.AWSProfile != "" && (s.Authentication != "aws-profile" || s.Provider != "bedrock-converse" || !credentials.ValidAWSProfile(s.AWSProfile)) {
		return nil, ErrProfile
	}
	switch s.Authentication {
	case "aws-profile":
		if s.Provider != "bedrock-converse" || s.CredentialID != "" || !credentials.ValidAWSProfile(s.AWSProfile) {
			return nil, ErrProfile
		}
	case "secret-store":
		if !id.MatchString(s.CredentialID) || s.Provider == "bedrock-converse" || s.Provider == "vertex-gemini" {
			return nil, ErrProfile
		}
	case "workload-identity":
		if s.CredentialID != "" || (s.Provider != "bedrock-converse" && s.Provider != "vertex-gemini" && s.Provider != "azure-openai") {
			return nil, ErrProfile
		}
	default:
		return nil, ErrProfile
	}
	if s.Provider == "bedrock-converse" {
		if s.Endpoint != "" || !regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`).MatchString(s.Region) {
			return nil, ErrProfile
		}
	} else {
		if s.Region != "" {
			return nil, ErrProfile
		}
		u, err := url.Parse(s.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || len(s.Endpoint) > 2048 {
			return nil, ErrProfile
		}
		suffix := map[string]string{"openai-chat-text-tools-v1": "/chat/completions", "openai-responses-text-tools-v1": "/responses", "anthropic-messages-text-tools-v1": "/messages", "gemini-text-tools-v1": "/models/" + s.Model + ":generateContent"}[s.Codec]
		if suffix == "" || !strings.HasSuffix(u.Path, suffix) {
			return nil, ErrProfile
		}
		if s.Codec == "gemini-text-tools-v1" && strings.ContainsAny(s.Model, "/?#%") {
			return nil, ErrProfile
		}
		// The complete endpoint is selected by the administrator, never constructed
		// from a guest path. Only Azure's explicit API version query is supported.
		if u.RawQuery != "" {
			q, e := url.ParseQuery(u.RawQuery)
			if e != nil || s.Provider != "azure-openai" || len(q) != 1 || len(q["api-version"]) != 1 || q.Get("api-version") == "" {
				return nil, ErrProfile
			}
		}
	}
	if s.Provider == "anthropic-messages" {
		if !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`).MatchString(s.APIVersionHeader) {
			return nil, ErrProfile
		}
	} else if s.APIVersionHeader != "" {
		return nil, ErrProfile
	}
	if _, err := publicPolicy(s, "sha256:"+strings.Repeat("0", 64), []byte("[]")); err != nil {
		return nil, ErrProfile
	}
	canonical, err := contracts.Canonicalize(raw, 64<<10)
	if err != nil {
		return nil, ErrProfile
	}
	return &Profile{s, canonical, contracts.RawDigest(canonical)}, nil
}
