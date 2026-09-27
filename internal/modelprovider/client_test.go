//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

type secretResolver struct {
	calls int
	id    string
}

func (r *secretResolver) Resolve(_ context.Context, id string) (credentials.Resolution, error) {
	r.calls++
	r.id = id
	return credentials.Resolution{Value: "synthetic-test-key"}, nil
}
func profile(t *testing.T, provider, endpoint string) *Profile {
	t.Helper()
	auth := "secret-store"
	credential := "model-key"
	if provider == "vertex-gemini" || provider == "bedrock-converse" {
		auth = "workload-identity"
		credential = ""
	}
	s := Settings{APIVersion: Version, ID: "test", Provider: provider, Codec: codecs[provider][0], Model: "test-model", Endpoint: endpoint, Authentication: auth, CredentialID: credential, MaximumPromptTokens: 10000, MaximumCompletionTokens: 100, MaximumResponseBytes: 1024}
	if provider == "anthropic-messages" {
		s.APIVersionHeader = "2023-06-01"
	}
	if provider == "bedrock-converse" {
		s.Endpoint = ""
		s.Region = "us-west-2"
	}
	if provider != "bedrock-converse" {
		s.Endpoint = strings.TrimSuffix(endpoint, "/") + nativeSuffix(s.Codec)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func nativeSuffix(codec string) string {
	return map[string]string{"openai-chat-text-tools-v1": "/chat/completions", "openai-responses-text-tools-v1": "/responses", "anthropic-messages-text-tools-v1": "/messages", "gemini-text-tools-v1": "/models/test-model:generateContent"}[codec]
}
func nativeRequest(codec string) []byte {
	switch codec {
	case "openai-chat-text-tools-v1":
		return []byte(`{"model":"test-model", "stream":false,"store":false,"max_completion_tokens":10,"messages":[]}`)
	case "openai-responses-text-tools-v1":
		return []byte(`{"model":"test-model","stream":false,"store":false,"max_output_tokens":10,"input":[]}`)
	case "anthropic-messages-text-tools-v1":
		return []byte(`{"model":"test-model","stream":false,"max_tokens":10,"messages":[]}`)
	case "gemini-text-tools-v1":
		return []byte(`{"generationConfig":{"maxOutputTokens":10},"contents":[]}`)
	case "bedrock-converse-text-tools-v1":
		return []byte(`{"messages":[{"role":"user","content":[{"text":"test"}]}],"inferenceConfig":{"maxTokens":10}}`)
	}
	panic("unsupported test codec")
}

func TestEveryHTTPProviderPreservesNativeBytesAndHostAuth(t *testing.T) {
	for _, provider := range []string{"openai-chat", "openai-responses", "anthropic-messages", "gemini-api", "vertex-gemini", "azure-openai", "litellm"} {
		t.Run(provider, func(t *testing.T) {
			var received []byte
			var auth, version string
			var calls int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				received, _ = io.ReadAll(r.Body)
				if r.URL.Path != "/fixed-native-route"+nativeSuffix(codecs[provider][0]) {
					t.Error("route changed")
				}
				header := "Authorization"
				switch provider {
				case "anthropic-messages":
					header = "X-Api-Key"
				case "gemini-api":
					header = "X-Goog-Api-Key"
				case "azure-openai":
					header = "Api-Key"
				}
				auth = r.Header.Get(header)
				version = r.Header.Get("Anthropic-Version")
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Private-Header", "do-not-return")
				fmt.Fprint(w, "{ \"native\": true }\n")
			}))
			defer server.Close()
			p := profile(t, provider, server.URL+"/fixed-native-route")
			resolver := &secretResolver{}
			client, err := New(context.Background(), p, resolver)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			transport := client.http.Transport.(*http.Transport)
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			if provider == "vertex-gemini" {
				client.bearer = func(context.Context) (string, error) { return "synthetic-test-key", nil }
			}
			raw := nativeRequest(p.settings.Codec)
			out, err := client.Generate(context.Background(), raw)
			if err != nil || !bytes.Equal(raw, received) || string(out) != "{ \"native\": true }\n" || calls != 1 {
				t.Fatal("native transfer failed", err)
			}
			expected := "Bearer synthetic-test-key"
			if provider == "anthropic-messages" || provider == "gemini-api" || provider == "azure-openai" {
				expected = "synthetic-test-key"
			}
			if auth != expected {
				t.Fatal("wrong authentication header")
			}
			if provider == "anthropic-messages" && version != "2023-06-01" {
				t.Fatal("missing API version")
			}
			if provider != "vertex-gemini" && (resolver.calls != 1 || resolver.id != "model-key") {
				t.Fatal("wrong credential reference")
			}
		})
	}
}

func TestHTTPFailuresNeverReplayOrExposePrivateText(t *testing.T) {
	for _, failure := range []string{"redirect", "429", "oversize", "stream", "compression", "duplicate-json", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				switch failure {
				case "redirect":
					w.Header().Set("Location", "/again")
					w.WriteHeader(307)
				case "429":
					w.WriteHeader(429)
					fmt.Fprint(w, `{"error":"private-provider-secret"}`)
				case "oversize":
					fmt.Fprint(w, `{"padding":"`+strings.Repeat("x", 1024)+`"}`)
				case "stream":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: private-provider-secret\n")
				case "compression":
					w.Header().Set("Content-Encoding", "gzip")
					fmt.Fprint(w, `{}`)
				case "duplicate-json":
					fmt.Fprint(w, `{"a":1,"a":2}`)
				case "cancel":
					select {
					case <-r.Context().Done():
					case <-time.After(3 * time.Second):
						t.Error("provider cancellation was not observed")
					}
				}
			}))
			defer server.Close()
			client, err := New(context.Background(), profile(t, "openai-chat", server.URL+"/fixed"), &secretResolver{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			timeout := 2 * time.Second
			if failure == "cancel" {
				timeout = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out, err := client.Generate(ctx, nativeRequest(client.profile.Codec))
			if !errors.Is(err, ErrProvider) || out != nil || calls.Load() != 1 || strings.Contains(err.Error(), "private-provider-secret") {
				t.Fatal("unsafe failure result", err, calls.Load())
			}
		})
	}
}

func TestBedrockNativeSignedHTTPBoundary(t *testing.T) {
	raw := nativeRequest("bedrock-converse-text-tools-v1")
	result := []byte(`{"output":{"message":{"role":"assistant","content":[{"text":"native response"}]}},"usage":{"inputTokens":12,"outputTokens":3,"totalTokens":15,"cacheReadInputTokens":4},"stopReason":"end_turn","additionalModelResponseFields":{"retained":true}}`)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, raw) || r.URL.Path != "/model/test-model/converse" || !strings.Contains(r.Header.Get("Authorization"), "/us-west-2/bedrock/aws4_request") || r.Header.Get("X-Amz-Security-Token") != "test-session-token" {
			t.Error("Bedrock request changed or unsigned")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	}))
	defer server.Close()
	p := profile(t, "bedrock-converse", "")
	c := &Client{profile: p.settings, http: boundedHTTP(), sign: awsSign(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test-id", SecretAccessKey: "test-secret", SessionToken: "test-session-token"}, nil
	}), "us-west-2")}
	c.profile.Endpoint = server.URL + "/model/test-model/converse"
	c.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	defer c.Close()
	out, err := c.Generate(context.Background(), raw)
	if err != nil || calls.Load() != 1 || !bytes.Equal(out, result) {
		t.Fatal("Bedrock native transfer failed", err)
	}
}

func TestProfileAndNativeOverrideRejections(t *testing.T) {
	p := profile(t, "openai-chat", "https://provider.invalid/v1/chat/completions")
	for _, change := range []func(*Settings){func(s *Settings) { s.Endpoint = "http://provider.invalid" }, func(s *Settings) { s.CredentialID = "" }, func(s *Settings) { s.Endpoint = "https://user:secret@provider.invalid" }, func(s *Settings) { s.Codec = "gemini-text-tools-v1" }, func(s *Settings) { s.Endpoint += "?api_key=secret" }, func(s *Settings) { s.Authentication = "workload-identity" }} {
		s := p.Settings()
		change(&s)
		raw, _ := json.Marshal(s)
		if _, err := Parse(raw); err == nil {
			t.Fatal("bad profile accepted")
		}
	}
	c := &Client{profile: p.settings}
	for _, raw := range []string{`{"model":"other","stream":false,"store":false,"max_completion_tokens":10}`, `{"model":"test-model","stream":true,"store":false,"max_completion_tokens":10}`, `{"model":"test-model","stream":false,"store":false,"max_completion_tokens":101}`, `{"model":"test-model","stream":false,"store":false,"max_completion_tokens":10,"headers":{}}`} {
		if _, err := c.Generate(context.Background(), []byte(raw)); !errors.Is(err, ErrRequest) {
			t.Fatal("guest override accepted", err)
		}
	}
}
