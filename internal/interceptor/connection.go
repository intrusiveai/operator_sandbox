package interceptor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// Connection is administrator-owned. A nil connection selects the default
// loopback HTTP endpoint.
// It contains no authentication secrets and is frozen with attachment intent.
type Connection struct {
	Protocol          string `json:"protocol"`
	Address           string `json:"address"`
	Port              int    `json:"port"`
	CACertificatesPEM string `json:"ca_certificates_pem,omitempty"`
}

func (s *Connection) Validate() error {
	if s == nil {
		return nil
	}
	if s.Protocol != "http" && s.Protocol != "https" {
		return diagnostictext.New("Interceptor protocol must be http or https")
	}
	if s.Port < 1 || s.Port > 65535 {
		return diagnostictext.New("Interceptor port must be between 1 and 65535")
	}
	if !connectionHost(s.Address) {
		return diagnostictext.New("Interceptor address must be a hostname or an IP address, without a URL, port, path or zone")
	}
	if s.CACertificatesPEM != "" {
		if s.Protocol != "https" {
			return diagnostictext.New("Interceptor CA certificates require protocol https")
		}
		if len(s.CACertificatesPEM) > 32<<10 || !x509.NewCertPool().AppendCertsFromPEM([]byte(s.CACertificatesPEM)) {
			return diagnostictext.New("Interceptor CA certificates must contain bounded PEM certificates")
		}
	}
	return nil
}

func connectionHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// NewConfigured uses only the administrator-selected endpoint. Redirects,
// ambient proxies and automatic request replay remain disabled. TLS verifies
// the hostname and system roots, with optional administrator CA certificates.
func NewConfigured(settings *Connection) (*Client, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	s := Connection{Protocol: "http", Address: "127.0.0.1", Port: 8080}
	if settings != nil {
		s = *settings
	}
	authority := net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.CACertificatesPEM != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, diagnostictext.New("Interceptor TLS system CA certificates are unavailable")
		}
		roots.AppendCertsFromPEM([]byte(s.CACertificatesPEM)) // Validate checked the PEM.
		tlsConfig.RootCAs = roots
	}
	dial := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10,
		TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 5 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != authority {
				return nil, diagnostictext.New("Interceptor destination differs from the configured endpoint")
			}
			return dial.DialContext(ctx, network, address)
		},
	}
	return &Client{origin: s.Protocol + "://" + authority, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) baseURL() string {
	if c.origin == "" {
		return Address
	}
	return c.origin
}
