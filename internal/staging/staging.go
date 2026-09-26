//go:build linux || darwin

// Package staging materializes verified inventory bytes in host-owned read-only
// mount trees. It does not establish release, skill or target admission authority.
package staging

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

var (
	ErrInputs     = errors.New("staging inputs do not match the verified contract and manifest inventories")
	ErrFilesystem = errors.New("staging filesystem validation or publication failed")
)

type Manifests struct {
	InputTree, SkillSet []byte
	Skills              [][]byte
}

type Receipt struct {
	APIVersion      string                    `json:"api_version"`
	Contract        contracts.PackageIdentity `json:"contract"`
	InputTreeDigest string                    `json:"input_tree_digest"`
	SkillSetDigest  string                    `json:"skill_set_digest"`
	FileCount       int                       `json:"file_count"`
	ContentBytes    int64                     `json:"content_bytes"`
}

type file struct {
	size   int64
	digest string
	mode   os.FileMode
}

// Tree has no open handles or caller-owned content buffers. Directory is the
// private wrapper; mount only its input, customer-skills and manifests children.
type Tree struct {
	directory string
	identity  os.FileInfo
	files     map[string]file
	dirs      []string
	receipt   Receipt
}

func (t *Tree) Directory() string { return t.directory }
func (t *Tree) Receipt() Receipt  { return t.receipt }

func absolute(p string) bool {
	return len(p) <= 4096 && p != "/" && filepath.IsAbs(p) && filepath.Clean(p) == p && strings.IndexFunc(p, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func integer(v any) int64 { n, _ := v.(json.Number).Float64(); return int64(n) } // schema-validated safe integer

// Capture reads an explicitly selected source file, without importing metadata or
// returning its path as guest data. Callers retain the independent expected digest.
// The selected file's ancestors are trusted host paths. No directory is mounted.
func Capture(ctx context.Context, name string, maximum int64) ([]byte, error) {
	if !absolute(name) || maximum < 0 || maximum > 64<<20 {
		return nil, ErrInputs
	}
	parent, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return nil, ErrFilesystem
	}
	defer parent.Close()
	return read(ctx, parent, filepath.Base(name), maximum, nil)
}

// Create takes frozen preparation bytes. Keys are input/<relative path> or
// customer-skills/<skill_id>/<relative path>. Manifest mount files are generated
// from m. Callers must not mutate buffers concurrently with this call. A verified
// protocol pin is mandatory; data/release/skill admission remain separate gates.
func Create(ctx context.Context, p *contracts.Protocol, parent string, m Manifests, contents map[string][]byte) (result *Tree, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrInputs
	}
	pin, ok := p.PackageIdentity()
	if !ok {
		return nil, ErrInputs
	}
	if p.ValidateManifestSet(m.InputTree, m.SkillSet, m.Skills) != nil {
		return nil, ErrInputs
	}
	tree, _ := p.ValidateInputTree(m.InputTree)
	set, _ := p.ValidateSkillSet(m.SkillSet)
	// Canonical per-skill identities and the loading identity supplement the raw
	// descriptor checks already performed by ValidateManifestSet.
	loading := map[string]any{}
	for k, v := range set {
		if k != "loading_digest" {
			loading[k] = v
		}
	}
	loadingRaw, _ := json.Marshal(loading)
	loadingDigest, e := contracts.CanonicalDigest(loadingRaw, contracts.ControlLimit)
	if e != nil || loadingDigest != set["loading_digest"] {
		return nil, ErrInputs
	}
	frozen := map[string][]byte{}
	add := func(name string, entry map[string]any) bool {
		raw, exists := contents[name]
		if !exists || int64(len(raw)) != integer(entry["size_bytes"]) || contracts.RawDigest(raw) != entry["digest"] {
			return false
		}
		frozen[name] = bytes.Clone(raw)
		return true
	}
	for _, v := range tree["entries"].([]any) {
		entry := v.(map[string]any)
		if !add("input/"+entry["path"].(string), entry) {
			return nil, ErrInputs
		}
	}
	for i, raw := range m.Skills {
		d, _ := contracts.CanonicalDigest(raw, contracts.SkillManifestLimit)
		descriptor := set["skills"].([]any)[i].(map[string]any)["manifest"].(map[string]any)
		if d != descriptor["object_digest"] {
			return nil, ErrInputs
		}
		skill, _ := p.ValidateSkillManifest(raw)
		for _, v := range skill["files"].([]any) {
			entry := v.(map[string]any)
			if !add("customer-skills/"+skill["skill_id"].(string)+"/"+entry["path"].(string), entry) {
				return nil, ErrInputs
			}
		}
	}
	if len(frozen) != len(contents) {
		return nil, ErrInputs
	}
	frozen["manifests/input-tree.json"] = bytes.Clone(m.InputTree)
	frozen["manifests/skill-set.json"] = bytes.Clone(m.SkillSet)
	for i, raw := range m.Skills {
		frozen[fmt.Sprintf("manifests/skills/%04d.json", i)] = bytes.Clone(raw)
	}
	t := &Tree{files: map[string]file{}}
	t.receipt = Receipt{APIVersion: "operator.dev/input-staging/v1alpha1", Contract: pin, FileCount: len(frozen)}
	t.receipt.InputTreeDigest, _ = contracts.CanonicalDigest(m.InputTree, contracts.InputTreeManifestLimit)
	t.receipt.SkillSetDigest, _ = contracts.CanonicalDigest(m.SkillSet, contracts.ControlLimit)
	dirs := map[string]bool{"input": true, "customer-skills": true, "manifests": true}
	for name, raw := range frozen {
		t.files[name] = file{int64(len(raw)), contracts.RawDigest(raw), 0444}
		t.receipt.ContentBytes += int64(len(raw))
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for d := range dirs {
		t.dirs = append(t.dirs, d)
	}
	sort.Slice(t.dirs, func(i, j int) bool {
		a, b := t.dirs[i], t.dirs[j]
		if strings.Count(a, "/") != strings.Count(b, "/") {
			return strings.Count(a, "/") < strings.Count(b, "/")
		}
		return a < b
	})
	if !absolute(parent) {
		return nil, ErrFilesystem
	}
	info, e := os.Lstat(parent)
	if e != nil || !private(info) {
		return nil, ErrFilesystem
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return nil, ErrFilesystem
	}
	defer r.Close()
	actual, e := r.Stat(".")
	if e != nil || !os.SameFile(info, actual) || !private(actual) {
		return nil, ErrFilesystem
	}
	name := "stage-" + rand.Text()
	if e = r.Mkdir(name, 0700); e != nil {
		return nil, ErrFilesystem
	}
	if e = r.Chmod(name, 0700); e != nil {
		return nil, fmt.Errorf("%w; incomplete staging retained at %s", ErrFilesystem, filepath.Join(parent, name))
	}
	t.directory = filepath.Join(parent, name)
	t.identity, e = r.Lstat(name)
	if e != nil {
		return nil, ErrFilesystem
	}
	defer func() {
		if err != nil {
			if cleanup := t.Discard(); cleanup != nil {
				err = fmt.Errorf("%w; incomplete staging retained at %s", err, t.directory)
			}
		}
	}()
	root, e := r.OpenRoot(name)
	if e != nil {
		return nil, ErrFilesystem
	}
	defer root.Close()
	for _, d := range t.dirs {
		if e = root.Mkdir(d, 0700); e != nil {
			return nil, ErrFilesystem
		}
		if e = root.Chmod(d, 0700); e != nil {
			return nil, ErrFilesystem
		}
	}
	for name, raw := range frozen {
		if e = write(ctx, root, name, raw, 0444); e != nil {
			return nil, e
		}
	}
	for i := len(t.dirs) - 1; i >= 0; i-- {
		if e = root.Chmod(t.dirs[i], 0555); e != nil {
			return nil, ErrFilesystem
		}
		if e = syncDir(root, t.dirs[i]); e != nil {
			return nil, ErrFilesystem
		}
	}
	// Exclusive link publication prevents a partial receipt becoming visible.
	receipt, _ := json.Marshal(t.receipt)
	if e = write(ctx, root, "staging-receipt.json.pending", receipt, 0600); e != nil {
		return nil, e
	}
	if e = root.Link("staging-receipt.json.pending", "staging-receipt.json"); e != nil {
		return nil, ErrFilesystem
	}
	if e = root.Remove("staging-receipt.json.pending"); e != nil {
		return nil, ErrFilesystem
	}
	t.files["staging-receipt.json"] = file{int64(len(receipt)), contracts.RawDigest(receipt), 0600}
	if syncDir(root, ".") != nil || syncDir(r, ".") != nil {
		return nil, ErrFilesystem
	}
	if e = t.Verify(ctx); e != nil {
		return nil, e
	}
	return t, nil
}

func private(i os.FileInfo) bool { return i.IsDir() && owned(i) && i.Mode() == os.ModeDir|0700 }
func owned(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}
func regular(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && i.Mode().IsRegular() && s.Nlink == 1 && i.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}

func write(ctx context.Context, r *os.Root, name string, raw []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrFilesystem
	}
	defer f.Close()
	for len(raw) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(raw), 64<<10)
		written, err := f.Write(raw[:n])
		if err != nil || written != n {
			return ErrFilesystem
		}
		raw = raw[n:]
	}
	if f.Chmod(mode) != nil || f.Sync() != nil {
		return ErrFilesystem
	}
	if f.Close() != nil {
		return ErrFilesystem
	}
	return nil
}

func syncDir(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func read(ctx context.Context, r *os.Root, name string, limit int64, want *file) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	i, err := r.Lstat(name)
	if err != nil || !regular(i) || i.Size() > limit {
		return nil, ErrFilesystem
	}
	if want != nil && (!owned(i) || i.Mode().Perm() != want.mode || i.Size() != want.size) {
		return nil, ErrFilesystem
	}
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrFilesystem
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !regular(before) || !os.SameFile(i, before) || before.Size() != i.Size() {
		return nil, ErrFilesystem
	}
	var b bytes.Buffer
	buf := make([]byte, 32<<10)
	reader := io.LimitReader(f, limit+1)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, e := reader.Read(buf)
		b.Write(buf[:n])
		if int64(b.Len()) > limit {
			return nil, ErrFilesystem
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, ErrFilesystem
		}
	}
	after, err := f.Stat()
	if err != nil || !regular(after) || after.Size() != int64(b.Len()) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrFilesystem
	}
	if want != nil && (!owned(after) || after.Mode().Perm() != want.mode || contracts.RawDigest(b.Bytes()) != want.digest) {
		return nil, ErrFilesystem
	}
	return b.Bytes(), nil
}

// Verify rechecks exact names, modes, raw bytes and the original directory inode.
// It must pass again before exposing these trees to Docker. The service owns the
// backing files and must prevent writes throughout the harness lifetime.
func (t *Tree) Verify(ctx context.Context) error {
	r, err := t.open()
	if err != nil {
		return err
	}
	defer r.Close()
	expected := map[string]bool{}
	for name := range t.files {
		expected[name] = false
	}
	for _, name := range t.dirs {
		expected[name] = true
	}
	seen := 0
	var walk func(*os.Root, string) error
	walk = func(root *os.Root, prefix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := root.Open(".")
		if err != nil {
			return ErrFilesystem
		}
		defer f.Close()
		for {
			names, e := f.Readdirnames(128)
			if e != nil && e != io.EOF {
				return ErrFilesystem
			}
			for _, name := range names {
				full := path.Join(prefix, name)
				directory, ok := expected[full]
				if !ok {
					return ErrFilesystem
				}
				delete(expected, full)
				seen++
				if directory {
					i, err := root.Lstat(name)
					if err != nil || !owned(i) || i.Mode() != os.ModeDir|0555 {
						return ErrFilesystem
					}
					child, err := root.OpenRoot(name)
					if err != nil {
						return ErrFilesystem
					}
					actual, err := child.Stat(".")
					if err != nil || !os.SameFile(i, actual) {
						child.Close()
						return ErrFilesystem
					}
					err = walk(child, full)
					child.Close()
					if err != nil {
						return err
					}
				} else {
					want := t.files[full]
					if _, err := read(ctx, root, name, want.size, &want); err != nil {
						return err
					}
				}
			}
			if e == io.EOF {
				break
			}
		}
		return nil
	}
	if err := walk(r, ""); err != nil {
		return err
	}
	if len(expected) != 0 || seen != len(t.files)+len(t.dirs) {
		return ErrFilesystem
	}
	return ctx.Err()
}

func (t *Tree) open() (*os.Root, error) {
	if t == nil || t.identity == nil {
		return nil, ErrFilesystem
	}
	i, err := os.Lstat(t.directory)
	if err != nil || !private(i) || !os.SameFile(t.identity, i) {
		return nil, ErrFilesystem
	}
	r, err := os.OpenRoot(t.directory)
	if err != nil {
		return nil, ErrFilesystem
	}
	i, err = r.Stat(".")
	if err != nil || !private(i) || !os.SameFile(t.identity, i) {
		r.Close()
		return nil, ErrFilesystem
	}
	return r, nil
}

// Discard removes this exact staging directory. The caller must ensure it has
// never been mounted or its container has confirmed exit; this API cannot decide.
func (t *Tree) Discard() error {
	r, err := t.open()
	if err != nil {
		return err
	}
	for _, name := range t.dirs {
		i, e := r.Lstat(name)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !i.IsDir() || !owned(i) {
			r.Close()
			return ErrFilesystem
		}
		if e = r.Chmod(name, 0700); e != nil {
			r.Close()
			return ErrFilesystem
		}
	}
	r.Close()
	parent, err := os.OpenRoot(filepath.Dir(t.directory))
	if err != nil {
		return ErrFilesystem
	}
	defer parent.Close()
	i, err := parent.Lstat(filepath.Base(t.directory))
	if err != nil || !os.SameFile(t.identity, i) {
		return ErrFilesystem
	}
	if parent.RemoveAll(filepath.Base(t.directory)) != nil || syncDir(parent, ".") != nil {
		return ErrFilesystem
	}
	return nil
}
