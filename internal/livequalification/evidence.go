//go:build linux || darwin

package livequalification

import (
	"runtime"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// Record is a fixed allowlist. No raw plan, locator, prompt, response, secret,
// backend version, native request ID or remote exception is serializable here.
type Record struct {
	APIVersion       string                  `json:"api_version"`
	CaseID           string                  `json:"case_id"`
	Kind             string                  `json:"kind"`
	Platform         string                  `json:"platform"`
	HostVersion      string                  `json:"host_version"`
	SourceCommit     string                  `json:"source_commit"`
	ContractVersion  string                  `json:"contract_version"`
	ContractDigest   string                  `json:"contract_digest"`
	Provider         string                  `json:"provider,omitempty"`
	Codec            string                  `json:"codec,omitempty"`
	ProfileDigest    string                  `json:"profile_digest,omitempty"`
	Backend          string                  `json:"backend,omitempty"`
	DeclaredAuthMode string                  `json:"declared_auth_mode"`
	Check            string                  `json:"check"`
	Status           string                  `json:"status"`
	Code             string                  `json:"code"`
	RecordedAt       string                  `json:"recorded_at"`
	Usage            *contracts.ModelMetrics `json:"usage,omitempty"`
}

type Identity struct{ Version, SourceCommit, ContractVersion, ContractDigest string }
type Sink func(Record) error

func (q *Prepared) record(id Identity, check, status, code string) Record {
	r := Record{APIVersion: Version, CaseID: q.plan.CaseID, Kind: q.plan.Kind, Platform: runtime.GOOS + "/" + runtime.GOARCH, HostVersion: id.Version, SourceCommit: id.SourceCommit, ContractVersion: id.ContractVersion, ContractDigest: id.ContractDigest, Backend: q.backend, DeclaredAuthMode: q.plan.DeclaredAuthMode, Check: check, Status: status, Code: code, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if q.profile != nil {
		s := q.profile.Settings()
		r.Provider = s.Provider
		r.Codec = s.Codec
		r.ProfileDigest = q.profile.Digest()
	}
	return r
}
func (q *Prepared) Check(id Identity) Record {
	return q.record(id, "preflight", "passed", "local_configuration_only")
}
