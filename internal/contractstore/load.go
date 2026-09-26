//go:build linux || darwin

// Package contractstore loads an installed contract package without executing its
// payloads. The expected pin must come from trusted installation metadata.
package contractstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

var (
	ErrPath         = errors.New("contract package directory must be an absolute clean host path")
	ErrInstallation = errors.New("contract package filesystem must contain exactly the declared regular files and directories, owned by the service user or root and not writable by group or others")
	ErrManifest     = errors.New("invalid installed contract package manifest")
	ErrPin          = errors.New("installed contract package does not match the trusted expected version and digest")
	ErrIntegrity    = errors.New("installed contract payload verification or offline schema loading failed")
)

const ReportVersion = "operator.dev/contract-check/v1alpha1"

// Report describes byte verification and offline loading, not release approval.
type Report struct {
	APIVersion       string                    `json:"api_version"`
	Status           string                    `json:"status"`
	Directory        string                    `json:"directory"`
	Package          contracts.PackageIdentity `json:"package"`
	ManifestDigest   string                    `json:"manifest_digest"`
	CatalogDigest    string                    `json:"catalog_digest"`
	OperationsDigest string                    `json:"operations_digest"`
	FileCount        int                       `json:"file_count"`
	ContentBytes     int64                     `json:"content_bytes"`
}

// Loaded retains only frozen metadata and the compiled protocol, not open files.
type Loaded struct {
	protocol *contracts.Protocol
	manifest []byte
	report   Report
}

func (l *Loaded) Protocol() *contracts.Protocol { return l.protocol }
func (l *Loaded) Manifest() []byte              { return bytes.Clone(l.manifest) }
func (l *Loaded) Report() Report                { return l.report }

var bootstrap = sync.OnceValues(func() (*contracts.Protocol, error) {
	return contracts.LoadProtocol(schemas.Files)
})

type entry struct {
	children map[string]*entry
	size     int64
	digest   string
}

func inventory(m map[string]any, manifest []byte) *entry {
	root := &entry{children: map[string]*entry{"package.json": {size: int64(len(manifest)), digest: contracts.RawDigest(manifest)}}}
	for _, value := range m["files"].([]any) {
		f := value.(map[string]any)
		parts := strings.Split(f["path"].(string), "/")
		parent := root
		for _, name := range parts[:len(parts)-1] {
			if parent.children[name] == nil {
				parent.children[name] = &entry{children: map[string]*entry{}}
			}
			parent = parent.children[name]
		}
		// JSON Schema integer values can use decimal/exponent notation. The
		// shared validator has already bounded this to an exact safe integer.
		size, _ := f["size_bytes"].(json.Number).Float64()
		parent.children[parts[len(parts)-1]] = &entry{size: int64(size), digest: f["digest"].(string)}
	}
	return root
}

// Load reads a content-only installation: package.json and exactly its inventoried
// payloads/parent directories. It authenticates the manifest against expected
// before reading any payload, then verifies and compiles the frozen bytes offline.
// No package-derived code is executed. A valid expected pin is not inferred from
// the incoming package. Directory ancestors are trusted installation paths.
func Load(ctx context.Context, directory string, expected contracts.PackageIdentity) (*Loaded, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(directory) > 4096 || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" || strings.IndexFunc(directory, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return nil, ErrPath
	}
	initial, err := os.Lstat(directory)
	if err != nil || !safeDir(initial) {
		return nil, ErrInstallation
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrInstallation
	}
	defer root.Close()
	actual, err := root.Stat(".")
	if err != nil || !safeDir(actual) || !os.SameFile(initial, actual) {
		return nil, ErrInstallation
	}
	manifest, err := readFile(ctx, root, "package.json", contracts.PackageManifestLimit, -1)
	if err != nil {
		return nil, err
	}
	validator, err := bootstrap()
	if err != nil {
		return nil, ErrIntegrity
	}
	m, err := validator.ValidatePackageManifest(manifest)
	if err != nil {
		return nil, ErrManifest
	}
	digest, err := contracts.CanonicalDigest(manifest, contracts.PackageManifestLimit)
	if err != nil || m["package_version"] != expected.Version || digest != expected.Digest {
		return nil, ErrPin
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	if err := readTree(ctx, root, "", inventory(m, manifest), files); err != nil {
		return nil, err
	}
	// The inventory walk includes the original manifest. Neither a mutable
	// catalog nor later filesystem changes supply bytes to the verified loader.
	if !bytes.Equal(files["package.json"], manifest) {
		return nil, ErrIntegrity
	}
	delete(files, "package.json")
	final, err := os.Lstat(directory)
	if err != nil || !safeDir(final) || !os.SameFile(initial, final) {
		return nil, ErrInstallation
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	protocol, err := validator.LoadVerifiedProtocol(manifest, files, expected)
	if err != nil {
		return nil, ErrIntegrity
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var total int64
	for _, raw := range files {
		total += int64(len(raw))
	}
	registry := protocol.RegistryDigests()
	return &Loaded{protocol: protocol, manifest: manifest, report: Report{
		APIVersion: ReportVersion, Status: "verified", Directory: directory, Package: expected,
		ManifestDigest: contracts.RawDigest(manifest), CatalogDigest: registry["catalog_digest"], OperationsDigest: registry["operations_digest"],
		FileCount: len(files), ContentBytes: total,
	}}, nil
}

func safeOwner(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && (st.Uid == 0 || st.Uid == uint32(os.Geteuid())) && info.Mode().Perm()&0022 == 0 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}
func safeDir(info os.FileInfo) bool { return info.IsDir() && safeOwner(info) }
func safeFile(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && safeOwner(info) && st.Nlink == 1
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func readFile(ctx context.Context, root *os.Root, name string, limit int64, size int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil || !safeFile(info) || info.Size() > limit || (size >= 0 && info.Size() != size) {
		return nil, ErrInstallation
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrInstallation
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !safeFile(before) || !os.SameFile(info, before) || before.Size() != info.Size() {
		return nil, ErrInstallation
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrInstallation
	}
	after, err := f.Stat()
	if err != nil || !safeFile(after) || int64(len(raw)) > limit || before.Size() != after.Size() || after.Size() != int64(len(raw)) || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrInstallation
	}
	current, err := root.Lstat(name)
	if err != nil || !safeFile(current) || !os.SameFile(after, current) {
		return nil, ErrInstallation
	}
	return raw, nil
}

// Each recursive step uses an independently pinned child root. Read names in
// bounded batches and reject the first undeclared name, including empty extras.
func readTree(ctx context.Context, root *os.Root, prefix string, want *entry, files map[string][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return ErrInstallation
	}
	defer dir.Close()
	before, err := dir.Stat()
	if err != nil || !safeDir(before) {
		return ErrInstallation
	}
	seen := map[string]bool{}
	for {
		names, readErr := dir.Readdirnames(128)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return ErrInstallation
		}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			e := want.children[name]
			if e == nil || seen[name] {
				return ErrInstallation
			}
			seen[name] = true
			full := path.Join(prefix, name)
			if e.children == nil {
				raw, err := readFile(ctx, root, name, e.size, e.size)
				if err != nil {
					return err
				}
				if contracts.RawDigest(raw) != e.digest {
					return ErrIntegrity
				}
				files[full] = raw
				continue
			}
			info, err := root.Lstat(name)
			if err != nil || !safeDir(info) {
				return ErrInstallation
			}
			child, err := root.OpenRoot(name)
			if err != nil {
				return ErrInstallation
			}
			actual, err := child.Stat(".")
			if err != nil || !safeDir(actual) || !os.SameFile(info, actual) {
				child.Close()
				return ErrInstallation
			}
			err = readTree(ctx, child, full, e, files)
			child.Close()
			if err != nil {
				return err
			}
			after, err := root.Lstat(name)
			if err != nil || !safeDir(after) || !os.SameFile(info, after) {
				return ErrInstallation
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	after, err := dir.Stat()
	if err != nil || !safeDir(after) || !before.ModTime().Equal(after.ModTime()) || len(seen) != len(want.children) {
		return ErrInstallation
	}
	return nil
}
