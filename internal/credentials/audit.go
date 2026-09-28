//go:build linux || darwin

package credentials

import (
	"context"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

var ErrAudit = errors.New("credential resolution audit unavailable")

// AuditEvent is an allowlist, not a serialization of Resolution or a backend
// error. Even backend version strings are untrusted and retained only as hashes.
type AuditEvent struct {
	CredentialID  string `json:"credential_id,omitempty"`
	ProfileID     string `json:"profile_id,omitempty"`
	Backend       string `json:"backend,omitempty"`
	Outcome       string `json:"outcome"`
	Code          string `json:"code"`
	CacheHit      bool   `json:"cache_hit"`
	RecordedAt    string `json:"recorded_at"`
	ResolvedAt    string `json:"resolved_at,omitempty"`
	VersionDigest string `json:"version_digest,omitempty"`
}

type AuditedResolver struct {
	resolver *Resolver
	record   func(AuditEvent) error
}

// WithAudit binds a campaign-local synchronous sink before model execution.
// Failure to durably record a resolution prevents disclosure of its value to
// the provider and invalidates its cache entry. There is no unaudited fallback.
func (r *Resolver) WithAudit(record func(AuditEvent) error) *AuditedResolver {
	return &AuditedResolver{r, record}
}
func (r *AuditedResolver) Resolve(ctx context.Context, id string) (Resolution, error) {
	if r == nil || r.resolver == nil || r.record == nil {
		return Resolution{}, ErrAudit
	}
	e := AuditEvent{Outcome: "failed", Code: "resolution_failed", RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	// refs/profiles are immutable after construction. Never copy an unknown
	// caller-provided identifier into the audit trail.
	if ref, ok := r.resolver.refs[id]; ok {
		e.CredentialID, e.ProfileID = ref.CredentialID, ref.StoreProfileID
		e.Backend = r.resolver.profiles[ref.StoreProfileID].BackendKind
	}
	resolved, err := r.resolver.Resolve(ctx, id)
	if err == nil {
		e.Outcome, e.Code, e.CacheHit = "resolved", "ok", resolved.cached
		e.ResolvedAt = resolved.ResolvedAt.UTC().Format(time.RFC3339Nano)
		if resolved.BackendVersion != "" {
			e.VersionDigest = contracts.RawDigest([]byte(resolved.BackendVersion))
		}
	} else if ctx.Err() != nil {
		e.Code = "canceled"
	}
	if r.record(e) != nil {
		r.resolver.Invalidate(id)
		return Resolution{}, ErrAudit
	}
	if err != nil {
		return Resolution{}, errors.New("credential resolution failed")
	}
	return resolved, nil
}
