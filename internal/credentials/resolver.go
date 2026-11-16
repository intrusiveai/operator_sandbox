//go:build linux || darwin

package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"go.yaml.in/yaml/v3"
)

var stableID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type Backend interface {
	Read(context.Context, SecretStoreProfile, Locator) ([]byte, string, error)
}

type BackendFactory func(context.Context, SecretStoreProfile) (Backend, error)

type cacheEntry struct {
	result  Resolution
	expires time.Time
}

type Resolver struct {
	config    Config
	factories map[string]BackendFactory
	profiles  map[string]SecretStoreProfile
	refs      map[string]HostCredentialRef
	mu        sync.Mutex
	backends  map[string]Backend
	cache     map[string]cacheEntry
	now       func() time.Time
	closed    bool
}

func Load(path string) (Config, error) {
	data, err := hostconfig.ReadPrivate(path, 1<<20)
	if err != nil {
		return Config{}, fmt.Errorf("read credential configuration: %w", err)
	}
	var document yaml.Node
	if yaml.Unmarshal(data, &document) != nil || !plainYAML(&document) {
		return Config{}, diagnostictext.New("invalid credential configuration")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, diagnostictext.New("invalid credential configuration")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Config{}, diagnostictext.New("credential configuration must contain exactly one YAML document")
	}
	if err := Validate(config); err != nil {
		return Config{}, diagnostictext.New("invalid credential configuration")
	}
	return config, nil
}

func Validate(config Config) error {
	if len(config.Profiles) > 32 || len(config.Credentials) > 128 {
		return diagnostictext.New("credential configuration exceeds entry limits")
	}
	profiles := map[string]SecretStoreProfile{}
	for index, profile := range config.Profiles {
		if (profile.APIVersion != StoreProfileVersion && profile.APIVersion != "interceptor.dev/secret-store-profile/v1alpha1") || profile.Kind != "SecretStoreProfile" || !stableID.MatchString(profile.ID) {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d].id: invalid identity or schema", index))
		}
		switch profile.BackendKind {
		case "aws-secrets-manager", "azure-key-vault", "gcp-secret-manager", "hashicorp-vault", "onepassword":
		default:
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d].backend_kind: unsupported backend", index))
		}
		if _, ok := profiles[profile.ID]; ok {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d].id: duplicate profile identity", index))
		}
		if len(profile.AllowedLocatorPrefixes) == 0 {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d].allowed_locator_prefixes: at least one allowed locator prefix is required", index))
		}
		if profile.TimeoutSeconds == 0 {
			profile.TimeoutSeconds = 10
		}
		if profile.MaxValueBytes == 0 {
			profile.MaxValueBytes = 64 << 10
		}
		if profile.MaxCacheTTLSeconds == 0 {
			profile.MaxCacheTTLSeconds = 300
		}
		if profile.TimeoutSeconds < 1 || profile.TimeoutSeconds > 60 || profile.MaxValueBytes < 1 || profile.MaxValueBytes > 1<<20 || profile.MaxCacheTTLSeconds < 0 || profile.MaxCacheTTLSeconds > 3600 {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d]: timeout, value, or cache limit is out of bounds", index))
		}
		if err := validateOnePasswordProfile(profile); err != nil {
			return fmt.Errorf("profile %q: %w", profile.ID, err)
		}
		if profile.AWSProfile != "" && (profile.BackendKind != "aws-secrets-manager" || !ValidAWSProfile(profile.AWSProfile)) {
			return diagnostictext.New("invalid AWS named profile selection")
		}
		if profile.BackendKind == "aws-secrets-manager" && ((profile.Authentication == "aws-profile" && profile.AWSProfile == "") || (profile.AWSProfile != "" && profile.Authentication != "" && profile.Authentication != "aws-profile")) {
			return diagnostictext.New("conflicting AWS authentication selection")
		}
		if !ValidStoreAuthentication(profile.BackendKind, profile.Authentication) {
			return diagnostictext.New("invalid secret-store authentication selection")
		}
		if profile.BackendKind == "aws-secrets-manager" && profile.Region == "" {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d].region: AWS requires a region", index))
		}
		if profile.BackendKind == "azure-key-vault" && !validHTTPS(profile.VaultURL) {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d].vault_url: Azure requires an HTTPS vault_url", index))
		}
		if profile.BackendKind == "hashicorp-vault" && (!validHTTPS(profile.VaultURL) || (profile.VaultProxy && profile.VaultTokenFile != "" || !profile.VaultProxy && profile.VaultTokenFile == "")) {
			return diagnostictext.New(fmt.Sprintf("Credential configuration profiles[%d]: Vault requires an HTTPS vault_url and exactly one of vault_proxy or token file", index))
		}
		if profile.VaultTokenFile != "" && (!filepath.IsAbs(profile.VaultTokenFile) || filepath.Clean(profile.VaultTokenFile) != profile.VaultTokenFile) {
			return diagnostictext.New("Vault token sink must be an absolute clean path")
		}
		if len(profile.AllowedLocatorPrefixes) > 128 {
			return diagnostictext.New("too many locator prefixes")
		}
		for _, prefix := range profile.AllowedLocatorPrefixes {
			if prefix == "" || len(prefix) > 2048 || strings.ContainsAny(prefix, "\x00\r\n") {
				return diagnostictext.New("invalid locator prefix")
			}
		}
		profiles[profile.ID] = profile
	}
	seen := map[string]bool{}
	for index, ref := range config.Credentials {
		if (ref.APIVersion != CredentialRefVersion && ref.APIVersion != "interceptor.dev/host-credential-ref/v1alpha1") || ref.Kind != "HostCredentialRef" || !stableID.MatchString(ref.CredentialID) {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].credential_id: invalid identity or schema", index))
		}
		if seen[ref.CredentialID] {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].credential_id: duplicate credential identity", index))
		}
		seen[ref.CredentialID] = true
		profile, ok := profiles[ref.StoreProfileID]
		if !ok {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].store_profile_id: unknown profile", index))
		}
		if ref.Locator.BackendKind != profile.BackendKind {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].locator.backend_kind: backend does not match its profile", index))
		}
		if ref.Locator.BackendKind == "aws-secrets-manager" && ref.Locator.Region != profile.Region || ref.Locator.BackendKind == "azure-key-vault" && strings.TrimSuffix(ref.Locator.VaultURL, "/") != strings.TrimSuffix(profile.VaultURL, "/") {
			return diagnostictext.New("credential locator differs from its profile")
		}
		canonical, err := canonicalLocator(ref.Locator)
		if err != nil {
			return fmt.Errorf("credential %q: %w", ref.CredentialID, err)
		}
		allowed := false
		for _, prefix := range profile.AllowedLocatorPrefixes {
			if prefix != "" && strings.HasPrefix(canonical, prefix) {
				allowed = true
			}
		}
		if !allowed {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].locator: locator is outside the profile allowlist", index))
		}
		if ref.Value.Format != "utf8" && ref.Value.Format != "json-string-field" {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].value_format: unsupported value format", index))
		}
		if ref.Value.Format == "json-string-field" && !stableID.MatchString(ref.Value.Field) {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].json_field: a simple JSON field is required", index))
		}
		if ref.Value.Format == "utf8" && ref.Value.Field != "" {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].json_field: utf8 selection may not name a field", index))
		}
		if ref.Locator.BackendKind == "hashicorp-vault" && ref.Value.Format != "json-string-field" {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].json_field: select an explicit Vault KV string field", index))
		}
		if ref.CacheTTLSeconds < 0 || ref.CacheTTLSeconds > profile.MaxCacheTTLSeconds {
			return diagnostictext.New(fmt.Sprintf("Credential configuration credentials[%d].cache_ttl_seconds: cache TTL exceeds its profile", index))
		}
	}
	return nil
}

func New(config Config, factories map[string]BackendFactory) (*Resolver, error) {
	if err := Validate(config); err != nil {
		return nil, err
	}
	if factories == nil {
		factories = DefaultFactories()
	}
	// Freeze host settings and factories before use.
	raw, _ := json.Marshal(config)
	config = Config{}
	_ = json.Unmarshal(raw, &config)
	factoryCopy := map[string]BackendFactory{}
	for k, v := range factories {
		factoryCopy[k] = v
	}
	r := &Resolver{config: config, factories: factoryCopy, profiles: map[string]SecretStoreProfile{}, refs: map[string]HostCredentialRef{}, backends: map[string]Backend{}, cache: map[string]cacheEntry{}, now: time.Now}
	for _, profile := range config.Profiles {
		if profile.TimeoutSeconds == 0 {
			profile.TimeoutSeconds = 10
		}
		if profile.MaxValueBytes == 0 {
			profile.MaxValueBytes = 64 << 10
		}
		if profile.MaxCacheTTLSeconds == 0 {
			profile.MaxCacheTTLSeconds = 300
		}
		r.profiles[profile.ID] = profile
	}
	for _, ref := range config.Credentials {
		r.refs[ref.CredentialID] = ref
	}
	return r, nil
}

func (r *Resolver) Resolve(ctx context.Context, credentialID string) (Resolution, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Resolution{}, diagnostictext.New("credential resolver is closed")
	}
	if cached, ok := r.cache[credentialID]; ok && r.now().Before(cached.expires) {
		result := cached.result
		result.cached = true
		r.mu.Unlock()
		return result, nil
	}
	ref, ok := r.refs[credentialID]
	if !ok {
		r.mu.Unlock()
		return Resolution{}, diagnostictext.New("requested credential_id is not configured")
	}
	profile := r.profiles[ref.StoreProfileID]
	backend := r.backends[profile.ID]
	if backend == nil {
		factory := r.factories[profile.BackendKind]
		if factory == nil {
			r.mu.Unlock()
			return Resolution{}, diagnostictext.New("configured credential backend is unavailable")
		}
		var err error
		setupCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutSeconds)*time.Second)
		backend, err = factory(setupCtx, profile)
		if err == nil {
			err = setupCtx.Err()
		}
		cancel()
		if err != nil {
			r.mu.Unlock()
			return Resolution{}, failed(credentialID, profile.ID, profile.BackendKind, "backend-initialization", err)
		}
		r.backends[profile.ID] = backend
	}
	r.mu.Unlock()
	requestContext, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutSeconds)*time.Second)
	defer cancel()
	raw, version, err := backend.Read(requestContext, profile, ref.Locator)
	if err == nil {
		err = requestContext.Err()
	}
	if err != nil {
		for i := range raw {
			raw[i] = 0
		}
		r.Invalidate(credentialID)
		return Resolution{}, failed(credentialID, profile.ID, profile.BackendKind, "secret-read", err)
	}
	value, err := selectValue(raw, ref.Value, profile.MaxValueBytes)
	for index := range raw {
		raw[index] = 0
	}
	if err != nil {
		r.Invalidate(credentialID)
		return Resolution{}, failed(credentialID, profile.ID, profile.BackendKind, "value-selection", err)
	}
	result := Resolution{CredentialID: credentialID, ProfileID: profile.ID, BackendVersion: version, ResolvedAt: r.now().UTC(), Value: value}
	if ref.CacheTTLSeconds > 0 {
		r.mu.Lock()
		if !r.closed {
			r.cache[credentialID] = cacheEntry{result: result, expires: r.now().Add(time.Duration(ref.CacheTTLSeconds) * time.Second)}
		}
		r.mu.Unlock()
	}
	return result, nil
}

func (r *Resolver) Invalidate(id string) {
	r.mu.Lock()
	if item, ok := r.cache[id]; ok {
		item.result.Value = ""
		delete(r.cache, id)
	}
	r.mu.Unlock()
}

func selectValue(raw []byte, selection ValueSelection, maximum int64) (string, error) {
	if int64(len(raw)) > maximum {
		return "", diagnostictext.New("secret value exceeds configured limit")
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", diagnostictext.New("secret value must be bounded UTF-8 without NUL")
	}
	if selection.Format == "utf8" {
		if len(raw) == 0 || bytes.ContainsAny(raw, "\r\n") {
			return "", diagnostictext.New("secret value is empty")
		}
		return string(raw), nil
	}
	if _, err := contracts.Decode(raw, int(maximum)); err != nil {
		return "", diagnostictext.New("secret value must be an unambiguous JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return "", diagnostictext.New("secret value must be exactly one JSON object")
	}
	field, ok := object[selection.Field]
	if !ok {
		return "", diagnostictext.New("selected JSON field is absent")
	}
	var value string
	if json.Unmarshal(field, &value) != nil || value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", diagnostictext.New("selected JSON field must be a non-empty string")
	}
	return value, nil
}

func canonicalLocator(locator Locator) (string, error) {
	if locator.BackendKind == "onepassword" {
		return onePasswordLocator(locator)
	}
	if locator.SecretReference != "" {
		return "", diagnostictext.New("secret_reference requires the onepassword backend")
	}
	switch locator.BackendKind {
	case "aws-secrets-manager":
		if locator.Region == "" || locator.SecretID == "" || (locator.VersionID != "" && locator.VersionStage != "") || locator.VaultURL != "" || locator.SecretName != "" || locator.Version != "" || locator.Project != "" || locator.Mount != "" || locator.Path != "" {
			return "", diagnostictext.New("AWS locator requires region and secret_id and at most one version selector")
		}
		return "aws://" + locator.Region + "/" + locator.SecretID, nil
	case "azure-key-vault":
		if !validHTTPS(locator.VaultURL) || locator.SecretName == "" || locator.SecretID != "" || locator.Region != "" || locator.VersionID != "" || locator.VersionStage != "" || locator.Project != "" || locator.Mount != "" || locator.Path != "" {
			return "", diagnostictext.New("Azure locator requires HTTPS vault_url and secret_name")
		}
		return strings.TrimSuffix(locator.VaultURL, "/") + "/secrets/" + locator.SecretName, nil
	case "gcp-secret-manager":
		if locator.Project == "" || locator.SecretName == "" || locator.Version == "" || locator.SecretID != "" || locator.Region != "" || locator.VersionID != "" || locator.VersionStage != "" || locator.VaultURL != "" || locator.Mount != "" || locator.Path != "" {
			return "", diagnostictext.New("GCP locator requires project, secret_name, and version")
		}
		return "gcp://projects/" + locator.Project + "/secrets/" + locator.SecretName, nil
	case "hashicorp-vault":
		if locator.Mount == "" || locator.Path == "" || strings.Contains(locator.Path, "..") || locator.SecretID != "" || locator.Region != "" || locator.VersionID != "" || locator.VersionStage != "" || locator.VaultURL != "" || locator.SecretName != "" || locator.Project != "" {
			return "", diagnostictext.New("Vault locator requires mount and safe path")
		}
		return "vault://" + locator.Mount + "/" + strings.TrimPrefix(locator.Path, "/"), nil
	default:
		return "", diagnostictext.New("unsupported credential backend")
	}
}

type redactedError struct{ message string }

func (e redactedError) Error() string             { return e.message }
func (e redactedError) DiagnosticMessage() string { return e.Error() }
func redact(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var onePassword onePasswordError
	if errors.As(err, &onePassword) {
		return safeOnePasswordCode(string(onePassword))
	}
	return redactedError{message: "secret-store request failed"}
}

func validHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Opaque == "" && len(raw) <= 2048 && !strings.ContainsAny(raw, "\x00\r\n")
}

func plainYAML(n *yaml.Node) bool {
	if n.Anchor != "" || n.Alias != nil || n.Kind == yaml.AliasNode {
		return false
	}
	for _, child := range n.Content {
		if !plainYAML(child) {
			return false
		}
	}
	return true
}

// Close drops cached values and releases backend clients. Callers cancel and join
// active resolutions before closing their installation/campaign resolver.
func (r *Resolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	clear(r.cache)
	var failed bool
	for _, backend := range r.backends {
		if c, ok := backend.(interface{ Close() error }); ok && c.Close() != nil {
			failed = true
		}
	}
	clear(r.backends)
	if failed {
		return diagnostictext.New("credential backend close failed")
	}
	return nil
}
