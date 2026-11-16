package interceptor

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func connectionFor(t *testing.T, srv *httptest.Server) *Connection {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	s := &Connection{Protocol: u.Scheme, Address: u.Hostname(), Port: port}
	if cert := srv.Certificate(); cert != nil {
		s.CACertificatesPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	}
	return s
}

func TestConfiguredConnectionRoutesAndEvidence(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			routes := map[string]int{}
			data := []byte("bounded archive transfer")
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routes[r.URL.Path]++
				if r.Method != "POST" || r.Header.Get("Authorization") != "" {
					t.Error("unexpected request")
				}
				if r.URL.Path == "/v1/evidence" {
					response := evidenceResponse(data)
					for k, vs := range response.Header {
						w.Header()[k] = vs
					}
					w.Write(data)
					return
				}
				body := any(map[string]string{"code": "operation_in_progress"})
				status := 202
				if r.URL.Path == "/v1/contract-version" {
					body = supportedContract
					status = 200
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(Response{Status: status, Body: encoded(t, body), SessionRevision: 4})
			})
			start := httptest.NewServer
			if secure {
				start = httptest.NewTLSServer
			}
			srv := start(handler)
			defer srv.Close()
			settings := connectionFor(t, srv)
			if !secure {
				settings.Address = "localhost"
			}
			c, err := NewConfigured(settings)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			// The client freezes configuration; later caller mutation cannot redirect it.
			settings.Port = 1
			t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
			if err = c.CheckContract(context.Background()); err != nil {
				t.Fatal(err)
			}
			q := query()
			q.Operation = "application.invoke"
			prepared, err := PrepareOperation(q, []byte(`{"input":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.Execute(context.Background(), prepared)
			if err != nil || result.Status != 202 {
				t.Fatal(result, err)
			}
			download, err := c.DownloadEvidence(context.Background(), evidenceQuery(), privateEvidenceDir(t))
			if err != nil {
				t.Fatal(err)
			}
			reader, err := download.Reader()
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(reader)
			if err != nil || string(got) != string(data) {
				t.Fatal(string(got), err)
			}
			if err = download.Close(); err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"/v1/contract-version", "/v1/operations", "/v1/evidence"} {
				if routes[route] != 1 {
					t.Fatal(routes)
				}
			}
		})
	}
}

func TestConnectionValidationAndTLS(t *testing.T) {
	for _, s := range []Connection{
		{Protocol: "tcp", Address: "localhost", Port: 8080},
		{Protocol: "http", Address: "https://user:secret@host", Port: 8080},
		{Protocol: "http", Address: "host/path", Port: 8080},
		{Protocol: "http", Address: "host:8080", Port: 8080},
		{Protocol: "http", Address: "host", Port: 0},
		{Protocol: "https", Address: "host", Port: 65536},
		{Protocol: "http", Address: "host", Port: 8080, CACertificatesPEM: "invalid"},
		{Protocol: "https", Address: "host", Port: 8080, CACertificatesPEM: "invalid"},
	} {
		if _, err := NewConfigured(&s); err == nil {
			t.Fatal("invalid connection accepted")
		}
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "example.internal"} {
		s := Connection{Protocol: "http", Address: host, Port: 8080}
		c, err := NewConfigured(&s)
		if err != nil {
			t.Fatal(err)
		}
		if c.baseURL() != "http://"+net.JoinHostPort(host, "8080") {
			t.Fatal(c.baseURL())
		}
		c.Close()
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS request sent") }))
	defer srv.Close()
	s := connectionFor(t, srv)
	s.CACertificatesPEM = ""
	c, err := NewConfigured(s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.CheckContract(context.Background()); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
}

func TestConfiguredConnectionRejectsRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("followed redirect") }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, err := NewConfigured(connectionFor(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.CheckContract(context.Background()); err == nil {
		t.Fatal("accepted redirect")
	}
}
