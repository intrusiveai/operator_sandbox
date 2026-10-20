//go:build linux || darwin

package reporting

import (
	"encoding/json"
	"regexp"
	"time"
)

// CredentialResolution contains only audit facts, never locators or values.
// Event is the sequence in the exported, digest-bound source event stream.
type CredentialResolution struct {
	Event        int64    `json:"event"`
	RecordedAt   string   `json:"recorded_at"`
	CredentialID string   `json:"credential_id,omitempty"`
	ProfileID    string   `json:"profile_id,omitempty"`
	Backend      string   `json:"backend,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	Code         string   `json:"code,omitempty"`
	CacheHit     *bool    `json:"cache_hit,omitempty"`
	ResolvedAt   string   `json:"resolved_at,omitempty"`
	Omitted      []string `json:"omitted_fields,omitempty"`
}

var auditID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func projectCredential(sequence int64, at string, metadata map[string]json.RawMessage) (CredentialResolution, map[string]json.RawMessage) {
	c := CredentialResolution{Event: sequence, RecordedAt: at}
	keep := map[string]json.RawMessage{}
	for _, f := range []struct {
		name    string
		dst     *string
		allowed []string
	}{
		{"credential_id", &c.CredentialID, nil}, {"profile_id", &c.ProfileID, nil},
		{"backend", &c.Backend, []string{"aws-secrets-manager", "azure-key-vault", "gcp-secret-manager", "hashicorp-vault"}},
		{"outcome", &c.Outcome, []string{"resolved", "failed"}},
		{"code", &c.Code, []string{"ok", "resolution_failed", "canceled"}},
	} {
		var value string
		valid := json.Unmarshal(metadata[f.name], &value) == nil && auditID.MatchString(value)
		if f.allowed != nil {
			found := false
			for _, option := range f.allowed {
				found = found || value == option
			}
			valid = valid && found
		}
		if valid {
			*f.dst = value
			keep[f.name], _ = json.Marshal(value)
		} else {
			c.Omitted = append(c.Omitted, f.name+":unavailable_or_invalid")
		}
	}
	var cache bool
	if string(metadata["cache_hit"]) == "true" || string(metadata["cache_hit"]) == "false" {
		_ = json.Unmarshal(metadata["cache_hit"], &cache)
		c.CacheHit = &cache
		keep["cache_hit"], _ = json.Marshal(cache)
	} else {
		c.Omitted = append(c.Omitted, "cache_hit:unavailable_or_invalid")
	}
	for _, field := range []string{"recorded_at", "resolved_at"} {
		var value string
		if json.Unmarshal(metadata[field], &value) == nil && len(value) <= 64 {
			if _, e := time.Parse(time.RFC3339Nano, value); e == nil {
				keep[field], _ = json.Marshal(value)
				if field == "resolved_at" {
					c.ResolvedAt = value
				}
				continue
			}
		}
		if field == "recorded_at" {
			keep[field], _ = json.Marshal(at)
		}
	}
	if len(c.Omitted) > 0 {
		keep["omitted_fields"], _ = json.Marshal(c.Omitted)
	}
	return c, keep
}
