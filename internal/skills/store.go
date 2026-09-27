//go:build linux || darwin

package skills

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

type signature struct {
	APIVersion     string `json:"api_version"`
	KeyID          string `json:"key_id"`
	ManifestDigest string `json:"manifest_digest"`
	Signature      string `json:"signature,omitempty"`
}

// Keygen explicitly creates one installation trust root; existing keys are never
// overwritten. No campaign or build operation implicitly creates trust.
func Keygen(ctx context.Context, directory string) (string, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", ErrSkill
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	defer clear(private)
	if err := os.Mkdir(directory, 0700); err != nil {
		return "", err
	}
	r, err := privateRoot(directory)
	if err != nil {
		return "", err
	}
	defer r.Close()
	if err := write(ctx, r, "private.key", private); err != nil {
		return "", err
	}
	if err := write(ctx, r, "public.key", public); err != nil {
		return "", err
	}
	if err := syncDir(r, "."); err != nil {
		return "", err
	}
	if err := syncParent(directory); err != nil {
		return "", err
	}
	return contracts.RawDigest(public), nil
}
func publicKey(directory string) (ed25519.PublicKey, error) {
	r, err := privateRoot(directory)
	if err != nil {
		return nil, err
	}
	r.Close()
	raw, err := hostconfig.ReadPrivate(filepath.Join(directory, "public.key"), ed25519.PublicKeySize)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, ErrSkill
	}
	return ed25519.PublicKey(raw), nil
}
func sign(directory, digest string) ([]byte, error) {
	public, err := publicKey(directory)
	if err != nil {
		return nil, err
	}
	raw, err := hostconfig.ReadPrivate(filepath.Join(directory, "private.key"), ed25519.PrivateKeySize)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, ErrSkill
	}
	defer clear(raw)
	key := ed25519.PrivateKey(raw)
	derived := ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize])
	defer clear(derived)
	if !bytes.Equal(key, derived) || !bytes.Equal(key.Public().(ed25519.PublicKey), public) {
		return nil, ErrSkill
	}
	s := signature{APIVersion: "operator.dev/skill-signature/v1alpha1", KeyID: contracts.RawDigest(public), ManifestDigest: digest}
	payload, err := canonical(s, 4096)
	if err != nil {
		return nil, err
	}
	s.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))
	return canonical(s, 4096)
}
func verifySignature(directory, digest string, raw []byte) error {
	var s signature
	if interceptor.DecodeTypedBody(raw, &s, 4096) != nil || s.APIVersion != "operator.dev/skill-signature/v1alpha1" || s.ManifestDigest != digest {
		return ErrSkill
	}
	public, err := publicKey(directory)
	if err != nil || s.KeyID != contracts.RawDigest(public) {
		return ErrSkill
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(s.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != s.Signature {
		return ErrSkill
	}
	s.Signature = ""
	payload, err := canonical(s, 4096)
	if err != nil || !ed25519.Verify(public, payload, sig) {
		return ErrSkill
	}
	return nil
}

// Read verifies an explicit bundle directory against the independently installed
// public key, then recaptures and validates every inventoried instruction file.
func Read(ctx context.Context, p *contracts.Protocol, keyDirectory, directory string) (*Bundle, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return nil, ErrSkill
	}
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrSkill
	}
	names, err := f.Readdirnames(4)
	if err != nil && len(names) == 0 {
		return nil, ErrSkill
	}
	sort.Strings(names)
	if len(names) != 3 || strings.Join(names, ",") != "files,manifest.json,signature.json" {
		return nil, ErrSkill
	}
	raw, err := staging.Capture(ctx, filepath.Join(directory, "manifest.json"), contracts.SkillManifestLimit)
	if err != nil {
		return nil, err
	}
	canonicalRaw, err := contracts.Canonicalize(raw, contracts.SkillManifestLimit)
	if err != nil || !bytes.Equal(raw, canonicalRaw) {
		return nil, ErrSkill
	}
	digest := contracts.RawDigest(raw)
	sig, err := staging.Capture(ctx, filepath.Join(directory, "signature.json"), 4096)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(keyDirectory, digest, sig); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrSkill
	}
	if _, err := p.ValidateSkillManifest(raw); err != nil {
		return nil, ErrSkill
	}
	var m manifest
	if json.Unmarshal(raw, &m) != nil {
		return nil, ErrSkill
	}
	parts := strings.Split(m.SkillID, ":")
	if len(parts) != 2 {
		return nil, ErrSkill
	}
	files, err := staging.CaptureInstructionTree(ctx, filepath.Join(directory, "files"))
	if err != nil {
		return nil, err
	}
	b, err := validateContent(p, parts[0], files)
	if err != nil || !bytes.Equal(raw, b.raw) {
		return nil, ErrSkill
	}
	// Imported inventories must already be normalized. Do not silently repair
	// changed CRLF/path bytes while checking a signed, supposedly immutable object.
	for name, raw := range files {
		if !bytes.Equal(raw, b.files[name]) {
			return nil, ErrSkill
		}
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(info, current) {
		return nil, ErrSkill
	}
	b.signature = bytes.Clone(sig)
	return b, nil
}

// Install publishes verified frozen bytes under their canonical manifest digest.
// An existing complete bundle is checked again; partial publication fails closed.
func (b *Bundle) Install(ctx context.Context, p *contracts.Protocol, keyDirectory, store string) error {
	if b == nil || !digestPattern.MatchString(b.digest) {
		return ErrSkill
	}
	if err := verifySignature(keyDirectory, b.digest, b.signature); err != nil {
		return err
	}
	if err := os.Mkdir(store, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	r, err := privateRoot(store)
	if err != nil {
		return err
	}
	defer r.Close()
	name := strings.TrimPrefix(b.digest, "sha256:")
	if err := r.Mkdir(name, 0700); errors.Is(err, os.ErrExist) {
		var existing *Bundle
		existing, err = LoadInstalled(ctx, p, keyDirectory, store, b.digest)
		if err == nil && !bytes.Equal(existing.raw, b.raw) {
			return ErrSkill
		}
		return err
	} else if err != nil {
		return err
	}
	if err := r.Mkdir(name+"/files", 0700); err != nil {
		return err
	}
	dirs := map[string]bool{name: true, name + "/files": true}
	for _, entry := range b.manifest.Files {
		file := name + "/files/" + entry.Path
		parts := strings.Split(file, "/")
		for i := 3; i < len(parts); i++ {
			dir := strings.Join(parts[:i], "/")
			if !dirs[dir] {
				if err := r.Mkdir(dir, 0700); err != nil {
					return err
				}
				dirs[dir] = true
			}
		}
		if err := write(ctx, r, file, b.files[entry.Path]); err != nil {
			return err
		}
	}
	if err := write(ctx, r, name+"/manifest.json", b.raw); err != nil {
		return err
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ordered)))
	for _, dir := range ordered {
		if err := syncDir(r, dir); err != nil {
			return err
		}
	}
	// Signature is the final publication marker; a partial tree is not admitted.
	if err := write(ctx, r, name+"/signature.json", b.signature); err != nil {
		return err
	}
	if err := syncDir(r, name); err != nil {
		return err
	}
	if err := syncDir(r, "."); err != nil {
		return err
	}
	return syncParent(store)
}

func LoadInstalled(ctx context.Context, p *contracts.Protocol, keyDirectory, store, digest string) (*Bundle, error) {
	if !digestPattern.MatchString(digest) {
		return nil, ErrSkill
	}
	r, err := privateRoot(store)
	if err != nil {
		return nil, err
	}
	r.Close()
	b, err := Read(ctx, p, keyDirectory, filepath.Join(store, strings.TrimPrefix(digest, "sha256:")))
	if err != nil {
		return nil, err
	}
	if b.digest != digest {
		return nil, ErrSkill
	}
	return b, nil
}
func privateRoot(directory string) (*os.Root, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrSkill
	}
	i, err := os.Lstat(directory)
	if err != nil || !i.IsDir() || i.Mode().Perm() != 0700 || i.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, ErrSkill
	}
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, err
	}
	after, err := f.Stat()
	f.Close()
	if err != nil || !os.SameFile(i, after) {
		r.Close()
		return nil, ErrSkill
	}
	return r, nil
}
func write(ctx context.Context, r *os.Root, name string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func syncDir(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
func syncParent(directory string) error {
	f, err := os.Open(filepath.Dir(directory))
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
