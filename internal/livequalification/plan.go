//go:build linux || darwin

// Package livequalification runs explicit, bounded, read-only service probes.
// Configuration and remote response bytes never enter its evidence records.
package livequalification

import (
	"errors"
	"path/filepath"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

const Version = "operator.dev/live-qualification/v1alpha1"

var ErrPlan = errors.New("invalid qualification plan or private configuration")
var opaqueID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

type Plan struct {
	APIVersion string `json:"api_version"`
	CaseID     string `json:"case_id"`
	Kind       string `json:"kind"`
	// DeclaredAuthMode is administrator attribution, not proof of SDK selection.
	DeclaredAuthMode    string `json:"declared_auth_mode"`
	CredentialsFile     string `json:"credentials_file,omitempty"`
	CredentialID        string `json:"credential_id,omitempty"`
	FailureCredentialID string `json:"failure_credential_id,omitempty"`
	ModelProfileFile    string `json:"model_profile_file,omitempty"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	RotationWaitSeconds int    `json:"rotation_wait_seconds,omitempty"`
	MaximumModelCalls   int    `json:"maximum_model_calls,omitempty"`
	MaximumOutputTokens int64  `json:"maximum_output_tokens,omitempty"`
}

type Prepared struct {
	plan     Plan
	config   credentials.Config
	profile  *modelprovider.Profile
	backend  string
	cacheTTL int64
}

func Load(path string) (*Prepared, error) {
	raw, err := hostconfig.ReadPrivate(path, 64<<10)
	var plan Plan
	if err != nil || interceptor.DecodeTypedBody(raw, &plan, 64<<10) != nil {
		return nil, ErrPlan
	}
	return Prepare(plan)
}

// Prepare performs local validation only; constructors that resolve identities
// are deliberately deferred until an explicit live run.
func Prepare(p Plan) (*Prepared, error) {
	if p.APIVersion != Version || !opaqueID.MatchString(p.CaseID) || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 600 {
		return nil, ErrPlan
	}
	for _, path := range []string{p.CredentialsFile, p.ModelProfileFile} {
		if path != "" && (!filepath.IsAbs(path) || filepath.Clean(path) != path) {
			return nil, ErrPlan
		}
	}
	q := &Prepared{plan: p}
	var err error
	if p.CredentialsFile != "" {
		q.config, err = credentials.Load(p.CredentialsFile)
		if err != nil {
			return nil, ErrPlan
		}
	}
	if p.Kind == "secret-store" {
		if p.ModelProfileFile != "" || p.MaximumModelCalls != 0 || p.MaximumOutputTokens != 0 || p.RotationWaitSeconds < 0 || p.RotationWaitSeconds > 300 || p.RotationWaitSeconds >= p.TimeoutSeconds {
			return nil, ErrPlan
		}
		ref, ok := q.reference(p.CredentialID)
		if !ok {
			return nil, ErrPlan
		}
		q.backend, q.cacheTTL = ref.Locator.BackendKind, ref.CacheTTLSeconds
		if p.FailureCredentialID != "" {
			failure, ok := q.reference(p.FailureCredentialID)
			if !ok || p.FailureCredentialID == p.CredentialID || failure.StoreProfileID != ref.StoreProfileID {
				return nil, ErrPlan
			}
		}
		modes := map[string][]string{"aws-secrets-manager": {"web-identity", "container-role", "instance-role", "named-profile", "aws-environment"}, "azure-key-vault": {"workload-identity", "managed-identity", "azure-cli", "azure-client-secret"}, "gcp-secret-manager": {"external-account", "metadata-identity", "google-adc"}, "hashicorp-vault": {"token-sink", "proxy"}}
		if !contains(modes[q.backend], p.DeclaredAuthMode) {
			return nil, ErrPlan
		}
		for _, store := range q.config.Profiles {
			if store.ID == ref.StoreProfileID && (q.backend == "azure-key-vault" || q.backend == "gcp-secret-manager") {
				local := store.Authentication != "" && store.Authentication != "workload-identity"
				declaredLocal := contains([]string{"google-adc", "azure-cli", "azure-client-secret"}, p.DeclaredAuthMode)
				if local != declaredLocal || (local && store.Authentication != p.DeclaredAuthMode) {
					return nil, ErrPlan
				}
			}
		}
		if q.backend == "aws-secrets-manager" {
			for _, store := range q.config.Profiles {
				if store.ID == ref.StoreProfileID && ((store.AWSProfile != "") != (p.DeclaredAuthMode == "named-profile") || (store.Authentication == "aws-environment") != (p.DeclaredAuthMode == "aws-environment")) {
					return nil, ErrPlan
				}
			}
		}
		if q.backend == "hashicorp-vault" {
			for _, store := range q.config.Profiles {
				if store.ID == ref.StoreProfileID && store.VaultProxy != (p.DeclaredAuthMode == "proxy") {
					return nil, ErrPlan
				}
			}
		}
	} else if p.Kind == "provider" {
		if p.CredentialID != "" || p.FailureCredentialID != "" || p.RotationWaitSeconds != 0 || p.MaximumModelCalls != 3 || p.MaximumOutputTokens < 1 {
			return nil, ErrPlan
		}
		q.profile, err = modelprovider.Load(p.ModelProfileFile)
		if err != nil {
			return nil, ErrPlan
		}
		s := q.profile.Settings()
		if p.MaximumOutputTokens > s.MaximumCompletionTokens {
			return nil, ErrPlan
		}
		if s.Authentication == "aws-profile" {
			if p.CredentialsFile != "" || p.DeclaredAuthMode != "named-profile" {
				return nil, ErrPlan
			}
		} else if contains([]string{"google-adc", "azure-cli", "azure-client-secret", "aws-environment", "api-key-env", "none"}, s.Authentication) {
			if p.CredentialsFile != "" || p.DeclaredAuthMode != s.Authentication {
				return nil, ErrPlan
			}
		} else if s.UsesStoredCredential() {
			if _, ok := q.reference(s.CredentialID); !ok || (p.DeclaredAuthMode != s.Authentication && !(s.Authentication == "secret-store" && p.DeclaredAuthMode == "api-key")) {
				return nil, ErrPlan
			}
		} else {
			modes := map[string][]string{"bedrock-converse": {"web-identity", "container-role", "instance-role"}, "azure-openai": {"workload-identity", "managed-identity"}, "vertex-gemini": {"external-account", "metadata-identity"}}
			if p.CredentialsFile != "" || !contains(modes[s.Provider], p.DeclaredAuthMode) {
				return nil, ErrPlan
			}
		}
	} else {
		return nil, ErrPlan
	}
	if p.Kind == "provider" {
		if _, err := newConversation(q); err != nil {
			return nil, ErrPlan
		}
	}
	return q, nil
}
func (q *Prepared) reference(id string) (credentials.HostCredentialRef, bool) {
	for _, ref := range q.config.Credentials {
		if ref.CredentialID == id {
			return ref, true
		}
	}
	return credentials.HostCredentialRef{}, false
}
func contains(values []string, v string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}
