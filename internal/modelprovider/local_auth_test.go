//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withAuthentication(t *testing.T, p *Profile, mode string) *Profile {
	t.Helper()
	s := p.Settings()
	s.Authentication = mode
	s.CredentialID = ""
	if mode == "secret-store" {
		s.CredentialID = "model-key"
	}
	raw, _ := json.Marshal(s)
	result, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCloudAPIKeysAndLocalGoogleADC(t *testing.T) {
	for _, tc := range []struct{ provider, mode string }{{"vertex-gemini", "secret-store"}, {"azure-openai", "secret-store"}, {"vertex-gemini", "google-adc"}, {"azure-openai", "azure-cli"}} {
		t.Run(tc.provider+"-"+tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			// A synthetic CLI prevents accidental access to the developer's login.
			t.Setenv("PATH", dir)
			os.WriteFile(filepath.Join(dir, "az"), []byte("#!/bin/sh\nprintf '%s' '{\"accessToken\":\"synthetic-local-token\",\"expires_on\":4102444800}'\n"), 0700)
			tokenCalls := 0
			tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokenCalls++
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"access_token":"synthetic-local-token","token_type":"Bearer","expires_in":3600}`)
			}))
			defer tokens.Close()
			rawADC, _ := json.Marshal(map[string]string{"type": "authorized_user", "client_id": "private-id", "client_secret": "private-secret", "refresh_token": "private-refresh", "token_uri": tokens.URL, "quota_project_id": "private-quota-project"})
			path := filepath.Join(dir, "adc.json")
			os.WriteFile(path, rawADC, 0600)
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
			t.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", "")
			requests := 0
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, nativeRequest(codecs[tc.provider][0])) {
					t.Error("native bytes changed")
				}
				if r.URL.RawQuery != "" {
					t.Error("credentials in URL")
				}
				if tc.mode == "secret-store" {
					keyHeader := "X-Goog-Api-Key"
					if tc.provider == "azure-openai" {
						keyHeader = "Api-Key"
					}
					if r.Header.Get(keyHeader) != "synthetic-test-key" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Goog-User-Project") != "" {
						t.Error("incorrect API key headers")
					}
				} else {
					if r.Header.Get("Authorization") != "Bearer synthetic-local-token" || r.Header.Get("X-Goog-Api-Key") != "" || r.Header.Get("Api-Key") != "" {
						t.Error("incorrect identity headers")
					}
					if tc.mode == "google-adc" && r.Header.Get("X-Goog-User-Project") != "private-quota-project" {
						t.Error("missing ADC quota project")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer target.Close()
			p := withAuthentication(t, profile(t, tc.provider, target.URL), tc.mode)
			resolver := &secretResolver{}
			client, err := New(context.Background(), p, resolver)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.http.Transport.(*http.Transport).TLSClientConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			for i := 0; i < 2; i++ {
				if _, err = client.Generate(context.Background(), nativeRequest(p.settings.Codec)); err != nil {
					t.Fatal(err)
				}
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err = client.Generate(canceled, nativeRequest(p.settings.Codec)); err == nil {
				t.Fatal("ignored cancellation")
			}
			if requests != 2 {
				t.Fatal("unexpected model replay")
			}
			if tc.mode == "secret-store" && (resolver.calls != 2 || tokenCalls != 0) {
				t.Fatal("API key route used wrong credentials")
			}
			if tc.mode != "secret-store" && resolver.calls != 0 {
				t.Fatal("identity route read a model key")
			}
			public, err := p.PublicModel([]byte("[]"))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private-id", "private-secret", "private-refresh", "private-quota-project", "authentication", "model-key"} {
				if bytes.Contains(public, []byte(secret)) {
					t.Fatal("public profile exposed private auth")
				}
			}
		})
	}
}

func TestCloudAuthenticationAdmission(t *testing.T) {
	for _, provider := range []string{"vertex-gemini", "azure-openai", "gemini-api", "openai-chat", "bedrock-converse"} {
		base := profile(t, provider, "https://example.invalid").Settings()
		for _, mode := range []string{"google-adc", "azure-cli", "azure-client-secret"} {
			s := base
			s.Authentication = mode
			s.CredentialID = ""
			raw, _ := json.Marshal(s)
			p, err := Parse(raw)
			want := (provider == "vertex-gemini" && mode == "google-adc") || (provider == "azure-openai" && strings.HasPrefix(mode, "azure-"))
			if (err == nil) != want {
				t.Fatalf("%s %s accepted=%v", provider, mode, err == nil)
			}
			if want {
				s.CredentialID = "conflicting-key"
				raw, _ = json.Marshal(s)
				if _, err = Parse(raw); err == nil {
					t.Fatal("ambiguous credentials accepted")
				}
				old := profile(t, provider, "https://example.invalid")
				if old.Digest() == p.Digest() {
					t.Fatal("auth selection not bound to private profile digest")
				}
			}
		}
	}
}
