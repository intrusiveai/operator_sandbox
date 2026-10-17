//go:build linux || darwin

package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

const diagnosticRequestID = "req_0123456789abcdef0123456789abcdef"

func fixtureClient(t *testing.T, server *httptest.Server, trust bool) *Client {
	t.Helper()
	c, err := New(context.Background(), profile(t, "openai-chat", server.URL), &secretResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if trust {
		c.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	t.Cleanup(c.Close)
	return c
}

func TestDiagnosticHTTPRejections(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500, 503} {
		for _, shape := range []string{"json", "html", "malformed", "oversized", "interrupted", "private-code", "private-header", "duplicate"} {
			t.Run(fmt.Sprintf("%d/%s", status, shape), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Request-Id", diagnosticRequestID)
					w.Header().Set("Authorization", "Bearer synthetic-header-secret")
					body := `{"error":{"code":"invalid_api_key","message":"synthetic-test-key synthetic-body-secret"}}`
					switch shape {
					case "html":
						w.Header().Set("Content-Type", "text/html")
						body = "synthetic-body-secret"
					case "malformed":
						body = `{"error":"synthetic-body-secret"`
					case "oversized":
						body = `{"error":{"code":"invalid_api_key","message":"` + strings.Repeat("x", 2048) + `"}}`
					case "interrupted":
						w.Header().Set("Content-Length", "900")
					case "private-code":
						body = `{"error":{"code":"synthetic-body-secret"}}`
					case "private-header":
						w.Header().Set("X-Request-Id", "synthetic-test-key")
					case "duplicate":
						body = `{"error":{"code":"invalid_api_key","code":"synthetic-body-secret"}}`
					}
					w.WriteHeader(status)
					fmt.Fprint(w, body)
				}))
				defer server.Close()
				c := fixtureClient(t, server, true)
				out, d, err := c.GenerateWithMetadata(context.Background(), nativeRequest(c.profile.Codec))
				if len(out) != 0 || !errors.Is(err, ErrProvider) || Details(err) == nil || !d.Valid() || d.Category != "http_rejection" || d.HTTPStatus != status || d.Provider != "openai-chat" || d.Model != "test-model" || calls.Load() != 1 {
					t.Fatalf("%+v %v calls=%d", d, err, calls.Load())
				}
				wantCode := ""
				if shape == "json" || shape == "private-header" {
					wantCode = "invalid_api_key"
				}
				if d.ProviderCode != wantCode {
					t.Fatalf("unexpected code: %+v", d)
				}
				wantID := diagnosticRequestID
				if shape == "private-header" {
					wantID = ""
				}
				if d.RequestID != wantID {
					t.Fatalf("unexpected request ID: %+v", d)
				}
				raw, _ := json.Marshal(d)
				for _, secret := range []string{"synthetic-test-key", "synthetic-header-secret", "synthetic-body-secret"} {
					if strings.Contains(string(raw)+err.Error(), secret) {
						t.Fatal("private data retained")
					}
				}
			})
		}
	}
}

func TestDiagnosticResponseReadingAndValidation(t *testing.T) {
	for _, mode := range []string{"malformed", "oversized", "chunked-oversized", "interrupted", "success"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", diagnosticRequestID)
				switch mode {
				case "malformed":
					fmt.Fprint(w, `{"private":"synthetic-body-secret"`)
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", 2048))
				case "chunked-oversized":
					w.(http.Flusher).Flush()
					fmt.Fprint(w, strings.Repeat("x", 2048))
				case "interrupted":
					w.Header().Set("Content-Length", "900")
					fmt.Fprint(w, `{"private":"synthetic-body-secret"}`)
				default:
					fmt.Fprint(w, `{"ok":true}`)
				}
			}))
			defer server.Close()
			c := fixtureClient(t, server, true)
			body, d, err := c.GenerateWithMetadata(context.Background(), nativeRequest(c.profile.Codec))
			if d.HTTPStatus != 200 || d.RequestID != diagnosticRequestID || calls.Load() != 1 {
				t.Fatalf("missing HTTP facts %+v", d)
			}
			if mode == "success" {
				if err != nil || string(body) != `{"ok":true}` {
					t.Fatal(err, string(body))
				}
				return
			}
			reason := "invalid_json"
			category := "response_validation"
			if strings.Contains(mode, "oversized") {
				reason = "response_too_large"
			}
			if mode == "interrupted" {
				reason = "read_failed"
				category = "response_read"
			}
			if err == nil || len(body) != 0 || d.Reason != reason || d.Category != category || !d.Valid() {
				t.Fatalf("%+v %v", d, err)
			}
		})
	}
}

type failingCredentials struct{ captured *context.Context }

func (f failingCredentials) Resolve(ctx context.Context, _ string) (credentials.Resolution, error) {
	if f.captured != nil {
		*f.captured = ctx
	}
	return credentials.Resolution{}, errors.New("synthetic-credential-secret")
}

func TestDiagnosticTransportAndCredentials(t *testing.T) {
	for _, mode := range []string{"connection", "tls", "timeout", "cancellation", "credential_preparation"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				close(entered)
				<-release
			}))
			defer server.Close()
			defer close(release)
			c := fixtureClient(t, server, mode != "tls")
			if mode == "connection" {
				server.Close()
			}
			if mode == "credential_preparation" {
				c.resolver = failingCredentials{}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			if mode == "cancellation" {
				go func() { <-entered; cancel() }()
			}
			_, d, err := c.GenerateWithMetadata(ctx, nativeRequest(c.profile.Codec))
			if err == nil || !d.Valid() || d.Category != mode || d.HTTPStatus != 0 || d.RequestID != "" {
				t.Fatalf("%+v %v", d, err)
			}
			raw, _ := json.Marshal(d)
			if strings.Contains(string(raw)+err.Error(), "synthetic-credential-secret") {
				t.Fatal("secret retained")
			}
			if calls.Load() > 1 {
				t.Fatal("replayed")
			}
		})
	}
}

func TestDiagnosticTLSProtocolFailure(t *testing.T) {
	// A plaintext HTTP endpoint reached through an HTTPS URL must be a TLS failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	c, err := New(context.Background(), profile(t, "openai-chat", strings.Replace(server.URL, "http:", "https:", 1)), &secretResolver{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Generate(context.Background(), nativeRequest(c.profile.Codec))
	if d := Details(err); d == nil || d.Category != "tls" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDiagnosticRejectsUnknownFieldsAndUnsafeIDs(t *testing.T) {
	d := Diagnostic{Provider: "openai-chat", Model: "test-model"}.WithFailure("connection", "no_response")
	raw, _ := json.Marshal(d)
	if DecodeDiagnostic(raw) == nil {
		t.Fatal("valid diagnostic rejected")
	}
	raw = append(raw[:len(raw)-1], []byte(`,"authorization":"synthetic-secret"}`)...)
	if DecodeDiagnostic(raw) != nil {
		t.Fatal("unknown fields accepted")
	}
	d.RequestID = "synthetic-secret"
	if d.Valid() {
		t.Fatal("unsafe ID accepted")
	}
	// Context and network wrappers are classified without retaining their strings.
	if failureCategory(context.Background(), &net.OpError{Op: "dial", Err: context.DeadlineExceeded}, "connection") != "timeout" {
		t.Fatal("timeout classification")
	}
}

func TestDiagnosticCanceledBeforeDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := New(context.Background(), profile(t, "openai-chat", "https://127.0.0.1:1"), &secretResolver{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Generate(ctx, nativeRequest(c.profile.Codec))
	d := Details(err)
	if d == nil || d.Category != "cancellation" || d.Reason != "canceled" || d.HTTPStatus != 0 {
		t.Fatal(d, err)
	}
}

func TestDiagnosticCredentialFailureReleasesRequestContext(t *testing.T) {
	var requestContext context.Context
	parent := context.Background()
	c, err := New(parent, profile(t, "openai-chat", "https://127.0.0.1:1"), failingCredentials{captured: &requestContext})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Generate(parent, nativeRequest(c.profile.Codec))
	d := Details(err)
	if d == nil || d.Category != "credential_preparation" || d.Reason != "credential_unavailable" {
		t.Fatalf("cleanup changed failure: %+v %v", d, err)
	}
	if requestContext == nil || !errors.Is(requestContext.Err(), context.Canceled) {
		t.Fatal("request context leaked")
	}
	if parent.Err() != nil {
		t.Fatal("parent context changed")
	}
}
