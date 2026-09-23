package contracts

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const PackageManifestLimit = 8 << 20
const PackageFileLimit = 16 << 20
const PackageContentLimit = 128 << 20

// PackageIdentity is a caller-approved version plus canonical manifest digest.
// Matching it verifies content identity, not publisher authenticity or conformance.
type PackageIdentity struct {
	Version string `json:"package_version"`
	Digest  string `json:"package_digest"`
}

// PackageIdentity returns a copy of the pin only when this protocol was loaded
// through LoadVerifiedProtocol. Ordinary LoadProtocol is the development loader.
func (p *Protocol) PackageIdentity() (PackageIdentity, bool) {
	if p.packageIdentity == nil {
		return PackageIdentity{}, false
	}
	return *p.packageIdentity, true
}

// ValidatePackageManifest checks the installed manifest schema and inventory
// semantics. It cannot establish byte integrity without the payload resources.
func (p *Protocol) ValidatePackageManifest(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(ContractPackageSchema, raw, PackageManifestLimit)
	if err != nil {
		return nil, err
	}
	m := v.(map[string]any)
	files := m["files"].([]any)
	if !inventoryPaths(files) {
		return nil, ErrProtocol
	}
	entries := map[string]map[string]any{}
	var total int64
	for _, v := range files {
		f := v.(map[string]any)
		name := f["path"].(string)
		// Authentication material and the manifest itself cannot inventory themselves.
		first := strings.SplitN(name, "/", 2)[0]
		if folded(first) == "package.json" || folded(first) == "signatures" {
			return nil, ErrProtocol
		}
		entries[name] = f
		total += number(f["size_bytes"])
	}
	if total > PackageContentLimit || entries["catalog.json"] == nil || entries["operations.json"] == nil {
		return nil, ErrProtocol
	}
	previous := ""
	profiles := map[string]bool{}
	for _, v := range m["semantic_profiles"].([]any) {
		profile := v.(map[string]any)
		id := profile["profile_id"].(string)
		entry := entries[profile["path"].(string)]
		if id <= previous || entry == nil || number(entry["size_bytes"]) == 0 || entry["digest"] != profile["digest"] {
			return nil, ErrProtocol
		}
		profiles[id] = true
		previous = id
	}
	for _, id := range []string{"jcs-v1", "manifest-paths-v1", "harness-loop-v1"} {
		if !profiles[id] {
			return nil, ErrProtocol
		}
	}
	return m, nil
}

// VerifyPackage checks an explicit frozen payload set against a trusted expected
// identity. It never reads a filesystem, extracts archives, or executes payloads.
// Callers must bound reads and reject links/special files before supplying bytes.
func (p *Protocol) VerifyPackage(manifest []byte, files map[string][]byte, expected PackageIdentity) error {
	m, err := p.ValidatePackageManifest(manifest)
	if err != nil {
		return err
	}
	digest, err := CanonicalDigest(manifest, PackageManifestLimit)
	if err != nil {
		return err
	}
	if m["package_version"] != expected.Version || digest != expected.Digest {
		return ErrProtocol
	}
	if len(files) != len(m["files"].([]any)) {
		return ErrProtocol
	}
	for _, v := range m["files"].([]any) {
		entry := v.(map[string]any)
		raw, exists := files[entry["path"].(string)]
		if !exists || int64(len(raw)) != number(entry["size_bytes"]) || RawDigest(raw) != entry["digest"] {
			return ErrProtocol
		}
	}
	for key, name := range map[string]string{"catalog": "catalog.json", "operations": "operations.json"} {
		digest, err := CanonicalDigest(files[name], OrdinaryLimit)
		if err != nil {
			return err
		}
		if digest != m[key].(map[string]any)["object_digest"] {
			return ErrProtocol
		}
	}
	// Ensure the offline mapping refers only to named, inventoried schema files.
	v, err := Decode(files["catalog.json"], OrdinaryLimit)
	if err != nil {
		return err
	}
	catalog, ok := v.(map[string]any)
	if !ok || len(catalog) == 0 {
		return ErrCatalog
	}
	seen := map[string]bool{}
	for _, value := range catalog {
		name, ok := value.(string)
		if !ok || strings.ContainsAny(name, "/\\") || !strings.HasSuffix(name, ".schema.json") || seen[name] {
			return ErrCatalog
		}
		if _, exists := files[name]; !exists {
			return ErrCatalog
		}
		seen[name] = true
	}
	return nil
}

// BuildPackageManifest creates canonical metadata for explicitly supplied frozen
// payloads. It does not choose files, claim release readiness or publish a package.
func (p *Protocol) BuildPackageManifest(version string, files map[string][]byte, profiles map[string]string) ([]byte, PackageIdentity, error) {
	fail := func(err error) ([]byte, PackageIdentity, error) { return nil, PackageIdentity{}, err }
	if len(files) < 2 || len(files) > 4096 || len(profiles) > 64 {
		return fail(ErrLimit)
	}
	if len(version) > 128 {
		return fail(ErrLimit)
	}
	for id, name := range profiles {
		if len(id) > 128 || !normalizedPath(name) {
			return fail(ErrProtocol)
		}
	}
	names := make([]string, 0, len(files))
	total := 0
	for name, raw := range files {
		if !normalizedPath(name) {
			return fail(ErrProtocol)
		}
		if len(raw) > PackageFileLimit || len(raw) > PackageContentLimit-total {
			return fail(ErrLimit)
		}
		total += len(raw)
		names = append(names, name)
	}
	sort.Strings(names)
	inventory := []any{}
	for _, name := range names {
		inventory = append(inventory, map[string]any{"path": name, "size_bytes": json.Number(strconv.Itoa(len(files[name]))), "digest": RawDigest(files[name])})
	}
	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	semantics := []any{}
	for _, id := range ids {
		name := profiles[id]
		raw, ok := files[name]
		if !ok {
			return fail(ErrProtocol)
		}
		semantics = append(semantics, map[string]any{"profile_id": id, "path": name, "digest": RawDigest(raw)})
	}
	catalog, err := CanonicalDigest(files["catalog.json"], OrdinaryLimit)
	if err != nil {
		return fail(err)
	}
	operations, err := CanonicalDigest(files["operations.json"], OrdinaryLimit)
	if err != nil {
		return fail(err)
	}
	m := map[string]any{"api_version": "operator.dev/contract-package/v1alpha1", "package_version": version, "files": inventory, "semantic_profiles": semantics, "catalog": map[string]any{"path": "catalog.json", "object_digest": catalog}, "operations": map[string]any{"path": "operations.json", "object_digest": operations}}
	// Marshal only construction metadata, then validate before canonicalization.
	raw, err := json.Marshal(m)
	if err != nil {
		return fail(ErrJSON)
	}
	if _, err := p.ValidatePackageManifest(raw); err != nil {
		return fail(err)
	}
	raw, err = Canonicalize(raw, PackageManifestLimit)
	if err != nil {
		return fail(err)
	}
	identity := PackageIdentity{Version: version, Digest: RawDigest(raw)}
	if err := p.VerifyPackage(raw, files, identity); err != nil {
		return fail(err)
	}
	return raw, identity, nil
}

// LoadVerifiedProtocol copies bounded payload buffers before checking them, then
// compiles schemas/registry only from that verified snapshot. It executes no code.
func (p *Protocol) LoadVerifiedProtocol(manifest []byte, files map[string][]byte, expected PackageIdentity) (*Protocol, error) {
	if len(files) > 4096 {
		return nil, ErrLimit
	}
	frozen := resourceFS{}
	total := 0
	for name, raw := range files {
		if len(raw) > PackageFileLimit || len(raw) > PackageContentLimit-total {
			return nil, ErrLimit
		}
		total += len(raw)
		frozen[name] = bytes.Clone(raw)
	}
	if err := p.VerifyPackage(manifest, frozen, expected); err != nil {
		return nil, err
	}
	loaded, err := LoadProtocol(frozen)
	if err != nil {
		return nil, err
	}
	loaded.packageIdentity = &PackageIdentity{Version: expected.Version, Digest: expected.Digest}
	return loaded, nil
}

// resourceFS is an internal read-only file view of the frozen payload map.
// Only regular files are exposed; directory discovery is intentionally absent.
type resourceFS map[string][]byte

func (r resourceFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	raw, ok := r[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &resourceFile{Reader: bytes.NewReader(raw), name: path.Base(name), size: int64(len(raw))}, nil
}

type resourceFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *resourceFile) Close() error               { return nil }
func (f *resourceFile) Stat() (fs.FileInfo, error) { return resourceInfo{f.name, f.size}, nil }

type resourceInfo struct {
	name string
	size int64
}

func (f resourceInfo) Name() string       { return f.name }
func (f resourceInfo) Size() int64        { return f.size }
func (f resourceInfo) Mode() fs.FileMode  { return 0444 }
func (f resourceInfo) ModTime() time.Time { return time.Time{} }
func (f resourceInfo) IsDir() bool        { return false }
func (f resourceInfo) Sys() any           { return nil }
