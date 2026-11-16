//go:build linux || darwin

// Package targetprofile resolves administrator-owned concrete target policy.
package targetprofile

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/httptarget"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

const Version = "operator.dev/target-profile/v1alpha1"
const Limit = 64 << 10

// MaximumOperationTimeoutMS bounds the complete attempt, never each native step.
const MaximumOperationTimeoutMS int64 = hostconfig.MaximumOperationTimeoutMS

var ErrProfile = diagnostictext.New("invalid administrator target profile")
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Settings struct {
	Interceptor     *interceptor.Connection `json:"interceptor,omitempty"`
	HTTP            json.RawMessage         `json:"http,omitempty"`
	APIVersion      string                  `json:"api_version"`
	ID              string                  `json:"id"`
	TargetID        string                  `json:"target_id"`
	Adapter         string                  `json:"adapter"`
	AllowTargetStop bool                    `json:"allow_target_stop"`
	Scopes          attemptadapter.Scopes   `json:"scopes"`
	Feedback        struct {
		Ceiling         string   `json:"ceiling"`
		Kinds           []string `json:"allowed_kinds"`
		MaxAttemptBytes int64    `json:"max_attempt_bytes"`
	} `json:"feedback"`
	OperationTimeoutMS int64 `json:"operation_timeout_ms"`
}
type Profile struct {
	settings   Settings
	raw        []byte
	digest     string
	httpTarget *httptarget.Mapping
}

func Parse(raw []byte) (*Profile, error) {
	var s Settings
	if interceptor.DecodeTypedBody(raw, &s, Limit) != nil || s.APIVersion != Version || !identifier.MatchString(s.ID) || !identifier.MatchString(s.TargetID) || !slices.Contains([]string{"interceptor/v1", httptarget.Adapter}, s.Adapter) || s.OperationTimeoutMS < 1 || s.OperationTimeoutMS > MaximumOperationTimeoutMS || s.Feedback.MaxAttemptBytes < 0 || s.Feedback.MaxAttemptBytes > 1<<30 || len(s.Scopes.OperationIDs) == 0 || len(s.Scopes.OperationIDs) > 64 || len(s.Scopes.Routes) > 64 || len(s.Scopes.CallerPrincipalIDs) > 64 {
		return nil, ErrProfile
	}
	if !slices.Contains([]string{"black-box", "diagnostic", "oracle-assisted"}, s.Feedback.Ceiling) || s.Feedback.Kinds == nil || len(s.Feedback.Kinds) > 4 {
		return nil, ErrProfile
	}
	kinds := []string{"target_output", "operation_error", "injection_delivery", "oracle_outcome"}
	maxKinds := map[string]int{"black-box": 1, "diagnostic": 3, "oracle-assisted": 4}[s.Feedback.Ceiling]
	seen := map[string]bool{}
	for _, kind := range s.Feedback.Kinds {
		if seen[kind] || !slices.Contains(kinds[:maxKinds], kind) {
			return nil, ErrProfile
		}
		seen[kind] = true
	}
	for _, ids := range [][]string{s.Scopes.OperationIDs, s.Scopes.CallerPrincipalIDs} {
		seen = map[string]bool{}
		for _, id := range ids {
			if !identifier.MatchString(id) || seen[id] {
				return nil, ErrProfile
			}
			seen[id] = true
		}
	}
	for _, r := range s.Scopes.Routes {
		if len(r.Scopes) > 16 || len(r.Placements) > 16 || len(r.Pointers) > 64 || len(r.MergeFields) > 64 {
			return nil, ErrProfile
		}
	}
	if err := s.Interceptor.Validate(); err != nil {
		return nil, err
	}
	if s.Adapter != "interceptor/v1" && s.Interceptor != nil {
		return nil, ErrProfile
	}
	var mapping *httptarget.Mapping
	if s.Adapter == httptarget.Adapter {
		var err error
		mapping, err = httptarget.Parse(s.HTTP)
		if err != nil || s.AllowTargetStop || len(s.Scopes.Routes) > 0 || len(s.Scopes.CallerPrincipalIDs) > 0 || s.Scopes.AllowRetainedInjections || s.Feedback.Ceiling == "oracle-assisted" {
			return nil, ErrProfile
		}
		for _, kind := range s.Feedback.Kinds {
			if kind != "target_output" && kind != "operation_error" {
				return nil, ErrProfile
			}
		}
		for _, id := range s.Scopes.OperationIDs {
			if _, ok := mapping.Operation(id); !ok {
				return nil, ErrProfile
			}
		}
	} else if len(s.HTTP) > 0 {
		return nil, ErrProfile
	}
	canonical, err := contracts.Canonicalize(raw, Limit)
	if err != nil {
		return nil, ErrProfile
	}
	return &Profile{settings: s, raw: canonical, digest: contracts.RawDigest(canonical), httpTarget: mapping}, nil
}
func Load(name string) (*Profile, error) {
	raw, err := hostconfig.ReadPrivate(name, Limit)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}
func (p *Profile) JSON() []byte       { return bytes.Clone(p.raw) }
func (p *Profile) Digest() string     { return p.digest }
func (p *Profile) Settings() Settings { var s Settings; _ = json.Unmarshal(p.raw, &s); return s }
func (p *Profile) Resolve(live *capabilities.Live) (*attemptadapter.Policy, error) {
	if p == nil || live == nil || live.Export().TargetID() != p.settings.TargetID {
		return nil, ErrProfile
	}
	return attemptadapter.Resolve(live.Export(), p.settings.Scopes, p.settings.Feedback.Kinds, p.settings.Feedback.Ceiling)
}

// HTTP returns the frozen private mapping. It never comes from submitted content.
func (p *Profile) HTTP() *httptarget.Mapping { return p.httpTarget }

// RetainedJSON excludes direct-target mapping details. Interceptor connection
// settings remain in private retention so recovery can contact the same endpoint.
func (p *Profile) RetainedJSON() []byte {
	if p.httpTarget == nil {
		return p.JSON()
	}
	s := p.Settings()
	s.HTTP = nil
	raw, _ := json.Marshal(s)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	out["http_mapping_digest"] = p.httpTarget.Digest()
	out["private_profile_digest"] = p.Digest()
	raw, _ = json.Marshal(out)
	return raw
}

// LoadWithLimit applies installation policy as well as the profile's numeric bounds.
func LoadWithLimit(name string, maximumMS int64) (*Profile, error) {
	p, err := Load(name)
	if err != nil {
		return nil, err
	}
	if err = p.CheckTimeoutLimit(maximumMS); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *Profile) CheckTimeoutLimit(maximumMS int64) error {
	if maximumMS == 0 {
		maximumMS = hostconfig.DefaultOperationTimeoutMS
	}
	if p == nil || maximumMS < 1 || maximumMS > MaximumOperationTimeoutMS || p.settings.OperationTimeoutMS > maximumMS {
		return diagnostictext.New("target operation_timeout_ms exceeds the installation target.max_operation_timeout_ms ceiling")
	}
	return nil
}
