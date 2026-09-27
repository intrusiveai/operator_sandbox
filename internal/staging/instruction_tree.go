//go:build linux || darwin

package staging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// CaptureInstructionTree freezes a bounded data-only source tree. Callers must
// validate paths, media types and content before signing or publishing it. The
// selected directory's ancestors are trusted host paths; no source is mounted.
func CaptureInstructionTree(ctx context.Context, directory string) (map[string][]byte, error) {
	if !absolute(directory) {
		return nil, ErrInputs
	}
	initial, err := os.Lstat(directory)
	if err != nil || !initial.IsDir() {
		return nil, ErrFilesystem
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrFilesystem
	}
	defer root.Close()
	dev := initial.Sys().(*syscall.Stat_t).Dev
	stamps := map[string]os.FileInfo{}
	result := map[string][]byte{}
	total, entries := int64(0), 0
	var walk func(*os.Root, string, int) error
	walk = func(r *os.Root, prefix string, depth int) error {
		if depth > 16 {
			return ErrInputs
		}
		dir, err := r.Open(".")
		if err != nil {
			return ErrFilesystem
		}
		defer dir.Close()
		before, err := dir.Stat()
		if err != nil || !before.IsDir() || before.Sys().(*syscall.Stat_t).Dev != dev || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !instructionAttributes(dir) {
			return ErrFilesystem
		}
		if prefix == "" && !os.SameFile(initial, before) {
			return ErrFilesystem
		}
		stamps[path.Join(prefix, ".")] = before
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			names, e := dir.Readdirnames(128)
			if e != nil && !errors.Is(e, io.EOF) {
				return ErrFilesystem
			}
			for _, name := range names {
				entries++
				if entries > 2048 {
					return ErrInputs
				}
				full := path.Join(prefix, name)
				info, err := r.Lstat(name)
				if err != nil || info.Sys().(*syscall.Stat_t).Dev != dev {
					return ErrFilesystem
				}
				if info.IsDir() {
					child, err := r.OpenRoot(name)
					if err != nil {
						return ErrFilesystem
					}
					err = walk(child, full, depth+1)
					child.Close()
					if err != nil {
						return err
					}
					if !os.SameFile(info, stamps[full]) {
						return ErrFilesystem
					}
					continue
				}
				if !regular(info) || info.Mode().Perm()&0111 != 0 || len(result) >= 1024 || info.Size() > 1<<20 {
					return ErrInputs
				}
				total += info.Size()
				if total > 8<<20 {
					return ErrInputs
				}
				f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
				if err != nil {
					return ErrFilesystem
				}
				opened, statErr := f.Stat()
				ok := statErr == nil && os.SameFile(info, opened) && instructionAttributes(f)
				f.Close()
				if !ok {
					return ErrFilesystem
				}
				raw, err := read(ctx, r, name, 1<<20, nil)
				if err != nil {
					return err
				}
				result[full], stamps[full] = raw, info
			}
			if errors.Is(e, io.EOF) {
				break
			}
		}
		return nil
	}
	if err := walk(root, "", 0); err != nil {
		return nil, err
	}
	// Recheck earlier entries after capturing later ones, including directory
	// membership timestamps. Publication uses only the frozen bytes above.
	for name, before := range stamps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		after, err := root.Lstat(name)
		if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
			return nil, ErrFilesystem
		}
		if !after.IsDir() && !regular(after) {
			return nil, ErrFilesystem
		}
		f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, ErrFilesystem
		}
		opened, err := f.Stat()
		ok := err == nil && os.SameFile(before, opened) && instructionAttributes(f)
		f.Close()
		if !ok {
			return nil, ErrFilesystem
		}
		if !after.IsDir() {
			raw, err := read(ctx, root, name, 1<<20, nil)
			if err != nil || !bytes.Equal(raw, result[name]) {
				return nil, ErrFilesystem
			}
		}
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(initial, current) {
		return nil, ErrFilesystem
	}
	return result, nil
}

func instructionAttributes(f *os.File) bool {
	n, err := unix.Flistxattr(int(f.Fd()), nil)
	if err != nil || n > 64<<10 {
		return false
	}
	if n == 0 {
		return true
	}
	if runtime.GOOS != "darwin" {
		return false
	}
	names := make([]byte, n)
	n, err = unix.Flistxattr(int(f.Fd()), names)
	if err != nil {
		return false
	}
	// macOS attaches this inert provenance attribute automatically, including to
	// files Operator itself creates. No source metadata is copied to publication.
	for _, name := range strings.Split(string(names[:n]), "\x00") {
		if name != "" && name != "com.apple.provenance" {
			return false
		}
	}
	return true
}
