//go:build linux || darwin

package httpstarget

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

type Resolver interface {
	Resolve(context.Context, string) (credentials.Resolution, error)
}
type Client struct {
	mapping *Mapping
	secrets Resolver
	roots   *x509.CertPool
	timeout time.Duration
	lookup  func(context.Context, string, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}
type Outcome struct {
	Status     string
	Contact    string
	Code       string
	HTTPStatus int
	Content    []byte
	MediaType  string
	Truncated  bool
}

func New(m *Mapping, secrets Resolver, timeout time.Duration) (*Client, error) {
	if m == nil || timeout <= 0 || timeout > 30*time.Second || m.settings.Authentication.Mode != "none" && secrets == nil {
		return nil, ErrMapping
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		return nil, ErrMapping
	}
	if pem := m.settings.CACertificatesPEM; pem != "" && !roots.AppendCertsFromPEM([]byte(pem)) {
		return nil, ErrMapping
	}
	return &Client{mapping: m, secrets: secrets, roots: roots, timeout: timeout, lookup: net.DefaultResolver.LookupNetIP, dial: (&net.Dialer{}).DialContext}, nil
}

type trackedConn struct {
	net.Conn
	sent *atomic.Bool
}

func (c trackedConn) Write(p []byte) (int, error) { c.sent.Store(true); return c.Conn.Write(p) }

// Execute sends at most one application request. Errors are fixed codes; the
// transport's raw errors and response headers never escape this boundary.
func (c *Client) Execute(ctx context.Context, id string, payload []byte, media string) Outcome {
	out := Outcome{Status: "failed", Contact: "none", Code: "HTTPS_INPUT_INVALID"}
	op, body, e := c.mapping.Request(id, payload, media)
	if e != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	token := ""
	auth := c.mapping.settings.Authentication
	if auth.Mode != "none" {
		resolved, e := c.secrets.Resolve(ctx, auth.CredentialID)
		if e != nil || len(resolved.Value) == 0 || len(resolved.Value) > 8192 {
			out.Code = "HTTPS_CREDENTIAL_FAILED"
			return out
		}
		token = resolved.Value
		for _, r := range token {
			if r < 33 || r > 126 {
				out.Code = "HTTPS_CREDENTIAL_INVALID"
				return out
			}
		}
	}
	endpoint := c.mapping.settings.Origin + op.Path
	req, e := http.NewRequestWithContext(ctx, op.Method, endpoint, bytes.NewReader(body))
	if e != nil {
		return out
	}
	req.GetBody = nil // No body replay, even for methods normally considered idempotent.
	req.Header.Set("Content-Type", map[string]string{"text": "text/plain; charset=utf-8", "json": "application/json"}[op.Input.Format])
	req.Header.Set("Accept", map[string]string{"text": "text/plain", "json": "application/json"}[op.Response.Format])
	req.Header.Set("Accept-Encoding", "identity")
	if auth.Mode == "bearer" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if auth.Mode == "api-key" {
		req.Header.Set(auth.Header, token)
	}
	u, _ := url.Parse(c.mapping.settings.Origin)
	var sent atomic.Bool
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 32 << 10,
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil || host != u.Hostname() {
				return nil, ErrMapping
			}
			addresses, e := c.lookup(ctx, "ip", host)
			if e != nil || len(addresses) == 0 || len(addresses) > 64 {
				return nil, ErrMapping
			}
			for _, a := range addresses {
				if !c.mapping.AllowsAddress(a) {
					return nil, ErrMapping
				}
			}
			conn, e := c.dial(ctx, "tcp", net.JoinHostPort(addresses[0].Unmap().String(), port))
			if e != nil {
				return nil, e
			}
			secure := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: c.roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
			if e = secure.HandshakeContext(ctx); e != nil {
				conn.Close()
				return nil, e
			}
			return trackedConn{secure, &sent}, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		out.Code = "HTTPS_TRANSPORT_FAILED"
		if sent.Load() {
			out.Status = "unknown"
			out.Contact = "unknown"
		}
		if ctx.Err() != nil {
			out.Code = "HTTPS_CANCELED"
		}
		return out
	}
	defer resp.Body.Close()
	out.Contact = "attempted"
	out.HTTPStatus = resp.StatusCode
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		out.Code = "HTTPS_REDIRECT_REJECTED"
		return out
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Code = "HTTPS_STATUS_FAILED"
		return out
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		out.Code = "HTTPS_ENCODING_UNSUPPORTED"
		return out
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, op.MaximumResponseBytes+1))
	if e != nil {
		out.Status = "unknown"
		out.Contact = "unknown"
		out.Code = "HTTPS_RESPONSE_INTERRUPTED"
		return out
	}
	if int64(len(raw)) > op.MaximumResponseBytes {
		out.Code = "HTTPS_RESPONSE_LIMIT"
		out.Truncated = true
		return out
	}
	raw, media, e = SelectResponse(op, raw)
	if e != nil {
		out.Code = "HTTPS_RESPONSE_INVALID"
		return out
	}
	// An application echoing the authentication value must not disclose it through
	// selected feedback. Refuse the whole output instead of changing its bytes.
	if token != "" && bytes.Contains(raw, []byte(token)) {
		out.Code = "HTTPS_CREDENTIAL_REFLECTED"
		return out
	}
	out.Status = "completed"
	out.Code = ""
	out.Content = raw
	out.MediaType = media
	return out
}
