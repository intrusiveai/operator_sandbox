//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

const LookupTimeout = 10 * time.Second

type Client struct {
	cache *os.Root
	http  *http.Client
	now   func() time.Time
}

// Approval owns a validated byte snapshot; accessors return copies. It is not a
// signed artifact: trust comes from fixed-origin HTTPS or the private host cache.
type Approval struct {
	record                      Record
	raw                         []byte
	digest, source, retrievedAt string
}

func (a Approval) Record() Record      { return a.record }
func (a Approval) Bytes() []byte       { return bytes.Clone(a.raw) }
func (a Approval) Digest() string      { return a.digest }
func (a Approval) Source() string      { return a.source }
func (a Approval) RetrievedAt() string { return a.retrievedAt }

// Open uses an existing administrator-owned private directory. It never accepts
// alternate release origins, TLS roots, redirects or campaign-supplied clients.
func Open(cacheDirectory string) (*Client, error) {
	r, err := openCache(cacheDirectory)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: LookupTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: LookupTimeout,
		DisableCompression: true, MaxResponseHeaderBytes: 16 << 10, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}
	client := &http.Client{Transport: transport, Timeout: LookupTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrLookup }}
	return &Client{r, client, time.Now}, nil
}

// Close must follow completion of all preparation calls.
func (c *Client) Close() error { c.http.CloseIdleConnections(); return c.cache.Close() }

func approval(entry cacheEntry, source string, r Record) Approval {
	return Approval{r, bytes.Clone(entry.Response), entry.ResponseDigest, source, entry.RetrievedAt}
}

// Resolve always rechecks current compatibility, even for offline cache hits.
// A valid but incompatible record is a failure, not a reason to replace approval.
func (c *Client) Resolve(ctx context.Context, imageID string, h Requirements) (Approval, error) {
	if !digestPattern.MatchString(imageID) {
		return Approval{}, ErrRecord
	}
	if err := h.validate(); err != nil {
		return Approval{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, LookupTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Approval{}, ErrLookup
	}
	if entry, r, err := c.read(imageID); err == nil {
		if ctx.Err() != nil {
			return Approval{}, ErrLookup
		}
		if err := r.Compatible(imageID, h); err != nil {
			return Approval{}, err
		}
		return approval(entry, "cache", r), nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, Origin+"/sha256/"+strings.TrimPrefix(imageID, "sha256:"), nil)
	if err != nil {
		return Approval{}, ErrLookup
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := c.http.Do(request)
	if err != nil {
		return Approval{}, ErrLookup
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Approval{}, ErrUnapproved
	}
	if response.StatusCode != http.StatusOK || response.Uncompressed || response.ContentLength > ResponseLimit {
		return Approval{}, ErrLookup
	}
	encodings := response.Header.Values("Content-Encoding")
	if len(encodings) > 1 || (len(encodings) == 1 && !strings.EqualFold(strings.TrimSpace(encodings[0]), "identity")) {
		return Approval{}, ErrLookup
	}
	if len(response.Header.Values("Content-Type")) != 1 {
		return Approval{}, ErrLookup
	}
	media, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return Approval{}, ErrLookup
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, ResponseLimit+1))
	if err != nil || len(raw) > ResponseLimit || ctx.Err() != nil {
		return Approval{}, ErrLookup
	}
	r, err := Parse(raw)
	if err != nil {
		return Approval{}, err
	}
	if r.ImageDigest != imageID {
		return Approval{}, ErrRecord
	}
	if err := r.Compatible(imageID, h); err != nil {
		return Approval{}, err
	}
	entry := cacheEntry{Version: cacheVersion, Origin: Origin, ImageID: imageID, RetrievedAt: c.now().UTC().Format(time.RFC3339Nano), Response: raw, ResponseDigest: contracts.RawDigest(raw)}
	if err := c.write(entry); err != nil {
		return Approval{}, err
	}
	if ctx.Err() != nil {
		return Approval{}, ErrLookup
	}
	return approval(entry, "network", r), nil
}
