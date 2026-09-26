//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

func requirements() Requirements {
	return Requirements{"1.10.0", contracts.PackageIdentity{Version: "0.1.0", Digest: contracts.RawDigest([]byte("package"))}, "darwin/arm64", "operator-container/v1"}
}
func release() Record {
	h := requirements()
	return Record{"intrusive.ai/engine-release/v1alpha1", contracts.RawDigest([]byte("image")), "1.9.0", h.Contract.Version, h.Contract.Digest, h.RuntimeProfile, "linux/arm64"}
}
func wire(r Record) []byte { raw, _ := json.Marshal(r); return raw }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(raw []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}
}
func client(t *testing.T, rt roundTrip) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.http.Transport = rt
	c.now = func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) }
	return c, dir
}

func TestStableSemVerPrecedence(t *testing.T) {
	for _, v := range []struct {
		actual, minimum string
		ok              bool
	}{
		{"1.10.0", "1.9.0", true}, {"1.9.0", "1.10.0", false}, {"2.0.0", "1.999.999", true},
		{"1.0.0+host.01", "1.0.0+release.02", true}, {"1.0.0-rc.1", "0.1.0", false},
		{"2.0.0", "1.0.0-rc.1", false}, {"01.0.0", "1.0.0", false}, {"1.0", "1.0.0", false},
		{"9999999999999999999999.0.0", "9999999999999999999998.0.0", true},
		{"1.0.0+", "1.0.0", false}, {"1.0.0+bad+meta", "1.0.0", false},
	} {
		if got := atLeast(v.actual, v.minimum); got != v.ok {
			t.Fatal(v, got)
		}
	}
	for _, s := range []string{"0.1.0-alpha.1+build.01", "0.1.0+001", "0.1.0-x-y-z.--"} {
		if _, _, ok := version(s); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"0.1.0-01", "0.1.0-", "0.1.0-alpha..1", "v0.1.0", "0.1.0\n"} {
		if _, _, ok := version(s); ok {
			t.Fatal(s)
		}
	}
}

func TestStrictRecordAndCompatibility(t *testing.T) {
	r := release()
	raw := wire(r)
	for _, bad := range [][]byte{
		append([]byte(`{"platform":"linux/amd64",`), raw[1:]...), append(raw, []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"platform":"linux/arm64"`), []byte(`"platform":null`), 1),
		append([]byte(`{"extra":false,`), raw[1:]...), []byte(`[]`), []byte("{\xff}"),
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatal("accepted invalid record", string(bad))
		}
	}
	for _, alter := range []func(*Record){
		func(r *Record) { r.MinimumOperatorVersion = "2.0.0" }, func(r *Record) { r.ContractPackageDigest = contracts.RawDigest([]byte("other")) },
		func(r *Record) { r.ContractPackageVersion = "0.1.1" }, func(r *Record) { r.Platform = "linux/amd64" },
		func(r *Record) { r.ImageDigest = contracts.RawDigest([]byte("other")) }, func(r *Record) { r.RuntimeProfile = "other" },
	} {
		r := release()
		alter(&r)
		if err := r.Compatible(release().ImageDigest, requirements()); err == nil {
			t.Fatal(r)
		}
	}
	for _, alter := range []func(*Record){func(r *Record) { r.MinimumOperatorVersion = "1.0.0-rc.1" }, func(r *Record) { r.Platform = "darwin/arm64" }, func(r *Record) { r.ContractPackageVersion = "0.1.0-01" }} {
		r := release()
		alter(&r)
		if _, err := Parse(wire(r)); err == nil {
			t.Fatal(r)
		}
	}
}

func TestHTTPSCacheAndCurrentCompatibility(t *testing.T) {
	var calls atomic.Int32
	raw := append(wire(release()), '\n')
	c, _ := client(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != Origin+"/sha256/"+strings.TrimPrefix(release().ImageDigest, "sha256:") || r.Method != "GET" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("Authorization") != "" {
			t.Fatal(r)
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > LookupTimeout {
			t.Fatal("unbounded lookup")
		}
		return response(raw), nil
	})
	a, err := c.Resolve(context.Background(), release().ImageDigest, requirements())
	if err != nil || a.Source() != "network" || !bytes.Equal(a.Bytes(), raw) || a.Digest() != contracts.RawDigest(raw) {
		t.Fatal(a, err)
	}
	copy := a.Bytes()
	copy[0] = '!'
	if a.Bytes()[0] != '{' {
		t.Fatal("approval bytes mutable")
	}
	c.http.Transport = roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("cache contacted network"); return nil, ErrLookup })
	a, err = c.Resolve(context.Background(), release().ImageDigest, requirements())
	if err != nil || a.Source() != "cache" || calls.Load() != 1 {
		t.Fatal(a, err)
	}
	h := requirements()
	h.OperatorVersion = "1.0.0"
	if _, err := c.Resolve(context.Background(), release().ImageDigest, h); !errors.Is(err, ErrCompatibility) {
		t.Fatal(err)
	}
	h = requirements()
	h.Contract.Digest = contracts.RawDigest([]byte("new installed contract"))
	if _, err := c.Resolve(context.Background(), release().ImageDigest, h); !errors.Is(err, ErrCompatibility) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Resolve(ctx, release().ImageDigest, requirements()); err == nil {
		t.Fatal("canceled preparation succeeded")
	}
}

func TestHTTPFailuresNeverPopulateCache(t *testing.T) {
	for _, kind := range []string{"404", "500", "redirect", "gzip", "multiple encodings", "wrong type", "duplicate type", "wrong charset", "oversize declared", "oversize actual", "invalid record", "wrong image", "unmet minimum", "network", "canceled body"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			c, dir := client(t, func(r *http.Request) (*http.Response, error) {
				calls++
				out := response(wire(release()))
				switch kind {
				case "404":
					out.StatusCode = 404
				case "500":
					out.StatusCode = 500
				case "redirect":
					out.StatusCode = 302
					out.Header.Set("Location", "https://elsewhere.example/release")
				case "gzip":
					out.Header.Set("Content-Encoding", "gzip")
				case "multiple encodings":
					out.Header["Content-Encoding"] = []string{"identity", "identity"}
				case "wrong type":
					out.Header.Set("Content-Type", "text/plain")
				case "duplicate type":
					out.Header.Add("Content-Type", "application/json")
				case "wrong charset":
					out.Header.Set("Content-Type", "application/json; charset=latin1")
				case "oversize declared":
					out.ContentLength = ResponseLimit + 1
				case "oversize actual":
					out.ContentLength = -1
					out.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", ResponseLimit+1)))
				case "invalid record":
					out = response([]byte(`{"unexpected":true}`))
				case "wrong image":
					other := release()
					other.ImageDigest = contracts.RawDigest([]byte("other"))
					out = response(wire(other))
				case "unmet minimum":
					other := release()
					other.MinimumOperatorVersion = "9.0.0"
					out = response(wire(other))
				case "network":
					return nil, errors.New("network unavailable")
				case "canceled body":
					out.Body = io.NopCloser(errorReader{})
				}
				return out, nil
			})
			_, err := c.Resolve(context.Background(), release().ImageDigest, requirements())
			if err == nil || calls != 1 {
				t.Fatal(err, calls)
			}
			if kind == "404" && !errors.Is(err, ErrUnapproved) {
				t.Fatal(err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatal("failed response cached", files, err)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }

func TestResponseLimitAndDeadline(t *testing.T) {
	raw := wire(release())
	raw = append(raw, bytes.Repeat([]byte(" "), ResponseLimit-len(raw))...)
	c, _ := client(t, func(*http.Request) (*http.Response, error) { return response(raw), nil })
	if a, err := c.Resolve(context.Background(), release().ImageDigest, requirements()); err != nil || len(a.Bytes()) != ResponseLimit {
		t.Fatal(err)
	}
	d, _ := client(t, func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := d.Resolve(ctx, release().ImageDigest, requirements()); !errors.Is(err, ErrLookup) {
		t.Fatal(err)
	}
}

func TestCacheCorruptionRequiresValidatedRefetch(t *testing.T) {
	for _, kind := range []string{"digest", "origin", "image", "time", "unknown field", "symlink", "hardlink", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			c, dir := client(t, func(*http.Request) (*http.Response, error) { calls++; return response(wire(release())), nil })
			if _, err := c.Resolve(context.Background(), release().ImageDigest, requirements()); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, cacheName(release().ImageDigest))
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "digest":
				m["response_digest"] = contracts.RawDigest([]byte("wrong"))
			case "origin":
				m["origin"] = "https://untrusted.example"
			case "image":
				m["image_id"] = contracts.RawDigest([]byte("other"))
			case "time":
				m["retrieved_at"] = "bad"
			case "unknown field":
				m["extra"] = true
			}
			raw, _ = json.Marshal(m)
			outside := filepath.Join(t.TempDir(), "outside")
			if kind == "symlink" || kind == "hardlink" {
				if err := os.WriteFile(outside, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					err = os.Symlink(outside, p)
				} else {
					err = os.Link(outside, p)
				}
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "oversize" {
					raw = bytes.Repeat([]byte("x"), cacheLimit+1)
				}
				if err := os.WriteFile(p, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			a, err := c.Resolve(context.Background(), release().ImageDigest, requirements())
			if err != nil || calls != 2 || a.Source() != "network" {
				t.Fatal(calls, a, err)
			}
			if kind == "symlink" || kind == "hardlink" {
				kept, _ := os.ReadFile(outside)
				if !bytes.Equal(kept, raw) {
					t.Fatal("changed outside link target")
				}
			}
		})
	}
}

func TestConcurrentCachePublication(t *testing.T) {
	c, dir := client(t, func(*http.Request) (*http.Response, error) { return response(wire(release())), nil })
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if _, err := c.Resolve(context.Background(), release().ImageDigest, requirements()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if _, _, err := c.read(release().ImageDigest); err != nil {
		t.Fatal(err)
	}
}

func TestUnusableCacheAndFailedPublicationCannotApprove(t *testing.T) {
	t.Run("corrupt cache and offline", func(t *testing.T) {
		c, dir := client(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
		if err := os.WriteFile(filepath.Join(dir, cacheName(release().ImageDigest)), []byte("damaged"), 0600); err != nil {
			t.Fatal(err)
		}
		if a, err := c.Resolve(context.Background(), release().ImageDigest, requirements()); err == nil || len(a.Bytes()) != 0 {
			t.Fatal(a, err)
		}
	})
	t.Run("publication obstructed", func(t *testing.T) {
		c, dir := client(t, func(*http.Request) (*http.Response, error) { return response(wire(release())), nil })
		// A directory at the cache-file path cannot be replaced by a file rename.
		if err := os.Mkdir(filepath.Join(dir, cacheName(release().ImageDigest)), 0700); err != nil {
			t.Fatal(err)
		}
		if a, err := c.Resolve(context.Background(), release().ImageDigest, requirements()); !errors.Is(err, ErrCache) || len(a.Bytes()) != 0 {
			t.Fatal(a, err)
		}
		files, err := os.ReadDir(dir)
		if err != nil || len(files) != 1 {
			t.Fatal("temporary file leaked", files, err)
		}
	})
	t.Run("nonprivate root", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if c, err := Open(dir); err == nil {
			c.Close()
			t.Fatal("nonprivate cache accepted")
		}
	})
}

type resolverFunc func(context.Context, string, string, string) (dockercontrol.ImagePin, error)

func (f resolverFunc) ResolveImage(ctx context.Context, e, s, h string) (dockercontrol.ImagePin, error) {
	return f(ctx, e, s, h)
}

func TestPreparationRequiresLocalImageEvenWithCachedApproval(t *testing.T) {
	c, _ := client(t, func(*http.Request) (*http.Response, error) { return response(wire(release())), nil })
	if _, err := c.Resolve(context.Background(), release().ImageDigest, requirements()); err != nil {
		t.Fatal(err)
	}
	missing := resolverFunc(func(context.Context, string, string, string) (dockercontrol.ImagePin, error) {
		return dockercontrol.ImagePin{}, dockercontrol.ErrImage
	})
	if _, err := c.Prepare(context.Background(), missing, "unix:///saved/docker.sock", "image", requirements()); !errors.Is(err, dockercontrol.ErrImage) {
		t.Fatal(err)
	}
	local := resolverFunc(func(ctx context.Context, e, s, h string) (dockercontrol.ImagePin, error) {
		return dockercontrol.ImagePin{Selector: s, Endpoint: e, DaemonID: "daemon-1", ImageID: release().ImageDigest, HostPlatform: h, ImagePlatform: "linux/arm64"}, nil
	})
	p, err := c.Prepare(context.Background(), local, "unix:///saved/docker.sock", "image", requirements())
	if err != nil || p.Release.Source() != "cache" || p.Image.ImageID != p.Release.Record().ImageDigest {
		t.Fatal(p, err)
	}
}
