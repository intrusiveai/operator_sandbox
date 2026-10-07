//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateCAValidatesNativeModelTLSWithoutWeakeningVerification(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	base := withAuthentication(t, profile(t, "litellm", server.URL), "none")
	settings := base.Settings()
	settings.CACertificatesPEM = certificate
	raw, _ := json.Marshal(settings)
	selected, err := Parse(raw)
	if err != nil || selected.Digest() == base.Digest() {
		t.Fatal("private CA must be validated and bound to private profile identity", err)
	}
	public, err := selected.PublicModel([]byte("[]"))
	if err != nil || bytes.Contains(public, []byte("ca_certificates_pem")) || bytes.Contains(public, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("private trust configuration leaked into guest inputs", err)
	}
	client, err := New(context.Background(), selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Generate(context.Background(), nativeRequest(settings.Codec)); err != nil || requests != 1 {
		t.Fatal("native TLS with explicitly trusted CA failed", err, requests)
	}
	if client.http.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("certificate verification disabled")
	}
	for _, tc := range []struct{ name, ca, endpoint string }{
		{"system-only", "", settings.Endpoint},
		{"hostname-mismatch", certificate, strings.Replace(settings.Endpoint, "127.0.0.1", "localhost", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setting := settings
			setting.CACertificatesPEM, setting.Endpoint = tc.ca, tc.endpoint
			raw, _ := json.Marshal(setting)
			p, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			c, err := New(context.Background(), p, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Generate(context.Background(), nativeRequest(setting.Codec)); err == nil || requests != 1 {
				t.Fatal("untrusted peer or wrong hostname accepted", err, requests)
			}
		})
	}
	settings.CACertificatesPEM = "not a certificate"
	raw, _ = json.Marshal(settings)
	if _, err := Parse(raw); err == nil {
		t.Fatal("invalid private trust data accepted")
	}
}
