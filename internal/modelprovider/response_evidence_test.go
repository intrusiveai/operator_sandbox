package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateResponseRedactionPreservesRejectedStructure(t *testing.T) {
	raw := []byte(`{"n":1,"n":2,"escaped":"synthetic-\u0074est-key","api_key":{"nested":"unknown-private"},"text":"Bearer unlisted-token https://user:pass@example.invalid/path","normal":"auditable output"}`)
	got := privateResponse(raw, []string{"synthetic-test-key"})
	if got.Omission != "" || !got.Redacted || !json.Valid(got.Body) || !bytes.Contains(got.Body, []byte(`"n":1,"n":2`)) || !bytes.Contains(got.Body, []byte("auditable output")) {
		t.Fatal(string(got.Body), got.Omission)
	}
	for _, secret := range []string{"synthetic-", "unknown-private", "unlisted-token", "example.invalid", "user:pass"} {
		if bytes.Contains(got.Body, []byte(secret)) {
			t.Fatal("secret retained", secret)
		}
	}
	exact := []byte("{ \"arbitrary\": [1,true,null], \"text\": \"unaltered\" }")
	if x := privateResponse(exact, nil); x.Redacted || !bytes.Equal(x.Body, exact) {
		t.Fatal("altered evidence without need")
	}
	if x := privateResponse([]byte(`{"text":"synthetic-\u0074est-key`), []string{"synthetic-test-key"}); len(x.Body) != 0 || x.Omission != "invalid_json_redaction_unavailable" {
		t.Fatal(x)
	}
}
func TestProviderResponseEvidenceBoundaries(t *testing.T) {
	for _, mode := range []string{"complete", "duplicate", "malformed", "oversized", "interrupted", "encoding"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", diagnosticRequestID)
				switch mode {
				case "complete":
					fmt.Fprint(w, `{"unknown":"synthetic-test-key"}`)
				case "duplicate":
					fmt.Fprint(w, `{"x":1,"x":2}`)
				case "malformed":
					fmt.Fprint(w, `{"x":`)
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", 2048))
				case "interrupted":
					w.Header().Set("Content-Length", "900")
					fmt.Fprint(w, `{"x":1}`)
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
					fmt.Fprint(w, "unknown")
				}
			}))
			defer server.Close()
			c := fixtureClient(t, server, true)
			body, d, evidence, err := c.GenerateWithEvidence(context.Background(), nativeRequest(c.profile.Codec))
			if d.HTTPStatus != 200 || d.RequestID != diagnosticRequestID {
				t.Fatal(d)
			}
			if mode == "complete" {
				if err != nil || len(body) == 0 || len(evidence.Body) == 0 || !evidence.Redacted {
					t.Fatal(err, d, evidence)
				}
				return
			}
			if err == nil || len(body) != 0 {
				t.Fatal("invalid body usable", err)
			}
			if mode == "duplicate" {
				if string(evidence.Body) != `{"x":1,"x":2}` {
					t.Fatal("duplicate evidence lost", evidence)
				}
				return
			}
			if len(evidence.Body) != 0 || evidence.Omission == "" {
				t.Fatal("missing omission", evidence)
			}
		})
	}
}
