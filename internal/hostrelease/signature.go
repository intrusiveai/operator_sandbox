//go:build linux || darwin

// Package hostrelease verifies executable distribution artifacts independently
// of campaign content and the Attack Harness image-approval service.
package hostrelease

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

var ErrSignature = errors.New("host release signature verification failed")

const ManifestLimit = 4 << 20
const SignatureLimit = 64 << 10

// VerifySignature checks exact manifest bytes against an explicitly installed
// OpenPGP public keyring. Nothing from the release can add a trusted key. The
// temporary home excludes ambient keyrings and is removed after verification.
func VerifySignature(ctx context.Context, verifier, keyring string, manifest, signature []byte) error {
	if len(manifest) == 0 || len(manifest) > ManifestLimit || len(signature) == 0 || len(signature) > SignatureLimit || !filepath.IsAbs(verifier) || filepath.Clean(verifier) != verifier {
		return ErrSignature
	}
	trusted, err := staging.Capture(ctx, keyring, 4<<20)
	if err != nil || len(trusted) == 0 {
		return ErrSignature
	}
	home, err := os.MkdirTemp("", "operator-release-verify-")
	if err != nil {
		return ErrSignature
	}
	defer os.RemoveAll(home)
	for name, data := range map[string][]byte{"trusted.gpg": trusted, "manifest.json": manifest, "manifest.sig": signature} {
		if err := os.WriteFile(filepath.Join(home, name), data, 0600); err != nil {
			return ErrSignature
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, verifier, "--homedir", home, "--keyring", filepath.Join(home, "trusted.gpg"), "--weak-digest", "SHA1", "--weak-digest", "MD5", "--", filepath.Join(home, "manifest.sig"), filepath.Join(home, "manifest.json"))
	cmd.Env = []string{"LC_ALL=C"}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return ErrSignature
	}
	return nil
}
