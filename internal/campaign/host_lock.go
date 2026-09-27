//go:build linux || darwin

package campaign

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

// HostLease serializes workers using the same installed private state root.
// It is not proof that containers left by a dead worker have stopped. Callers
// MUST reconcile those saved bindings before starting another campaign.
// Administrative termination/status do not acquire this worker-only lock.
type HostLease struct {
	file *os.File
	once sync.Once
	err  error
}

func AcquireHostLease(stateRoot string) (*HostLease, error) {
	root, err := os.OpenRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = privateDir(root, "."); err != nil {
		return nil, err
	}
	const name = "execution.lock"
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		f, err = openRegular(root, name, os.O_RDWR)
	}
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, valid := info.Sys().(*syscall.Stat_t)
	if !valid || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || st.Nlink != 1 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
		return nil, ErrInvalid
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrActive
		}
		return nil, err
	}
	actual, err := root.Lstat(name)
	if err != nil || !os.SameFile(info, actual) {
		return nil, ErrInvalid
	}
	if err = syncDir(root, "."); err != nil {
		return nil, err
	}
	ok = true
	return &HostLease{file: f}, nil
}
func (l *HostLease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() { l.err = l.file.Close() })
	return l.err
}
