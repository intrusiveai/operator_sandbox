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
		return Config{}, errors.New("invalid credential configuration")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, errors.New("invalid credential configuration")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Config{}, errors.New("credential configuration must contain exactly one YAML document")
	}
	if err := Validate(config); err != nil {
		return Config{}, errors.New("invalid credential configuration")
	}
	return config, nil
}

func Validate(config Config) error {
	if len(config.Profiles) > 32 || len(config.Credentials) > 128 {
		return errors.New("credential configuration exceeds entry limits")
	}
	profiles := map[string]SecretStoreProfile{}
	for _, profile := range config.Profiles {
		if profile.APIVersion != StoreProfileVersion || profile.Kind != "SecretStoreProfile" || !stableID.MatchString(profile.ID) {
			return fmt.Errorf("secret-store profile %q has invalid identity or schema", profile.ID)
		}
		switch profile.BackendKind {
		case "aws-secrets-manager", "azure-key-vault", "gcp-secret-manager", "hashicorp-vault":
		default:
			return fmt.Errorf("profile %q has unsupported backend %q", profile.ID, profile.BackendKind)
		}
		if _, ok := profiles[profile.ID]; ok {
			return fmt.Errorf("secret-store profile %q is duplicated", profile.ID)
		}
		if len(profile.AllowedLocatorPrefixes) == 0 {
			return fmt.Errorf("profile %q requires allowed_locator_prefixes", profile.ID)
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
			return fmt.Errorf("profile %q has an out-of-bounds timeout, value, or cache limit", profile.ID)
		}
		if profile.AWSProfile != "" && (profile.BackendKind != "aws-secrets-manager" || !ValidAWSProfile(profile.AWSProfile)) {
			return errors.New("invalid AWS named profile selection")
		}
		if profile.BackendKind == "aws-secrets-manager" && profile.Region == "" {
			return fmt.Errorf("AWS profile %q requires region", profile.ID)
		}
		if profile.BackendKind == "azure-key-vault" && !validHTTPS(profile.VaultURL) {
			return fmt.Errorf("Azure profile %q requires an HTTPS vault_url", profile.ID)
		}
		if profile.BackendKind == "hashicorp-vault" && (!validHTTPS(profile.VaultURL) || (profile.VaultProxy && profile.VaultTokenFile != "" || !profile.VaultProxy && profile.VaultTokenFile == "")) {
			return fmt.Errorf("Vault profile %q requires an HTTPS vault_url and exactly one of vault_proxy or token file", profile.ID)
		}
		if profile.VaultTokenFile != "" && (!filepath.IsAbs(profile.VaultTokenFile) || filepath.Clean(profile.VaultTokenFile) != profile.VaultTokenFile) {
			return errors.New("Vault token sink must be an absolute clean path")
		}
		if len(profile.AllowedLocatorPrefixes) > 128 {
			return errors.New("too many locator prefixes")
		}
		for _, prefix := range profile.AllowedLocatorPrefixes {
			if prefix == "" || len(prefix) > 2048 || strings.ContainsAny(prefix, "\x00\r\n") {
				return errors.New("invalid locator prefix")
			}
		}
		profiles[profile.ID] = profile
	}
	seen := map[string]bool{}
	for _, ref := range config.Credentials {
		if ref.APIVersion != CredentialRefVersion || ref.Kind != "HostCredentialRef" || !stableID.MatchString(ref.CredentialID) {
			return fmt.Errorf("credential %q has invalid identity or schema", ref.CredentialID)
		}
		if seen[ref.CredentialID] {
			return fmt.Errorf("credential %q is duplicated", ref.CredentialID)
		}
		seen[ref.CredentialID] = true
		profile, ok := profiles[ref.StoreProfileID]
		if !ok {
			return fmt.Errorf("credential %q refers to unknown profile", ref.CredentialID)
		}
		if ref.Locator.BackendKind != profile.BackendKind {
			return fmt.Errorf("credential %q backend does not match its profile", ref.CredentialID)
		}
		if ref.Locator.BackendKind == "aws-secrets-manager" && ref.Locator.Region != profile.Region || ref.Locator.BackendKind == "azure-key-vault" && strings.TrimSuffix(ref.Locator.VaultURL, "/") != strings.TrimSuffix(profile.VaultURL, "/") {
			return errors.New("credential locator differs from its profile")
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
			return fmt.Errorf("credential %q locator is outside the profile allowlist", ref.CredentialID)
		}
		if ref.Value.Format != "utf8" && ref.Value.Format != "json-string-field" {
			return fmt.Errorf("credential %q has unsupported value format", ref.CredentialID)
		}
		if ref.Value.Format == "json-string-field" && !stableID.MatchString(ref.Value.Field) {
			return fmt.Errorf("credential %q requires a simple JSON field", ref.CredentialID)
		}
		if ref.Value.Format == "utf8" && ref.Value.Field != "" {
			return fmt.Errorf("credential %q utf8 selection may not name a field", ref.CredentialID)
		}
		if ref.Locator.BackendKind == "hashicorp-vault" && ref.Value.Format != "json-string-field" {
			return fmt.Errorf("credential %q must select an explicit Vault KV string field", ref.CredentialID)
		}
		if ref.CacheTTLSeconds < 0 || ref.CacheTTLSeconds > profile.MaxCacheTTLSeconds {
			return fmt.Errorf("credential %q cache TTL exceeds its profile", ref.CredentialID)
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
		return Resolution{}, errors.New("credential resolver is closed")
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
		return Resolution{}, fmt.Errorf("unknown credential %q", credentialID)
	}
	profile := r.profiles[ref.StoreProfileID]
	backend := r.backends[profile.ID]
	if backend == nil {
		factory := r.factories[profile.BackendKind]
		if factory == nil {
			r.mu.Unlock()
			return Resolution{}, fmt.Errorf("backend %q is unavailable", profile.BackendKind)
		}
		var err error
		setupCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutSeconds)*time.Second)
		backend, err = factory(setupCtx, profile)
		cancel()
		if err != nil {
			r.mu.Unlock()
			return Resolution{}, fmt.Errorf("initialize credential backend %q: %w", profile.ID, redact(err))
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
		return Resolution{}, fmt.Errorf("resolve credential %q: %w", credentialID, redact(err))
	}
	value, err := selectValue(raw, ref.Value, profile.MaxValueBytes)
	for index := range raw {
		raw[index] = 0
	}
	if err != nil {
		r.Invalidate(credentialID)
		return Resolution{}, fmt.Errorf("resolve credential %q: %w", credentialID, err)
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
		return "", errors.New("secret value exceeds configured limit")
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", errors.New("secret value must be bounded UTF-8 without NUL")
	}
	if selection.Format == "utf8" {
		if len(raw) == 0 || bytes.ContainsAny(raw, "\r\n") {
			return "", errors.New("secret value is empty")
		}
		return string(raw), nil
	}
	if _, err := contracts.Decode(raw, int(maximum)); err != nil {
		return "", errors.New("secret value must be an unambiguous JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return "", errors.New("secret value must be exactly one JSON object")
	}
	field, ok := object[selection.Field]
	if !ok {
		return "", errors.New("selected JSON field is absent")
	}
	var value string
	if json.Unmarshal(field, &value) != nil || value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("selected JSON field must be a non-empty string")
	}
	return value, nil
}

func canonicalLocator(locator Locator) (string, error) {
	switch locator.BackendKind {
	case "aws-secrets-manager":
		if locator.Region == "" || locator.SecretID == "" || (locator.VersionID != "" && locator.VersionStage != "") || locator.VaultURL != "" || locator.SecretName != "" || locator.Version != "" || locator.Project != "" || locator.Mount != "" || locator.Path != "" {
			return "", errors.New("AWS locator requires region and secret_id and at most one version selector")
		}
		return "aws://" + locator.Region + "/" + locator.SecretID, nil
	case "azure-key-vault":
		if !validHTTPS(locator.VaultURL) || locator.SecretName == "" || locator.SecretID != "" || locator.Region != "" || locator.VersionID != "" || locator.VersionStage != "" || locator.Project != "" || locator.Mount != "" || locator.Path != "" {
			return "", errors.New("Azure locator requires HTTPS vault_url and secret_name")
		}
		return strings.TrimSuffix(locator.VaultURL, "/") + "/secrets/" + locator.SecretName, nil
	case "gcp-secret-manager":
		if locator.Project == "" || locator.SecretName == "" || locator.Version == "" || locator.SecretID != "" || locator.Region != "" || locator.VersionID != "" || locator.VersionStage != "" || locator.VaultURL != "" || locator.Mount != "" || locator.Path != "" {
			return "", errors.New("GCP locator requires project, secret_name, and version")
		}
		return "gcp://projects/" + locator.Project + "/secrets/" + locator.SecretName, nil
	case "hashicorp-vault":
		if locator.Mount == "" || locator.Path == "" || strings.Contains(locator.Path, "..") || locator.SecretID != "" || locator.Region != "" || locator.VersionID != "" || locator.VersionStage != "" || locator.VaultURL != "" || locator.SecretName != "" || locator.Project != "" {
			return "", errors.New("Vault locator requires mount and safe path")
		}
		return "vault://" + locator.Mount + "/" + strings.TrimPrefix(locator.Path, "/"), nil
	default:
		return "", errors.New("unsupported credential backend")
	}
}

type redactedError struct{ message string }

func (e redactedError) Error() string { return e.message }
func redact(err error) error {
	if err == nil {
		return nil
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
		return errors.New("credential backend close failed")
	}
	return nil
}
