//go:build linux || darwin

package modelprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestProviderErrorDetailsEveryRoute(t *testing.T) {
	for _, route := range []string{"openai-chat", "openai-responses", "azure-openai", "azure-responses", "azure-identity", "litellm", "anthropic-messages", "bedrock-converse", "gemini-api", "vertex-gemini"} {
		t.Run(route, func(t *testing.T) {
			provider := route
			if strings.HasPrefix(route, "azure-") {
				provider = "azure-openai"
			}
			message := "The parameter temperature is unsupported."
			body := map[string]any{"error": map[string]any{"message": message, "type": "invalid_request_error", "code": "new_provider_code_123", "param": "temperature", "debug": "unselected-private-body"}}
			wantType, wantCode, wantParameter := "invalid_request_error", "new_provider_code_123", "temperature"
			switch provider {
			case "anthropic-messages":
				body = map[string]any{"type": "error", "error": map[string]any{"message": message, "type": "invalid_request_error"}}
				wantCode, wantParameter = "", ""
			case "bedrock-converse":
				body = map[string]any{"message": message}
				wantType, wantCode, wantParameter = "ValidationException", "", ""
			case "gemini-api", "vertex-gemini":
				body = map[string]any{"error": map[string]any{"message": message, "code": 400, "status": "INVALID_ARGUMENT", "details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.BadRequest", "fieldViolations": []any{map[string]any{"field": "generationConfig.temperature", "description": "unselected-private-body"}}}}}}
				wantType, wantCode, wantParameter = "INVALID_ARGUMENT", "400", "generationConfig.temperature"
			}
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if provider == "bedrock-converse" {
					w.Header().Set("X-Amzn-Errortype", "ValidationException")
					w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				}
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			p := profile(t, provider, server.URL)
			c := &Client{profile: p.settings, resolver: &secretResolver{}, http: boundedHTTP()}
			defer c.Close()
			if route == "azure-responses" {
				c.profile.Codec = "openai-responses-text-tools-v1"
			}
			if route == "azure-identity" {
				c.profile.Authentication = "azure-cli"
				c.profile.CredentialID = ""
				c.bearer = func(context.Context) (string, error) { return "synthetic-test-key", nil }
			}
			if provider == "vertex-gemini" {
				c.googleBearer = func(context.Context) (string, string, error) { return "synthetic-test-key", "", nil }
			}
			if provider == "bedrock-converse" {
				c.profile.Endpoint = server.URL
				c.sign = awsSign(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{AccessKeyID: "test-id", SecretAccessKey: "test-secret", SessionToken: "test-session-token"}, nil
				}), "us-east-2")
			}
			c.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			_, d, err := c.GenerateWithMetadata(context.Background(), nativeRequest(c.profile.Codec))
			if err == nil || !d.Valid() || d.HTTPStatus != 400 || d.ProviderMessage != message || d.ProviderType != wantType || d.ProviderCode != wantCode || d.ProviderParameter != wantParameter || calls.Load() != 1 {
				t.Fatalf("%+v %v calls=%d", d, err, calls.Load())
			}
			if wantCode == "" && d.Omissions.Code != "not_provided" || wantParameter == "" && d.Omissions.Parameter != "not_provided" {
				t.Fatal(d.Omissions)
			}
			raw, _ := json.Marshal(d)
			if strings.Contains(string(raw), "unselected-private-body") {
				t.Fatal("unselected field retained")
			}
			if got := DecodeDiagnostic(raw); got == nil || *got != d {
				t.Fatal("diagnostic round trip failed", string(raw))
			}
		})
	}
}

func TestErrorDetailRedactionAndBounds(t *testing.T) {
	secret := "actual+credential/value="
	redactor := newDetailRedactor([]string{secret})
	for _, text := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret))} {
		got, reason := selectDetail("Unsupported value; key="+text, 1024, redactor)
		if strings.Contains(got, text) || reason != "" || !strings.Contains(got, "[REDACTED]") {
			t.Fatal(got, reason)
		}
	}
	for _, text := range []string{"sk-proj-PARTIAL***masked", "Bearer unknown-credential", "https://user:password@example.test/?key=unknown-credential", "api_key=unknown-credential", "Authorization: secret-header-value", "password='unknown-credential'"} {
		got, _ := selectDetail(text, 1024, redactor)
		for _, secret := range []string{"PARTIAL", "unknown-credential", "secret-header-value", "user:password"} {
			if strings.Contains(got, secret) {
				t.Fatal(got)
			}
		}
	}
	for _, field := range []struct {
		value  any
		limit  int
		reason string
	}{
		{nil, 1024, "not_provided"}, {map[string]any{"secret": "value"}, 1024, "unsupported_value"}, {true, 1024, "unsupported_value"},
		{"bad\x1b[31m", 1024, "unsafe_control"}, {"bad\u202e", 1024, "unsafe_control"},
		{strings.Repeat("界", 500), 1024, "truncated"}, {strings.Repeat("x", 129), 128, "truncated"},
		{secret, 128, "credential_redacted"},
	} {
		got, reason := selectDetail(field.value, field.limit, redactor)
		if reason != field.reason || len(got) > field.limit || !utf8.ValidString(got) {
			t.Fatal(got, reason, field.reason)
		}
	}
	// Sanitize before truncation: no prefix of a credential can escape at the bound.
	got, reason := selectDetail(strings.Repeat("x", 1020)+secret, 1024, redactor)
	if reason != "truncated" || strings.Contains(got, "actu") {
		t.Fatal(got, reason)
	}
}

func TestErrorDetailsOmissionReasons(t *testing.T) {
	for _, tc := range []struct{ name, body, media, encoding, reason string }{
		{"missing", `{"error":{}}`, "application/json", "", "not_provided"},
		{"html", "<html>private</html>", "text/html", "", "unsupported_content_type"},
		{"malformed", `{"error":`, "application/json", "", "invalid_json"},
		{"duplicate", `{"error":{"message":"one","message":"two"}}`, "application/json", "", "invalid_json"},
		{"oversized", strings.Repeat("x", (16<<10)+1), "application/json", "", "body_size_limit"},
		{"compressed", "private", "application/json", "gzip", "unsupported_encoding"},
		{"interrupted", `{"error":{"message":"private"}}`, "application/json", "", "body_read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", tc.media)
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
				}
				if tc.name == "interrupted" {
					w.Header().Set("Content-Length", "500")
				}
				w.WriteHeader(400)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			c := fixtureClient(t, server, true)
			c.profile.MaximumResponseBytes = 64 << 10
			_, d, err := c.GenerateWithMetadata(context.Background(), nativeRequest(c.profile.Codec))
			if err == nil || !d.Valid() || d.HTTPStatus != 400 || d.Omissions.Message != tc.reason || d.Omissions.Type != tc.reason || d.Omissions.Code != tc.reason || d.Omissions.Parameter != tc.reason {
				t.Fatal(d, err)
			}
		})
	}
}

func TestBedrockErrorCredentialsRedacted(t *testing.T) {
	const access = "AKIA1234567890ABCDEF"
	const secret = "private-signing-secret"
	const session = "private-session-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, signature, _ := strings.Cut(r.Header.Get("Authorization"), "Signature=")
		message := "Rejected request: " + access + " " + secret + " " + session + " " + signature
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Errortype", "ValidationException")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"message": message})
	}))
	defer server.Close()
	p := profile(t, "bedrock-converse", "")
	c := &Client{profile: p.settings, http: boundedHTTP(), sign: awsSign(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: access, SecretAccessKey: secret, SessionToken: session}, nil
	}), "us-east-2")}
	c.profile.Endpoint = server.URL
	c.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	defer c.Close()
	_, d, err := c.GenerateWithMetadata(context.Background(), nativeRequest(c.profile.Codec))
	if err == nil || d.ProviderMessage != "Rejected request: [REDACTED] [REDACTED] [REDACTED] [REDACTED]" {
		t.Fatal(d, err)
	}
}

func TestDiagnosticRejectsUnknownNestedOmissions(t *testing.T) {
	d := Diagnostic{ProviderMessage: "A bounded error"}.WithFailure("http_rejection", "http_rejected")
	raw, _ := json.Marshal(d)
	raw = append(raw[:len(raw)-1], []byte(`,"omissions":{"unknown":"private"}}`)...)
	if DecodeDiagnostic(raw) != nil {
		t.Fatal("unknown nested metadata accepted")
	}
}

func TestBedrockErrorTypeSurvivesUnreadableBody(t *testing.T) {
	r := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not JSON"))}
	r.Header.Set("Content-Type", "text/html")
	r.Header.Set("X-Amzn-Errortype", "ValidationException")
	d := Diagnostic{Provider: "bedrock-converse", HTTPStatus: 400}.WithFailure("http_rejection", "http_rejected")
	d.readErrorDetails(r, 1024, nil)
	if d.ProviderType != "ValidationException" || d.Omissions.Type != "" || d.Omissions.Message != "unsupported_content_type" || !d.Valid() {
		t.Fatal(d)
	}
}

func TestAllErrorFieldsUseCredentialRedaction(t *testing.T) {
	r := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"the-secret","type":"the-secret","code":"the-secret","param":"the-secret"}}`))}
	r.Header.Set("Content-Type", "application/json")
	d := Diagnostic{Provider: "openai-chat", HTTPStatus: 400}.WithFailure("http_rejection", "http_rejected")
	d.readErrorDetails(r, 1024, []string{"the-secret"})
	if d.ProviderMessage != "" || d.ProviderType != "" || d.ProviderCode != "" || d.ProviderParameter != "" {
		t.Fatal(d)
	}
	if d.Omissions.Message != "credential_redacted" || d.Omissions.Type != "credential_redacted" || d.Omissions.Code != "credential_redacted" || d.Omissions.Parameter != "credential_redacted" {
		t.Fatal(d)
	}
}
