//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/modelcodec"
)

// Exercise private profile -> policy -> request builder -> TLS provider, not
// only the option allowlist. No model credentials or external calls are used.
func TestAnthropicOutputConfigAtProviderBoundary(t *testing.T) {
	for _, options := range []string{
		`{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`,
		`{"thinking":{"type":"disabled"},"output_config":{"effort":"medium"}}`,
		`{"thinking":{"type":"disabled"}}`,
		`{"output_config":null}`,
	} {
		t.Run(options, func(t *testing.T) {
			var received []byte
			calls := 0
			response := []byte(`{"id":"msg-test","type":"message","role":"assistant","model":"test-model","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":10}}`)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				received, _ = io.ReadAll(r.Body)
				if r.URL.Path != "/messages" || r.Header.Get("X-Api-Key") != "synthetic-test-key" {
					t.Error("host route or authentication changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(response)
			}))
			defer server.Close()
			base := profile(t, "anthropic-messages", server.URL)
			settings := base.Settings()
			settings.CodecOptions = json.RawMessage(options)
			raw, _ := json.Marshal(settings)
			p, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if p.Digest() == base.Digest() {
				t.Fatal("private options missing from profile identity")
			}
			public, err := p.PublicModel()
			if err != nil || bytes.Contains(public, []byte("output_config")) || bytes.Contains(public, []byte("thinking")) {
				t.Fatal("private options exposed to Harness", err)
			}
			policy, err := p.Policy([]byte(`[]`), []byte("Controlled request"))
			if err != nil {
				t.Fatal(err)
			}
			client, err := New(context.Background(), p, &secretResolver{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			var expected map[string]json.RawMessage
			json.Unmarshal([]byte(options), &expected)
			for _, purpose := range []string{"generation", "compaction"} {
				relay, _ := json.Marshal(map[string]any{"purpose": purpose, "conversation": []any{map[string]any{"role": "user", "content": "Reply OK"}}})
				request, err := modelcodec.Build(policy, relay)
				if err != nil {
					t.Fatal(err)
				}
				out, err := client.Generate(context.Background(), request)
				if err != nil || !bytes.Equal(out, response) {
					t.Fatal("provider exchange failed", err)
				}
				var native map[string]json.RawMessage
				if err := json.Unmarshal(received, &native); err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"thinking", "output_config"} {
					want := expected[field]
					if string(want) == "null" {
						want = nil
					}
					if !bytes.Equal(native[field], want) {
						t.Fatalf("%s %s: got %s want %s", purpose, field, native[field], want)
					}
				}
				if string(native["max_tokens"]) != "100" || string(native["model"]) != `"test-model"` {
					t.Fatal("host model or output allowance changed")
				}
				parsed, err := modelcodec.Parse(modelcodec.Anthropic, out)
				if err != nil || parsed.Metrics.TotalTokens != 30 {
					t.Fatal("normal response accounting failed", err)
				}
			}
			if calls != 2 {
				t.Fatal("unexpected provider retry", calls)
			}
			if _, err := modelcodec.Build(policy, []byte(`{"purpose":"generation","conversation":[],"output_config":{"effort":"low"}}`)); err == nil {
				t.Fatal("Harness supplied private options")
			}
		})
	}
}
