//go:build linux || darwin

package transport

import (
	"errors"
	"io"
	"os"
	"syscall"
)

func freshRoot(dir string) (*os.Root, error) {
	i, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return nil, ErrProtocol
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, err
	}
	entries, e := f.ReadDir(1)
	f.Close()
	if (e != nil && !errors.Is(e, io.EOF)) || len(entries) != 0 {
		r.Close()
		return nil, ErrProtocol
	}
	return r, nil
}

func regular(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	// An atomic ACK replacement or acknowledged producer cleanup can unlink the
	// captured inode while it is open. Zero links is safe; multiple links are not.
	return info.Mode().IsRegular() && ok && stat.Nlink <= 1
}

func readRegular(r *os.Root, name string, maximum int) ([]byte, error) {
	i, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !regular(i) || i.Size() > int64(maximum) {
		return nil, ErrProtocol
	}
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	// Opened bytes are authoritative for this capture. Atomic ACK replacement
	// may legitimately change the directory entry between Lstat and open.
	if err != nil || !regular(actual) || actual.Size() > int64(maximum) {
		return nil, ErrProtocol
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !regular(after) || !os.SameFile(actual, after) || after.Size() != int64(len(raw)) || actual.Size() != after.Size() || !actual.ModTime().Equal(after.ModTime()) || len(raw) > maximum {
		return nil, ErrProtocol
	}
	return raw, nil
}

func names(r *os.Root, maximum int) ([]string, error) {
	f, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(maximum + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maximum {
		return nil, ErrProtocol
	}
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.Name()
	}
	return result, nil
}

// These publication lanes are single-writer and guest-read-only. Checking for an
// existing destination and renaming cannot race another authorized producer.
func publish(r *os.Root, temp, final string, raw []byte, replace bool, gid int) (err error) {
	if !replace {
		if _, e := r.Lstat(final); !errors.Is(e, os.ErrNotExist) {
			if e != nil {
				return e
			}
			return ErrProtocol
		}
	}
	f, err := r.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = r.Remove(temp)
		}
	}()
	if err = f.Chown(-1, gid); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0640); err != nil {
		f.Close()
		return err
	}
	n, err := f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	if closeErr != nil {
		return closeErr
	}
	return r.Rename(temp, final)
}

func guestGroup(c Config) (int, error) {
	if c.GuestGID == nil {
		return os.Getgid(), nil
	}
	if *c.GuestGID < 0 {
		return 0, ErrProtocol
	}
	return *c.GuestGID, nil
}
