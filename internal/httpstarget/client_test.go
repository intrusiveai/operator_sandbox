//go:build linux || darwin

package httpstarget

import (
	"context"
	"encoding/pem"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type secretFunc func(context.Context, string) (credentials.Resolution, error)

func (f secretFunc) Resolve(c context.Context, id string) (credentials.Resolution, error) {
	return f(c, id)
}
func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	s := fixture()
	s.Origin = srv.URL
	s.AllowedPrivateCIDRs = []string{"127.0.0.1/32"}
	s.CACertificatesPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	c, e := New(parsed(t, s), nil, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	return c, srv
}
func TestTLSExecution(t *testing.T) {
	var count atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != "POST" || r.URL.Path != "/chat" || r.Header.Get("X-Test-Key") != "very-private" {
			t.Error("wrong request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":{"text":"done"},"private":"not-selected"}`))
	})
	s := c.mapping.Settings()
	s.Authentication = Authentication{"api-key", "test-key", "X-Test-Key"}
	c.mapping = parsed(t, s)
	c.secrets = secretFunc(func(_ context.Context, id string) (credentials.Resolution, error) {
		if id != "test-key" {
			t.Fatal(id)
		}
		return credentials.Resolution{Value: "very-private"}, nil
	})
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	out := c.Execute(context.Background(), "chat", []byte("input"), "text/plain")
	if out.Status != "completed" || string(out.Content) != "done" || out.Contact != "attempted" || count.Load() != 1 {
		t.Fatal(out)
	}
}
func TestTLSFailureAndRedirection(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	})
	out := c.Execute(context.Background(), "chat", []byte("x"), "text/plain")
	if out.Code != "HTTPS_REDIRECT_REJECTED" || redirected.Load() != 0 {
		t.Fatal(out)
	}
	s := c.mapping.Settings()
	s.CACertificatesPEM = ""
	untrusted, e := New(parsed(t, s), nil, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	out = untrusted.Execute(context.Background(), "chat", []byte("x"), "text/plain")
	if out.Contact != "none" || out.Code != "HTTPS_TRANSPORT_FAILED" {
		t.Fatal(out)
	}
}
func TestResponseLimitsAndUncertainty(t *testing.T) {
	for _, kind := range []string{"oversize", "disconnect", "timeout", "invalid", "status", "reflection"} {
		t.Run(kind, func(t *testing.T) {
			var count atomic.Int32
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				switch kind {
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat("x", 4097)))
				case "disconnect":
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
				case "timeout":
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
				case "invalid":
					_, _ = w.Write([]byte(`{"wrong":true}`))
				case "status":
					w.WriteHeader(500)
				case "reflection":
					_, _ = w.Write([]byte(`{"answer":{"text":"very-private"}}`))
				}
			})
			s := c.mapping.Settings()
			s.Authentication = Authentication{Mode: "bearer", CredentialID: "test"}
			c.mapping = parsed(t, s)
			c.secrets = secretFunc(func(context.Context, string) (credentials.Resolution, error) {
				return credentials.Resolution{Value: "very-private"}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			out := c.Execute(ctx, "chat", []byte("x"), "text/plain")
			if out.Status == "completed" || count.Load() != 1 {
				t.Fatal(out, count.Load())
			}
			if (kind == "disconnect" || kind == "timeout") && out.Status != "unknown" {
				t.Fatal("uncertainty lost", out)
			}
			if kind == "oversize" && !out.Truncated {
				t.Fatal(out)
			}
			if len(out.Content) != 0 {
				t.Fatal("failure leaked bytes")
			}
		})
	}
}
func TestDNSPinningAndMixedAnswers(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"answer":{"text":"ok"}}`)) })
	s := c.mapping.Settings()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	s.Origin = "https://example.com:" + port
	c.mapping = parsed(t, s)
	var dials atomic.Int32
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if !strings.HasPrefix(address, "127.0.0.1:") {
			t.Error("DNS result not pinned", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	c.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	if out := c.Execute(context.Background(), "chat", []byte("x"), "text/plain"); out.Contact != "none" || dials.Load() != 0 {
		t.Fatal(out)
	}
	c.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	// httptest's certificate includes example.com, preserving real hostname checks.
	out := c.Execute(context.Background(), "chat", []byte("x"), "text/plain")
	if out.Status != "completed" || dials.Load() != 1 {
		t.Fatal(out)
	}
}

func TestCredentialFailurePreventsContactAndDisclosure(t *testing.T) {
	var calls atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	s := c.mapping.Settings()
	s.Authentication = Authentication{Mode: "bearer", CredentialID: "credential"}
	c.mapping = parsed(t, s)
	for _, value := range []string{"", "secret\nInjected: header", strings.Repeat("x", 8193)} {
		c.secrets = secretFunc(func(context.Context, string) (credentials.Resolution, error) {
			return credentials.Resolution{Value: value}, nil
		})
		out := c.Execute(context.Background(), "chat", []byte("x"), "text/plain")
		if calls.Load() != 0 || out.Contact != "none" || len(out.Content) != 0 || !strings.HasPrefix(out.Code, "HTTPS_CREDENTIAL_") {
			t.Fatal(out)
		}
	}
}
func TestCanceledBeforeRequestDoesNotContactTarget(t *testing.T) {
	var calls atomic.Int32
	c, _ := testClient(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := c.Execute(ctx, "chat", []byte("x"), "text/plain")
	if out.Contact != "none" || out.Code != "HTTPS_CANCELED" || calls.Load() != 0 {
		t.Fatal(out)
	}
}
func TestInvalidCustomCA(t *testing.T) {
	s := fixture()
	s.CACertificatesPEM = "invalid certificate"
	if _, e := New(parsed(t, s), nil, time.Second); e == nil {
		t.Fatal("invalid CA accepted")
	}
}
