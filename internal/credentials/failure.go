//go:build linux || darwin

package credentials

import (
	"fmt"
	"slices"
)

// Failure contains selected configuration identifiers and the failed step, never
// a secret locator, value, or backend error string.
type Failure struct {
	CredentialID string `json:"credential_id,omitempty"`
	ProfileID    string `json:"profile_id,omitempty"`
	Backend      string `json:"backend,omitempty"`
	Step         string `json:"step"`
}

func (f Failure) Valid() bool {
	return stableID.MatchString(f.CredentialID) && stableID.MatchString(f.ProfileID) && slices.Contains([]string{"aws-secrets-manager", "azure-key-vault", "gcp-secret-manager", "hashicorp-vault", "onepassword"}, f.Backend) && slices.Contains([]string{"backend-initialization", "secret-read", "value-selection", "audit-retention"}, f.Step)
}
func (f Failure) Error() string {
	return fmt.Sprintf("Operator credential %s via profile %s (%s): %s failed; backend cause not retained", f.CredentialID, f.ProfileID, f.Backend, f.Step)
}
