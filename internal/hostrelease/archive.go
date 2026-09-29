//go:build linux || darwin

package hostrelease

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

// VerifyDirectory authenticates the manifest before trusting any inventory entry.
func VerifyDirectory(ctx context.Context, directory, verifier, keyring string) (Manifest, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return Manifest{}, ErrRelease
	}
	raw, err := staging.Capture(ctx, filepath.Join(directory, "release.json"), ManifestLimit)
	if err != nil {
		return Manifest{}, err
	}
	sig, err := staging.Capture(ctx, filepath.Join(directory, "release.sig"), SignatureLimit)
	if err != nil {
		return Manifest{}, err
	}
	if err = VerifySignature(ctx, verifier, keyring, raw, sig); err != nil {
		return Manifest{}, err
	}
	m, err := DecodeManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	return m, CheckContents(ctx, directory, m)
}

// Archive emits sorted, normalized regular files. Output must not already exist.
// Signature verification is mandatory again on the consuming installation.
func Archive(ctx context.Context, directory, output string) (err error) {
	raw, err := staging.Capture(ctx, filepath.Join(directory, "release.json"), ManifestLimit)
	if err != nil {
		return err
	}
	m, err := DecodeManifest(raw)
	if err != nil {
		return err
	}
	if err = CheckContents(ctx, directory, m); err != nil {
		return err
	}
	sig, err := staging.Capture(ctx, filepath.Join(directory, "release.sig"), SignatureLimit)
	if err != nil || len(sig) == 0 {
		return ErrSignature
	}
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		out.Close()
		if !done {
			os.Remove(output)
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	entries := append([]File{{Path: "release.json", Size: int64(len(raw)), Mode: 0600}, {Path: "release.sig", Size: int64(len(sig)), Mode: 0600}}, m.Files...)
	for _, f := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: f.Path, Size: f.Size, Mode: int64(f.Mode), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if f.Path == "release.json" {
			_, err = tw.Write(raw)
		} else if f.Path == "release.sig" {
			_, err = tw.Write(sig)
		} else {
			input, e := root.Open(f.Path)
			if e != nil {
				return e
			}
			h := sha256.New()
			n, e := io.Copy(io.MultiWriter(tw, h), io.LimitReader(input, f.Size+1))
			ce := input.Close()
			if e != nil || ce != nil || n != f.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != f.Digest {
				return ErrRelease
			}
		}
		if err != nil {
			return err
		}
	}
	if err = errors.Join(tw.Close(), gz.Close(), out.Sync()); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	done = true
	return nil
}

// Extract accepts only the bounded file-only format emitted by Archive. It writes
// to a fresh private directory; callers authenticate it before activation.
func Extract(ctx context.Context, archive, destination string) (err error) {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxReleaseBytes+8<<20 {
		return ErrRelease
	}
	gz, err := gzip.NewReader(in)
	if err != nil {
		return ErrRelease
	}
	defer gz.Close()
	if err = os.Mkdir(destination, 0700); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(destination)
		}
	}()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(io.LimitReader(gz, MaxReleaseBytes+8<<20))
	seen := map[string]bool{}
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ErrRelease
		}
		if h.Typeflag != tar.TypeReg || !validPath(h.Name) || seen[h.Name] || len(seen) >= MaxFiles+2 || h.Size < 0 || h.Size > MaxFileBytes || len(h.PAXRecords) > 0 {
			return ErrRelease
		}
		limit := MaxFileBytes
		if h.Name == "release.json" {
			limit = ManifestLimit
		}
		if h.Name == "release.sig" {
			limit = SignatureLimit
		}
		if h.Size > limit {
			return ErrRelease
		}
		mode := int64(0600)
		if h.Name == "bin/operatorctl" {
			mode = 0700
		}
		if h.Mode != mode {
			return ErrRelease
		}
		total += h.Size
		if total > MaxReleaseBytes+ManifestLimit+SignatureLimit {
			return ErrRelease
		}
		seen[h.Name] = true
		if e = root.MkdirAll(filepath.Dir(h.Name), 0700); e != nil {
			return e
		}
		out, e := root.OpenFile(h.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(mode))
		if e != nil {
			return e
		}
		n, e := io.Copy(out, tr)
		ce := errors.Join(out.Sync(), out.Close())
		if e != nil || ce != nil || n != h.Size {
			return ErrRelease
		}
	}
	// Drain to validate gzip CRC and reject data after the tar terminator.
	n, e := io.Copy(io.Discard, io.LimitReader(gz, 1))
	if e != nil || n != 0 {
		return ErrRelease
	}
	if !seen["release.json"] || !seen["release.sig"] {
		return ErrRelease
	}
	done = true
	return nil
}
