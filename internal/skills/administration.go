//go:build linux || darwin

package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// Directory flock keeps removal and publication exclusive while readers capture
// frozen copies. No lease is kept for the lifetime of an accepted campaign.
func storeLease(store string, exclusive bool) (*os.File, error) {
	r, err := privateRoot(store)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("skill store busy")
	}
	return f, nil
}

// Remove deletes only one hash-named installed tree. No denylist or tombstone
// prevents later import/build of the same bytes. Partial deletion is retryable.
func Remove(ctx context.Context, store, digest string) (bool, error) {
	if !digestPattern.MatchString(digest) || !filepath.IsAbs(store) || filepath.Clean(store) != store {
		return false, ErrSkill
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := os.Lstat(store); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	lease, err := storeLease(store, true)
	if err != nil {
		return false, err
	}
	defer lease.Close()
	r, err := privateRoot(store)
	if err != nil {
		return false, err
	}
	defer r.Close()
	name := strings.TrimPrefix(digest, "sha256:")
	info, err := r.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A substituted link or mount is never treated as the installed directory.
	parent, err := r.Stat(".")
	if err != nil {
		return false, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	ps, valid := parent.Sys().(*syscall.Stat_t)
	if !ok || !valid || !info.IsDir() || st.Dev != ps.Dev {
		return false, ErrSkill
	}
	if err = campaign.RemovePurgeTree(ctx, r, name, uint64(ps.Dev)); err != nil {
		return false, err
	}
	return true, nil
}

// Save writes a portable frozen manifest. It contains digests, not trust keys or
// installation paths. Existing output is never overwritten.
func (s *Selection) Save(ctx context.Context, name string) error {
	if s == nil || !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return ErrSkill
	}
	r, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer r.Close()
	if err = write(ctx, r, filepath.Base(name), s.Manifest()); err != nil {
		return err
	}
	return syncDir(r, ".")
}
