//go:build linux || darwin

// Package imagerelease implements host-only HTTPS image approval and compatibility.
// Approval is a preparation gate, not proof of confinement or permission to launch.
package imagerelease

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

const Origin = "https://releases.intrusive.ai"
const ResponseLimit = 64 << 10

var (
	ErrRecord        = errors.New("invalid image release record")
	ErrCompatibility = errors.New("image release is incompatible with the installed Operator, contract, platform or runtime profile")
	ErrUnapproved    = errors.New("image is not approved by the release service")
	ErrLookup        = errors.New("release lookup failed; startup requires a valid cached or HTTPS release record")
	ErrCache         = errors.New("cannot safely read or persist the release cache")
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var numeric = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var identifier = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
var digits = regexp.MustCompile(`^[0-9]+$`)

type Record struct {
	APIVersion             string `json:"api_version"`
	ImageDigest            string `json:"image_digest"`
	MinimumOperatorVersion string `json:"minimum_operator_version"`
	ContractPackageVersion string `json:"contract_package_version"`
	ContractPackageDigest  string `json:"contract_package_digest"`
	RuntimeProfile         string `json:"runtime_profile"`
	Platform               string `json:"platform"`
}

// Requirements are trusted installed host facts, not campaign-selected claims.
// RuntimeProfile identifies installed policy; actual confinement probes are separate.
type Requirements struct {
	OperatorVersion string
	Contract        contracts.PackageIdentity
	HostPlatform    string
	RuntimeProfile  string
}

func version(s string) (core []string, prerelease string, ok bool) {
	if len(s) == 0 || len(s) > 128 {
		return nil, "", false
	}
	base, build, hasBuild := strings.Cut(s, "+")
	if hasBuild {
		for _, id := range strings.Split(build, ".") {
			if !identifier.MatchString(id) {
				return nil, "", false
			}
		}
	}
	base, pre, hasPre := strings.Cut(base, "-")
	if hasPre {
		for _, id := range strings.Split(pre, ".") {
			if !identifier.MatchString(id) || (digits.MatchString(id) && len(id) > 1 && id[0] == '0') {
				return nil, "", false
			}
		}
	}
	core = strings.Split(base, ".")
	if len(core) != 3 {
		return nil, "", false
	}
	for _, n := range core {
		if !numeric.MatchString(n) {
			return nil, "", false
		}
	}
	return core, pre, true
}

func stable(s string) bool { _, pre, ok := version(s); return ok && pre == "" }

// Stable SemVer precedence ignores build metadata and compares unbounded numeric
// components by length/digits, avoiding lexicographic version or integer overflow.
func atLeast(actual, minimum string) bool {
	a, ap, ok := version(actual)
	if !ok || ap != "" {
		return false
	}
	m, mp, ok := version(minimum)
	if !ok || mp != "" {
		return false
	}
	for i := range a {
		if len(a[i]) != len(m[i]) {
			return len(a[i]) > len(m[i])
		}
		if a[i] != m[i] {
			return a[i] > m[i]
		}
	}
	return true
}

func strict(raw []byte, dst any, limit int) error {
	canonical, err := contracts.Canonicalize(raw, limit)
	if err != nil {
		return ErrRecord
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return ErrRecord
	}
	roundTrip, err := json.Marshal(dst)
	if err != nil {
		return ErrRecord
	}
	roundTrip, err = contracts.Canonicalize(roundTrip, limit)
	if err != nil || !bytes.Equal(canonical, roundTrip) {
		return ErrRecord
	}
	return nil
}

func Parse(raw []byte) (Record, error) {
	var r Record
	if strict(raw, &r, ResponseLimit) != nil {
		return Record{}, ErrRecord
	}
	if err := r.validate(); err != nil {
		return Record{}, err
	}
	return r, nil
}

func (r Record) validate() error {
	_, _, validContract := version(r.ContractPackageVersion)
	if r.APIVersion != "intrusive.ai/engine-release/v1alpha1" || !digestPattern.MatchString(r.ImageDigest) ||
		!digestPattern.MatchString(r.ContractPackageDigest) || !stable(r.MinimumOperatorVersion) || !validContract ||
		r.RuntimeProfile != "operator-container/v1" || (r.Platform != "linux/amd64" && r.Platform != "linux/arm64") {
		return ErrRecord
	}
	return nil
}

func (h Requirements) validate() error {
	_, err := dockercontrol.ImagePlatform(h.HostPlatform)
	_, _, contractOK := version(h.Contract.Version)
	if err != nil || !stable(h.OperatorVersion) || !contractOK || !digestPattern.MatchString(h.Contract.Digest) || h.RuntimeProfile != "operator-container/v1" {
		return ErrCompatibility
	}
	return nil
}

func (r Record) Compatible(imageID string, h Requirements) error {
	if err := r.validate(); err != nil {
		return err
	}
	if h.validate() != nil {
		return ErrCompatibility
	}
	platform, _ := dockercontrol.ImagePlatform(h.HostPlatform)
	if r.ImageDigest != imageID || !atLeast(h.OperatorVersion, r.MinimumOperatorVersion) || r.ContractPackageVersion != h.Contract.Version ||
		r.ContractPackageDigest != h.Contract.Digest || r.RuntimeProfile != h.RuntimeProfile || r.Platform != platform {
		return ErrCompatibility
	}
	return nil
}
