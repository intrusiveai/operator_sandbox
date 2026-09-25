//go:build linux || darwin

package interceptor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"
)

const maxEvidenceBytes int64 = 9007199254740991

// Evidence errors concern collection, not execution uncertainty or permission to
// replay a target operation. Messages contain no server bodies or host paths.
type EvidenceError struct{ Kind string }

func (e *EvidenceError) Error() string { return "Interceptor evidence collection failed: " + e.Kind }
func evidenceError(kind string) error  { return &EvidenceError{Kind: kind} }

// EvidenceRequest contains trusted host settings and a persisted session binding.
// Its limits/deadline are local acceptance policy, not fields sent to Interceptor.
type EvidenceRequest struct {
	CampaignID, SessionID                string
	MaxArchiveBytes, InterceptorMaxBytes int64
	Deadline                             time.Time
}

// EvidenceReceipt proves transfer integrity only. Native archive identity,
// provenance, journal hashes and completeness still require validation.
type EvidenceReceipt struct {
	CampaignID          string `json:"campaign_id"`
	SessionID           string `json:"session_id"`
	Bytes               int64  `json:"bytes"`
	SHA256              string `json:"sha256"`
	LocalMaxBytes       int64  `json:"local_max_bytes"`
	InterceptorMaxBytes int64  `json:"interceptor_max_bytes"`
}

// EvidenceDownload owns uncommitted, host-only temporary bytes. Close after all
// readers finish; it removes the file on success as well as failure. No pathname
// or writable file descriptor is exposed. It must not be shared with the guest.
type EvidenceDownload struct {
	root     *os.Root
	file     *os.File
	name     string
	receipt  EvidenceReceipt
	closed   bool
	closeErr error
}

func (d *EvidenceDownload) Receipt() EvidenceReceipt { return d.receipt }
func (d *EvidenceDownload) Reader() (*io.SectionReader, error) {
	if d == nil || d.closed || d.file == nil {
		return nil, evidenceError("download_closed")
	}
	return io.NewSectionReader(evidenceReadAt{d.file}, 0, d.receipt.Bytes), nil
}

// SectionReader.Outer must not reveal a writable *os.File to its caller.
type evidenceReadAt struct{ file *os.File }

func (r evidenceReadAt) ReadAt(p []byte, offset int64) (int, error) { return r.file.ReadAt(p, offset) }
func (d *EvidenceDownload) Close() error {
	if d == nil {
		return nil
	}
	if d.closed {
		return d.closeErr
	}
	d.closed = true
	failed := false
	if d.file != nil && d.file.Close() != nil {
		failed = true
	}
	if d.root != nil {
		if d.name != "" {
			if err := d.root.Remove(d.name); err != nil && !os.IsNotExist(err) {
				failed = true
			}
		}
		if d.root.Close() != nil {
			failed = true
		}
	}
	if failed {
		d.closeErr = evidenceError("temporary_cleanup_failed")
	}
	return d.closeErr
}

func evidenceRoot(directory string) (*os.Root, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, evidenceError("private_directory_required")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return nil, evidenceError("private_directory_required")
	}
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, evidenceError("temporary_storage_unavailable")
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, evidenceError("temporary_storage_unavailable")
	}
	actual, err := f.Stat()
	f.Close()
	if err != nil || !os.SameFile(info, actual) {
		r.Close()
		return nil, evidenceError("temporary_storage_unavailable")
	}
	return r, nil
}

func singleHeader(h http.Header, name string) (string, bool) {
	values := h.Values(name)
	return h.Get(name), len(values) == 1 && values[0] != ""
}
func evidenceInteger(s string) (int64, bool) {
	if s == "" || s[0] == '0' {
		return 0, false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0 && n <= maxEvidenceBytes
}

// DownloadEvidence performs exactly one POST to the fixed local evidence route.
// It accepts the smaller pinned ceiling and requires the native advertised limit
// to match. The explicit deadline includes export generation and byte transfer;
// the caller selects it from host policy (not a guest control-frame timeout).
func (c *Client) DownloadEvidence(ctx context.Context, q EvidenceRequest, directory string) (download *EvidenceDownload, err error) {
	if c == nil || c.http == nil || !identifier.MatchString(q.CampaignID) || !identifier.MatchString(q.SessionID) || q.MaxArchiveBytes <= 0 || q.MaxArchiveBytes > maxEvidenceBytes || q.InterceptorMaxBytes <= 0 || q.InterceptorMaxBytes > maxEvidenceBytes || q.Deadline.IsZero() {
		return nil, ErrRequest
	}
	ctx, cancel := context.WithDeadline(ctx, q.Deadline)
	defer cancel()
	if ctx.Err() != nil {
		return nil, evidenceError("canceled_before_dispatch")
	}
	r, err := evidenceRoot(directory)
	if err != nil {
		return nil, err
	}
	d := &EvidenceDownload{root: r}
	defer func() {
		if download == nil {
			if cleanupErr := d.Close(); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	raw, _ := json.Marshal(map[string]string{"api_version": LifecycleVersion, "campaign_id": q.CampaignID, "session_id": q.SessionID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, Address+"/v1/evidence", io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		return nil, ErrRequest
	}
	request.ContentLength = int64(len(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/x-tar, application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, evidenceError("transport_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		result, e := readJSONResponse(ctx, response, JSONLimit)
		if e != nil {
			return nil, evidenceError("invalid_response")
		}
		return nil, &RemoteError{result}
	}
	media, ok := singleHeader(response.Header, "Content-Type")
	parsed, params, e := mime.ParseMediaType(media)
	if !ok || e != nil || parsed != "application/x-tar" || len(params) != 0 || response.Uncompressed || len(response.TransferEncoding) != 0 || len(response.Trailer) != 0 || response.Header.Get("Content-Range") != "" {
		return nil, evidenceError("invalid_headers")
	}
	if enc := response.Header.Values("Content-Encoding"); len(enc) > 1 || len(enc) == 1 && enc[0] != "identity" {
		return nil, evidenceError("invalid_headers")
	}
	lengthHeader, lengthOK := singleHeader(response.Header, "Content-Length")
	length, lengthOKValue := evidenceInteger(lengthHeader)
	maximumHeader, maximumOK := singleHeader(response.Header, "X-Evidence-Max-Bytes")
	maximum, maximumOKValue := evidenceInteger(maximumHeader)
	hash, hashOK := singleHeader(response.Header, "X-Content-SHA256")
	if !lengthOK || !lengthOKValue || response.ContentLength != length || !maximumOK || !maximumOKValue || maximum != q.InterceptorMaxBytes || !hashOK || !digest.MatchString(hash) {
		return nil, evidenceError("invalid_headers")
	}
	if length > min(q.MaxArchiveBytes, q.InterceptorMaxBytes) {
		return nil, evidenceError("archive_limit_exceeded")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, evidenceError("temporary_storage_unavailable")
	}
	name := "evidence-" + hex.EncodeToString(nonce[:]) + ".pending"
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, evidenceError("temporary_storage_unavailable")
	}
	d.name, d.file = name, f
	if err := receiveEvidence(ctx, f, response.Body, length, hash); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, evidenceError("temporary_storage_failed")
	}
	if ctx.Err() != nil {
		return nil, evidenceError("transfer_interrupted")
	}
	d.receipt = EvidenceReceipt{q.CampaignID, q.SessionID, length, hash, q.MaxArchiveBytes, q.InterceptorMaxBytes}
	return d, nil
}

type evidenceReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r evidenceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func receiveEvidence(ctx context.Context, dst io.Writer, src io.Reader, size int64, hash string) error {
	h := sha256.New()
	source := evidenceReader{ctx, src}
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), io.LimitReader(source, size), make([]byte, 64<<10))
	if err != nil || n != size || ctx.Err() != nil {
		return evidenceError("transfer_interrupted")
	}
	// Probe without writing an extra byte to disk, even at the local ceiling.
	var extra [1]byte
	next, err := io.ReadFull(source, extra[:])
	if next != 0 || err != io.EOF {
		return evidenceError("length_mismatch")
	}
	if "sha256:"+hex.EncodeToString(h.Sum(nil)) != hash {
		return evidenceError("digest_mismatch")
	}
	return nil
}
