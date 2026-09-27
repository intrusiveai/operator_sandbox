//go:build linux || darwin

package staging

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// CaptureInventory captures exactly the declared direct regular-file children of
// a directory, with bounded enumeration and bytes. It rejects extra entries,
// links, special files and files on another device before returning any content.
func CaptureInventory(ctx context.Context, directory string, sizes map[string]int64) (map[string][]byte, error) {
	if !absolute(directory) || len(sizes) > 4096 {
		return nil, ErrInputs
	}
	total := int64(0)
	for name, size := range sizes {
		if name == "." || name == ".." || filepath.Base(name) != name || size < 0 || size > 64<<20 {
			return nil, ErrInputs
		}
		total += size
		if total > 64<<20 {
			return nil, ErrInputs
		}
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
	dir, err := root.Open(".")
	if err != nil {
		return nil, ErrFilesystem
	}
	defer dir.Close()
	before, err := dir.Stat()
	if err != nil || !os.SameFile(initial, before) {
		return nil, ErrFilesystem
	}
	dev := before.Sys().(*syscall.Stat_t).Dev
	result := map[string][]byte{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		names, readErr := dir.Readdirnames(128)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, ErrFilesystem
		}
		for _, name := range names {
			size, ok := sizes[name]
			if !ok {
				return nil, ErrInputs
			}
			if _, seen := result[name]; seen {
				return nil, ErrFilesystem
			}
			info, err := root.Lstat(name)
			if err != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Dev != dev {
				return nil, ErrFilesystem
			}
			raw, err := read(ctx, root, name, size, nil)
			if err != nil || int64(len(raw)) != size {
				return nil, ErrFilesystem
			}
			after, err := root.Lstat(name)
			if err != nil || !os.SameFile(info, after) {
				return nil, ErrFilesystem
			}
			result[name] = raw
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	after, err := dir.Stat()
	if err != nil || !before.ModTime().Equal(after.ModTime()) || len(result) != len(sizes) {
		return nil, ErrFilesystem
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(initial, current) {
		return nil, ErrFilesystem
	}
	return result, nil
}
