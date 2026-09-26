//go:build linux || darwin

package interceptor

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func privateEvidenceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func evidenceQuery() EvidenceRequest {
	return EvidenceRequest{CampaignID: "campaign-1", SessionID: "sess-old", MaxArchiveBytes: 4 << 30, InterceptorMaxBytes: 4 << 30, Deadline: time.Now().Add(time.Minute)}
}
func evidenceResponse(data []byte) *http.Response {
	return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{
		"Content-Type": {"application/x-tar"}, "Content-Length": {strconv.Itoa(len(data))},
		"X-Content-Sha256": {rawDigest(data)}, "X-Evidence-Max-Bytes": {"4294967296"},
	}, Body: io.NopCloser(bytes.NewReader(data))}
}
func evidenceClient(f func(*http.Request) (*http.Response, error)) *Client {
	return &Client{http: &http.Client{Transport: roundTrip(f)}}
}
func assertEvidenceError(t *testing.T, err error, kind string) {
	t.Helper()
	var e *EvidenceError
	if !errors.As(err, &e) || kind != "" && e.Kind != kind {
		t.Fatalf("want evidence error %s, got %v", kind, err)
	}
	var call *CallError
	if errors.As(err, &call) {
		t.Fatal("collection failure classified as execution uncertainty")
	}
}
func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary files leaked", entries, err)
	}
}

func TestEvidenceDownloadPrivateLifetime(t *testing.T) {
	q, dir := evidenceQuery(), privateEvidenceDir(t)
	data := []byte("transport-verified bytes still require native archive verification")
	calls := 0
	c := evidenceClient(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != Address+"/v1/evidence" || r.Method != "POST" || r.GetBody != nil || r.Header.Get("Authorization") != "" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Fatal("unexpected request")
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		if json.Unmarshal(raw, &body) != nil || len(body) != 3 || body["campaign_id"] != q.CampaignID || body["session_id"] != q.SessionID || body["api_version"] != LifecycleVersion {
			t.Fatal(string(raw))
		}
		deadline, ok := r.Context().Deadline()
		if !ok || !deadline.Equal(q.Deadline) {
			t.Fatal("deadline changed")
		}
		return evidenceResponse(data), nil
	})
	d, err := c.DownloadEvidence(context.Background(), q, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if calls != 1 || d.Receipt().Bytes != int64(len(data)) || d.Receipt().SHA256 != rawDigest(data) || d.Receipt().SessionID != q.SessionID {
		t.Fatal(d.Receipt(), calls)
	}
	info, err := os.Stat(filepath.Join(dir, d.name))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	reader, err := d.Reader()
	if err != nil {
		t.Fatal(err)
	}
	underlying, _, _ := reader.Outer()
	if _, ok := underlying.(io.Writer); ok {
		t.Fatal("download exposes writable bytes")
	}
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal(string(got), err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, dir)
	_, err = d.Reader()
	assertEvidenceError(t, err, "download_closed")
}

func TestEvidenceRejectsHeadersBeforeWriting(t *testing.T) {
	for name, edit := range map[string]func(*http.Response){
		"missing length":   func(r *http.Response) { r.Header.Del("Content-Length"); r.ContentLength = -1 },
		"duplicate length": func(r *http.Response) { r.Header.Add("Content-Length", "4") },
		"wrong length":     func(r *http.Response) { r.ContentLength++ },
		"zero length":      func(r *http.Response) { r.Header.Set("Content-Length", "0"); r.ContentLength = 0 },
		"invalid integer":  func(r *http.Response) { r.Header.Set("Content-Length", "+4") },
		"missing digest":   func(r *http.Response) { r.Header.Del("X-Content-SHA256") },
		"duplicate digest": func(r *http.Response) { r.Header.Add("X-Content-SHA256", rawDigest([]byte("data"))) },
		"bad digest":       func(r *http.Response) { r.Header.Set("X-Content-SHA256", "bad") },
		"missing limit":    func(r *http.Response) { r.Header.Del("X-Evidence-Max-Bytes") },
		"changed limit":    func(r *http.Response) { r.Header.Set("X-Evidence-Max-Bytes", "4294967297") },
		"duplicate limit":  func(r *http.Response) { r.Header.Add("X-Evidence-Max-Bytes", "4294967296") },
		"gzip":             func(r *http.Response) { r.Header.Set("Content-Encoding", "gzip") },
		"decompressed":     func(r *http.Response) { r.Uncompressed = true },
		"chunked":          func(r *http.Response) { r.TransferEncoding = []string{"chunked"} },
		"partial":          func(r *http.Response) { r.Header.Set("Content-Range", "bytes 0-3/4") },
		"media":            func(r *http.Response) { r.Header.Set("Content-Type", "application/json") },
		"duplicate media":  func(r *http.Response) { r.Header.Add("Content-Type", "application/x-tar") },
	} {
		t.Run(name, func(t *testing.T) {
			dir := privateEvidenceDir(t)
			c := evidenceClient(func(*http.Request) (*http.Response, error) {
				r := evidenceResponse([]byte("data"))
				edit(r)
				return r, nil
			})
			d, err := c.DownloadEvidence(context.Background(), evidenceQuery(), dir)
			if d != nil {
				d.Close()
				t.Fatal("accepted invalid headers")
			}
			assertEvidenceError(t, err, "invalid_headers")
			assertEmpty(t, dir)
		})
	}
}

func TestEvidenceFailuresCleanUpAndPreserveCommittedData(t *testing.T) {
	for _, name := range []string{"short", "excess", "digest", "interrupted", "lost reply", "over local limit", "over native limit"} {
		t.Run(name, func(t *testing.T) {
			dir := privateEvidenceDir(t)
			if err := os.WriteFile(filepath.Join(dir, "committed.tar"), []byte("saved"), 0600); err != nil {
				t.Fatal(err)
			}
			q := evidenceQuery()
			if name == "over local limit" {
				q.MaxArchiveBytes = 3
			}
			if name == "over native limit" {
				q.InterceptorMaxBytes = 3
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			c := evidenceClient(func(*http.Request) (*http.Response, error) {
				calls++
				if name == "lost reply" {
					return nil, errors.New("private host path must not leak")
				}
				r := evidenceResponse([]byte("data"))
				switch name {
				case "short":
					r.Body = io.NopCloser(strings.NewReader("dat"))
				case "excess":
					r.Body = io.NopCloser(strings.NewReader("data!"))
				case "digest":
					r.Header.Set("X-Content-SHA256", rawDigest([]byte("other")))
				case "interrupted":
					r.Body = io.NopCloser(readerFunc(func(p []byte) (int, error) { cancel(); return copy(p, "data"), nil }))
				case "over native limit":
					r.Header.Set("X-Evidence-Max-Bytes", "3")
				}
				return r, nil
			})
			d, err := c.DownloadEvidence(ctx, q, dir)
			if d != nil {
				d.Close()
				t.Fatal("accepted bad transfer")
			}
			assertEvidenceError(t, err, "")
			if strings.Contains(err.Error(), "private host path") || calls != 1 {
				t.Fatal(err, calls)
			}
			entries, _ := os.ReadDir(dir)
			got, e := os.ReadFile(filepath.Join(dir, "committed.tar"))
			if e != nil || string(got) != "saved" || len(entries) != 1 {
				t.Fatal("committed file changed or partial file retained", entries, e)
			}
		})
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestEvidenceStreamingBoundAndWriterFailure(t *testing.T) {
	const size int64 = 6 << 20 // Larger than the JSON transport ceiling.
	var consumed int64
	h := sha256.New()
	_, _ = io.Copy(h, io.LimitReader(zeroReader{}, size))
	hash := "sha256:" + hex.EncodeToString(h.Sum(nil))
	c := evidenceClient(func(*http.Request) (*http.Response, error) {
		r := evidenceResponse([]byte("x"))
		r.ContentLength = size
		r.Header.Set("Content-Length", strconv.FormatInt(size, 10))
		r.Header.Set("X-Content-SHA256", hash)
		r.Body = io.NopCloser(readerFunc(func(p []byte) (int, error) {
			if len(p) > 64<<10 {
				t.Fatal("unbounded transfer buffer", len(p))
			}
			if consumed == size {
				return 0, io.EOF
			}
			n := int(min(int64(len(p)), size-consumed))
			clear(p[:n])
			consumed += int64(n)
			return n, nil
		}))
		return r, nil
	})
	dir := privateEvidenceDir(t)
	d, err := c.DownloadEvidence(context.Background(), evidenceQuery(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Receipt().Bytes != size || consumed != size {
		t.Fatal(d.Receipt(), consumed)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, dir)
	err = receiveEvidence(context.Background(), failingWriter{}, strings.NewReader("data"), 4, rawDigest([]byte("data")))
	assertEvidenceError(t, err, "transfer_interrupted")
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestEvidenceNativeErrorsAndLocalRefusal(t *testing.T) {
	q := evidenceQuery()
	dir := privateEvidenceDir(t)
	c := evidenceClient(func(*http.Request) (*http.Response, error) {
		return wireResponse(t, 413, map[string]any{"code": "evidence_limit_exceeded", "maximum_bytes": q.InterceptorMaxBytes, "evidence_retained": true}), nil
	})
	_, err := c.DownloadEvidence(context.Background(), q, dir)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Response.Status != 413 || remote.Response.Code() != "evidence_limit_exceeded" {
		t.Fatal(err)
	}
	var detail map[string]any
	_ = json.Unmarshal(remote.Response.Body, &detail)
	if detail["evidence_retained"] != true || detail["maximum_bytes"] != float64(q.InterceptorMaxBytes) {
		t.Fatal(detail)
	}
	assertEmpty(t, dir)
	c = evidenceClient(func(*http.Request) (*http.Response, error) { t.Fatal("local refusal dispatched"); return nil, nil })
	for _, change := range []func(*EvidenceRequest){func(q *EvidenceRequest) { q.CampaignID = "../bad" }, func(q *EvidenceRequest) { q.SessionID = "" }, func(q *EvidenceRequest) { q.MaxArchiveBytes = 0 }, func(q *EvidenceRequest) { q.InterceptorMaxBytes = maxEvidenceBytes + 1 }, func(q *EvidenceRequest) { q.Deadline = time.Time{} }} {
		input := q
		change(&input)
		if _, err := c.DownloadEvidence(context.Background(), input, dir); !errors.Is(err, ErrRequest) {
			t.Fatal(err)
		}
	}
	q.Deadline = time.Now().Add(-time.Second)
	_, err = c.DownloadEvidence(context.Background(), q, dir)
	assertEvidenceError(t, err, "canceled_before_dispatch")
	q = evidenceQuery()
	alias := filepath.Join(privateEvidenceDir(t), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	_, err = c.DownloadEvidence(context.Background(), q, alias)
	assertEvidenceError(t, err, "private_directory_required")
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = c.DownloadEvidence(context.Background(), q, dir)
	assertEvidenceError(t, err, "private_directory_required")
}

func TestEvidenceNativeServeContentHTTP(t *testing.T) {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "session.json", Mode: 0600, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/evidence" || r.Host != "127.0.0.1:8080" {
			t.Error("wrong endpoint")
		}
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("X-Content-SHA256", rawDigest(archive.Bytes()))
		w.Header().Set("X-Evidence-Max-Bytes", "4294967296")
		http.ServeContent(w, r, "evidence.tar", time.Time{}, bytes.NewReader(archive.Bytes()))
	}))
	defer srv.Close()
	c := New()
	defer c.Close()
	c.http.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", srv.Listener.Addr().String())
	}
	dir := privateEvidenceDir(t)
	d, err := c.DownloadEvidence(context.Background(), evidenceQuery(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Receipt().Bytes != int64(archive.Len()) {
		t.Fatal(d.Receipt())
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, dir)
}

func TestStageRetainedEvidenceBoundsAndCleanup(t *testing.T) {
	raw := []byte("retained archive bytes")
	receipt := EvidenceReceipt{"campaign-1", "sess-old", int64(len(raw)), rawDigest(raw), 1024, 1024}
	for _, mode := range []string{"success", "short", "excess", "digest", "capacity", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			dir := privateEvidenceDir(t)
			r := receipt
			data := bytes.Clone(raw)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "short":
				data = data[:len(data)-1]
			case "excess":
				data = append(data, 'x')
			case "digest":
				r.SHA256 = rawDigest(nil)
			case "capacity":
				r.LocalMaxBytes = 1
			case "cancelled":
				cancel()
			}
			d, err := StageEvidence(ctx, r, bytes.NewReader(data), dir)
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if err = d.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				d.Close()
				t.Fatal("invalid retained source accepted")
			}
			assertEmpty(t, dir)
		})
	}
}
