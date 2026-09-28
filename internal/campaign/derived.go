//go:build linux || darwin

package campaign

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

const DerivedManifestLimit = 16 << 20

type DerivedFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"size_bytes"`
	Digest string `json:"digest"`
}
type Generation struct {
	APIVersion     string        `json:"api_version"`
	CampaignID     string        `json:"campaign_id"`
	ManifestDigest string        `json:"run_manifest_digest"`
	Files          []DerivedFile `json:"files"`
}

// DerivedFiles owns disposable, host-generated output under an already locked
// campaign. Only the final generation manifest makes these derived bytes public.
type DerivedFiles struct {
	owner  *NativeRecovery
	dir    string
	files  map[string]DerivedFile
	failed bool
}

func (r *NativeRecovery) NewDerived() (*DerivedFiles, error) {
	if e := mkdir(r.root, "reports"); e != nil {
		return nil, e
	}
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	dir := "reports/.building-" + hex.EncodeToString(nonce)
	if e := mkdir(r.root, dir); e != nil {
		return nil, e
	}
	return &DerivedFiles{owner: r, dir: dir, files: map[string]DerivedFile{}}, nil
}
func (d *DerivedFiles) Close() error {
	if d.dir == "" {
		return nil
	}
	return d.owner.root.RemoveAll(d.dir)
}
func derivedName(name string) bool {
	if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !validID(p) || p == ".." || p == "." {
			return false
		}
	}
	return true
}
func (d *DerivedFiles) Put(ctx context.Context, name string, raw []byte) error {
	return d.Copy(ctx, name, bytes.NewReader(raw), int64(len(raw)), contracts.RawDigest(raw))
}
func (d *DerivedFiles) Copy(ctx context.Context, name string, reader io.Reader, size int64, digest string) (err error) {
	if d.dir == "" || !derivedName(name) || name == "generation.json" || size < 0 || size > contracts.MaxSafeInteger || !validDigest(digest) {
		return ErrInvalid
	}
	want := DerivedFile{name, size, digest}
	if old, ok := d.files[name]; ok {
		if old != want {
			return ErrCorrupt
		}
		return nil
	}
	if len(d.files) >= 100000 {
		return ErrQuota
	}
	if e := d.owner.evidenceSpace(size + DerivedManifestLimit); e != nil {
		return e
	}
	if parent := path.Dir(name); parent != "." {
		if e := mkdir(d.owner.root, d.dir+"/"+parent); e != nil {
			return e
		}
	}
	f, e := d.owner.root.OpenFile(d.dir+"/"+name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer func() {
		if err != nil {
			d.failed = true
		}
	}()
	h := sha256.New()
	n, e := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(evidenceContextReader{ctx, reader}, size+1), make([]byte, 64<<10))
	if e == nil && (n != size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest) {
		e = ErrCorrupt
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = syncDir(d.owner.root, path.Dir(d.dir+"/"+name)); e != nil {
		return e
	}
	d.files[name] = want
	return nil
}
func (d *DerivedFiles) Commit(ctx context.Context) (Generation, string, error) {
	g := Generation{"operator.dev/report-generation/v1alpha1", d.owner.manifest.CampaignID, d.owner.digest, []DerivedFile{}}
	if d.failed || d.dir == "" {
		return g, "", ErrInvalid
	}
	for _, f := range d.files {
		g.Files = append(g.Files, f)
	}
	sort.Slice(g.Files, func(i, j int) bool { return g.Files[i].Path < g.Files[j].Path })
	raw, e := encode(g, DerivedManifestLimit)
	if e != nil {
		return g, "", e
	}
	digest := contracts.RawDigest(raw)
	dir := "reports/" + digest[7:]
	if e = ctx.Err(); e != nil {
		return g, "", e
	}
	if _, e = d.owner.root.Lstat(dir); e == nil {
		if e = privateDir(d.owner.root, dir); e != nil {
			return g, "", e
		}
		old, e := readFile(d.owner.root, dir+"/generation.json", DerivedManifestLimit)
		if e != nil || !bytes.Equal(old, raw) {
			return g, "", ErrCorrupt
		}
		for _, entry := range g.Files {
			if e = d.owner.copyDerived(ctx, dir, entry, io.Discard); e != nil {
				return g, "", e
			}
		}
		return g, digest, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return g, "", e
	}
	hooks := diskHooks()
	if e = publish(d.owner.root, d.dir+"/generation.json", raw, false, &hooks); e != nil {
		return g, "", e
	}
	if e = d.owner.root.Rename(d.dir, dir); e != nil {
		return g, "", e
	}
	d.dir = ""
	if e = syncDir(d.owner.root, "reports"); e != nil {
		return g, "", e
	}
	return g, digest, nil
}
func (r *NativeRecovery) copyDerived(ctx context.Context, dir string, entry DerivedFile, dst io.Writer) error {
	if !derivedName(entry.Path) {
		return ErrInvalid
	}
	if p := path.Dir(entry.Path); p != "." {
		if e := privateDir(r.root, dir+"/"+p); e != nil {
			return e
		}
	}
	f, e := openRegular(r.root, dir+"/"+entry.Path, os.O_RDONLY)
	if e != nil {
		return e
	}
	defer f.Close()
	return copyChecked(ctx, dst, f, entry.Bytes, entry.Digest)
}
func copyChecked(ctx context.Context, dst io.Writer, src io.Reader, size int64, digest string) error {
	h := sha256.New()
	n, e := io.CopyBuffer(io.MultiWriter(dst, h), io.LimitReader(evidenceContextReader{ctx, src}, size+1), make([]byte, 64<<10))
	if e != nil {
		return e
	}
	if n != size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest {
		return ErrCorrupt
	}
	return nil
}

// ExportDerived creates an exclusively owned output directory. export.json is
// published last; interrupted exports are explicit partial directories and are
// never overwritten or adopted on a retry. The caller keeps the campaign lock.
func (r *NativeRecovery) ExportDerived(ctx context.Context, g Generation, digest, output string, archives []NativeEvidence) error {
	raw, e := encode(g, DerivedManifestLimit)
	if e != nil || contracts.RawDigest(raw) != digest || g.CampaignID != r.manifest.CampaignID || g.ManifestDigest != r.digest {
		return ErrInvalid
	}
	if e = os.Mkdir(output, 0700); e != nil {
		return e
	}
	dst, e := os.OpenRoot(output)
	if e != nil {
		return e
	}
	defer dst.Close()
	entries := []DerivedFile{}
	write := func(entry DerivedFile, copy func(io.Writer) error) error {
		if !derivedName(entry.Path) || entry.Bytes < 0 || entry.Bytes > contracts.MaxSafeInteger || !validDigest(entry.Digest) {
			return ErrInvalid
		}
		if p := path.Dir(entry.Path); p != "." {
			if e := mkdir(dst, p); e != nil {
				return e
			}
		}
		f, e := dst.OpenFile(entry.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		e = copy(f)
		if e == nil {
			e = f.Sync()
		}
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		if e = syncDir(dst, path.Dir(entry.Path)); e != nil {
			return e
		}
		entries = append(entries, entry)
		return nil
	}
	for _, entry := range g.Files {
		if e = write(entry, func(w io.Writer) error { return r.copyDerived(ctx, "reports/"+digest[7:], entry, w) }); e != nil {
			return e
		}
	}
	entry := DerivedFile{"generation.json", int64(len(raw)), digest}
	if e = write(entry, func(w io.Writer) error { _, e := w.Write(raw); return e }); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, archive := range archives {
		if e = r.VerifyRetainedEvidence(ctx, archive); e != nil {
			return e
		}
		p := archive.Provenance.Transfer
		name := "evidence/" + p.SHA256[7:] + ".tar"
		if seen[name] {
			continue
		}
		seen[name] = true
		e = write(DerivedFile{name, p.Bytes, p.SHA256}, func(w io.Writer) error {
			f, e := openRegular(r.root, archive.Path, os.O_RDONLY)
			if e != nil {
				return e
			}
			defer f.Close()
			return copyChecked(ctx, w, f, p.Bytes, p.SHA256)
		})
		if e != nil {
			return e
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	manifest, e := encode(struct {
		APIVersion string        `json:"api_version"`
		Generation string        `json:"generation_digest"`
		Files      []DerivedFile `json:"files"`
	}{"operator.dev/campaign-export/v1alpha1", digest, entries}, DerivedManifestLimit)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	hooks := diskHooks()
	if e = publish(dst, "export.json", manifest, false, &hooks); e != nil {
		return e
	}
	parent, e := os.Open(path.Dir(output))
	if e != nil {
		return e
	}
	return errors.Join(parent.Sync(), parent.Close())
}
