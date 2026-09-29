//go:build linux || darwin

package livequalification

import (
	"os"
	"path/filepath"
	"syscall"
)

// CreateEvidence anchors creation to a verified private parent, excludes links
// and replacement, and syncs the directory entry before any service call.
func CreateEvidence(name string) (*os.File, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, ErrPlan
	}
	parent := filepath.Dir(name)
	before, err := os.Lstat(parent)
	if err != nil || !before.IsDir() || before.Mode().Perm()&0077 != 0 {
		return nil, ErrPlan
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
		return nil, ErrPlan
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, ErrPlan
	}
	defer root.Close()
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return nil, ErrPlan
	}
	file, err := root.OpenFile(filepath.Base(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, ErrPlan
	}
	dir, err := root.Open(".")
	if err == nil {
		err = dir.Sync()
		closeErr := dir.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		file.Close()
		return nil, ErrPlan
	}
	return file, nil
}
