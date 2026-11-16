//go:build linux || darwin

package modelprovider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type deadlineTransport func(*http.Request) (*http.Response, error)

func (f deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelDeadlineUsesCallerBudget(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		allowance, wait, cancelAfter time.Duration
		source                       string
		success                      bool
	}{
		{"five-minute-operation", 300 * time.Second, 121 * time.Second, 0, "configured-operation", true},
		{"short-request", 10 * time.Second, 121 * time.Second, 0, "request", false},
		{"earlier-campaign", 20 * time.Second, 121 * time.Second, 0, "campaign", false},
		{"campaign-cancellation", 300 * time.Second, 121 * time.Second, 8 * time.Second, "configured-operation", false},
		{"explicit-no-deadline-default", 0, 121 * time.Second, 0, "host-default", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := profile(t, "openai-responses", "https://example.invalid")
				c, err := New(context.Background(), p, &secretResolver{})
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				if tc.allowance > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.allowance)
					defer cancel()
					ctx = WithDeadlineSource(ctx, tc.source)
				}
				if tc.cancelAfter > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					time.AfterFunc(tc.cancelAfter, cancel)
				}
				calls := 0
				c.http.Transport = deadlineTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					select {
					case <-r.Context().Done():
						return nil, r.Context().Err()
					case <-time.After(tc.wait):
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"provider_metadata":"accepted by transport"}`))}, nil
				})
				start := time.Now()
				body, diagnostic, _, err := c.GenerateWithEvidence(ctx, nativeRequest(p.Settings().Codec))
				if (err == nil) != tc.success || calls != 1 {
					t.Fatalf("body=%s error=%v diagnostic=%+v calls=%d", body, err, diagnostic, calls)
				}
				if diagnostic.DeadlineSource != tc.source {
					t.Fatalf("deadline source: %+v", diagnostic)
				}
				if tc.success && time.Since(start) != tc.wait {
					t.Fatal("request did not run past 120 seconds")
				}
				if !tc.success {
					want := "timeout"
					if tc.cancelAfter > 0 {
						want = "cancellation"
					}
					if !diagnostic.Valid() || diagnostic.Category != want || !strings.Contains(diagnostic.Summary(), "Attack Harness model request") {
						t.Fatalf("%+v", diagnostic)
					}
				}
			})
		})
	}
}
