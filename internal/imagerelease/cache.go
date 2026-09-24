//go:build linux || darwin

package imagerelease

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

const cacheVersion = "operator.dev/image-release-cache/v1alpha1"
const cacheLimit = 128 << 10 // Includes base64 encoding of up to 64 KiB exact bytes.

type cacheEntry struct {
	Version        string `json:"api_version"`
	Origin         string `json:"origin"`
	ImageID        string `json:"image_id"`
	RetrievedAt    string `json:"retrieved_at"`
	Response       []byte `json:"response_base64"`
	ResponseDigest string `json:"response_digest"`
}

func openCache(dir string) (*os.Root, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrCache
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ErrCache
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, ErrCache
	}
	actual, err := f.Stat()
	f.Close()
	if err != nil || !os.SameFile(info, actual) {
		r.Close()
		return nil, ErrCache
	}
	return r, nil
}

func cacheName(imageID string) string {
	return strings.TrimPrefix(contracts.RawDigest([]byte(Origin)), "sha256:") + "-" + strings.TrimPrefix(imageID, "sha256:") + ".json"
}

func regular(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && ok && stat.Nlink <= 1
}

func (c *Client) read(imageID string) (cacheEntry, Record, error) {
	var entry cacheEntry
	var record Record
	name := cacheName(imageID)
	info, err := c.cache.Lstat(name)
	if err != nil || !regular(info) || info.Size() > cacheLimit {
		return entry, record, ErrCache
	}
	f, err := c.cache.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return entry, record, ErrCache
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !regular(before) || before.Size() > cacheLimit {
		return entry, record, ErrCache
	}
	raw, err := io.ReadAll(io.LimitReader(f, cacheLimit+1))
	if err != nil || len(raw) > cacheLimit {
		return entry, record, ErrCache
	}
	after, err := f.Stat()
	if err != nil || !regular(after) || before.Size() != after.Size() || after.Size() != int64(len(raw)) || !before.ModTime().Equal(after.ModTime()) {
		return entry, record, ErrCache
	}
	if strict(raw, &entry, cacheLimit) != nil || entry.Version != cacheVersion || entry.Origin != Origin || entry.ImageID != imageID || contracts.RawDigest(entry.Response) != entry.ResponseDigest {
		return entry, record, ErrCache
	}
	t, err := time.Parse(time.RFC3339Nano, entry.RetrievedAt)
	if err != nil || t.UTC().Format(time.RFC3339Nano) != entry.RetrievedAt {
		return entry, record, ErrCache
	}
	record, err = Parse(entry.Response)
	if err != nil || record.ImageDigest != imageID {
		return entry, record, ErrCache
	}
	return entry, record, nil
}

func (c *Client) write(entry cacheEntry) error {
	raw, err := json.Marshal(entry)
	if err != nil || len(raw) > cacheLimit {
		return ErrCache
	}
	// Unique temporary names allow concurrent preparations without partial reads.
	tmp := ".release-" + rand.Text() + ".tmp"
	f, err := c.cache.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrCache
	}
	defer c.cache.Remove(tmp)
	n, err := f.Write(raw)
	if err != nil || n != len(raw) {
		f.Close()
		return ErrCache
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return ErrCache
	}
	if err := f.Close(); err != nil {
		return ErrCache
	}
	if err := c.cache.Rename(tmp, cacheName(entry.ImageID)); err != nil {
		return ErrCache
	}
	dir, err := c.cache.Open(".")
	if err != nil {
		return ErrCache
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return ErrCache
	}
	return nil
}
