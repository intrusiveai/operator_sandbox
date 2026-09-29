//go:build linux || darwin

package hostrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
)

const ManifestVersion = "operator.dev/host-release/v1alpha1"
const MaxFiles = 8192
const MaxFileBytes int64 = 128 << 20
const MaxReleaseBytes int64 = 256 << 20

var ErrRelease = errors.New("invalid or incompatible host release")
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var hexPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	Digest string `json:"digest"`
	Mode   uint32 `json:"mode"`
}
type Manifest struct {
	APIVersion   string                    `json:"api_version"`
	Version      string                    `json:"version"`
	Platform     string                    `json:"platform"`
	SourceCommit string                    `json:"source_commit"`
	GoToolchain  string                    `json:"go_toolchain"`
	Contract     contracts.PackageIdentity `json:"contract"`
	Files        []File                    `json:"files"`
}

func validPath(name string) bool {
	if name == "" || len(name) > 512 || !namePattern.MatchString(name) || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
func validPlatform(p string) bool {
	return p == "linux/amd64" || p == "linux/arm64" || p == "darwin/amd64" || p == "darwin/arm64"
}
func (m Manifest) Validate() error {
	if m.APIVersion != ManifestVersion || !versionPattern.MatchString(m.Version) || !validPlatform(m.Platform) || !hexPattern.MatchString(m.SourceCommit) || m.GoToolchain != "go1.26.5" || !versionPattern.MatchString(m.Contract.Version) || !digestPattern.MatchString(m.Contract.Digest) || len(m.Files) == 0 || len(m.Files) > MaxFiles {
		return ErrRelease
	}
	var total int64
	seen := map[string]bool{}
	prior := ""
	for _, f := range m.Files {
		if !validPath(f.Path) || f.Path <= prior || f.Path == "release.json" || f.Path == "release.sig" || f.Size < 0 || f.Size > MaxFileBytes || !digestPattern.MatchString(f.Digest) {
			return ErrRelease
		}
		mode := uint32(0600)
		if f.Path == "bin/operatorctl" {
			mode = 0700
		}
		if f.Mode != mode {
			return ErrRelease
		}
		// No file may also be an ancestor of another file.
		for parent := path.Dir(f.Path); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return ErrRelease
			}
		}
		total += f.Size
		if total > MaxReleaseBytes {
			return ErrRelease
		}
		seen[f.Path] = true
		prior = f.Path
	}
	for _, name := range []string{"bin/operatorctl", "contract/package.json", "templates/operator-config.yaml", "sbom.spdx.json", "provenance.json"} {
		if !seen[name] {
			return ErrRelease
		}
	}
	return nil
}
func DecodeManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if _, err := contracts.Decode(raw, ManifestLimit); err != nil {
		return m, ErrRelease
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || m.Validate() != nil {
		return Manifest{}, ErrRelease
	}
	return m, nil
}

// Inventory hashes only regular, singly linked files in a trusted build tree.
// Signature and manifest are distribution metadata, not recursively inventoried.
func Inventory(ctx context.Context, directory string) ([]File, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := []File{}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if name != "." && !validPath(name) {
				return ErrRelease
			}
			return nil
		}
		if name == "release.json" || name == "release.sig" {
			return nil
		}
		if !validPath(name) || len(files) >= MaxFiles {
			return ErrRelease
		}
		info, e := root.Lstat(name)
		if e != nil {
			return e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || st.Nlink != 1 || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() > MaxFileBytes {
			return ErrRelease
		}
		f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		opened, e := f.Stat()
		if e != nil || !os.SameFile(info, opened) {
			f.Close()
			return ErrRelease
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, MaxFileBytes+1))
		after, se := f.Stat()
		ce := f.Close()
		if e != nil || se != nil || ce != nil || n != info.Size() || after.Size() != n || !after.ModTime().Equal(info.ModTime()) {
			return ErrRelease
		}
		total += n
		if total > MaxReleaseBytes {
			return ErrRelease
		}
		mode := uint32(0600)
		if name == "bin/operatorctl" {
			if info.Mode().Perm()&0100 == 0 {
				return ErrRelease
			}
			mode = 0700
		}
		files = append(files, File{name, n, "sha256:" + hex.EncodeToString(h.Sum(nil)), mode})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func CheckContents(ctx context.Context, directory string, m Manifest) error {
	if m.Validate() != nil {
		return ErrRelease
	}
	files, err := Inventory(ctx, directory)
	if err != nil {
		return err
	}
	if len(files) != len(m.Files) {
		return ErrRelease
	}
	for i := range files {
		if files[i] != m.Files[i] {
			return ErrRelease
		}
	}
	_, err = contractstore.Load(ctx, filepath.Join(directory, "contract"), m.Contract)
	return err
}
