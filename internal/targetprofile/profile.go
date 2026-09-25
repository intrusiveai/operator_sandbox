//go:build linux || darwin

// Package targetprofile resolves administrator-owned concrete target policy.
package targetprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/attemptadapter"
	"github.com/intrusive-ai/operator-sandbox/internal/capabilities"
	"github.com/intrusive-ai/operator-sandbox/internal/hostconfig"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

const Version = "operator.dev/target-profile/v1alpha1"
const Limit = 64 << 10

var ErrProfile = errors.New("invalid administrator target profile")
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Settings struct {
	APIVersion      string                `json:"api_version"`
	ID              string                `json:"id"`
	TargetID        string                `json:"target_id"`
	Adapter         string                `json:"adapter"`
	AllowTargetStop bool                  `json:"allow_target_stop"`
	Scopes          attemptadapter.Scopes `json:"scopes"`
	Feedback        struct {
		Ceiling         string   `json:"ceiling"`
		Kinds           []string `json:"allowed_kinds"`
		MaxAttemptBytes int64    `json:"max_attempt_bytes"`
	} `json:"feedback"`
	OperationTimeoutMS int64 `json:"operation_timeout_ms"`
}
type Profile struct {
	settings Settings
	raw      []byte
	digest   string
}

func Parse(raw []byte) (*Profile, error) {
	var s Settings
	if interceptor.DecodeTypedBody(raw, &s, Limit) != nil || s.APIVersion != Version || !identifier.MatchString(s.ID) || !identifier.MatchString(s.TargetID) || s.Adapter != "interceptor/v1" || s.OperationTimeoutMS < 1 || s.OperationTimeoutMS > 30000 || s.Feedback.MaxAttemptBytes < 0 || s.Feedback.MaxAttemptBytes > 1<<30 || len(s.Scopes.OperationIDs) == 0 || len(s.Scopes.OperationIDs) > 64 || len(s.Scopes.Routes) > 64 || len(s.Scopes.CallerPrincipalIDs) > 64 {
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
	canonical, err := contracts.Canonicalize(raw, Limit)
	if err != nil {
		return nil, ErrProfile
	}
	return &Profile{s, canonical, contracts.RawDigest(canonical)}, nil
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
