package httpstarget

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func fixture() Settings {
	return Settings{
		APIVersion: Version, Origin: "https://agent.example.com", AllowedPrivateCIDRs: []string{},
		Authentication: Authentication{Mode: "none"},
		Operations:     []Operation{{ID: "chat", Method: "POST", Path: "/chat", Input: Input{Format: "json", Field: []string{"input", "text"}, Fixed: map[string]any{"mode": "test"}}, Response: Response{Format: "json", Field: []string{"answer", "text"}}, MaximumInputBytes: 1024, MaximumRequestBytes: 2048, MaximumResponseBytes: 4096}},
	}
}
func parsed(t *testing.T, s Settings) *Mapping {
	t.Helper()
	raw, _ := json.Marshal(s)
	m, e := Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestMapping(t *testing.T) {
	m := parsed(t, fixture())
	o, body, e := m.Request("chat", []byte("a\"},\"host\":\"evil"), "text/plain")
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	_ = json.Unmarshal(body, &v)
	if v["mode"] != "test" || v["input"].(map[string]any)["text"] != "a\"},\"host\":\"evil" {
		t.Fatal(string(body))
	}
	raw, media, e := SelectResponse(o, []byte(`{"answer":{"text":"ok"},"secret":"excluded"}`))
	if e != nil || string(raw) != "ok" || media != "text/plain" {
		t.Fatal(string(raw), media, e)
	}
	for _, bad := range []string{`{"answer":{}}`, `{"answer":null}`, `{"answer":{"text":"a","text":"b"}}`} {
		if _, _, e := SelectResponse(o, []byte(bad)); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, _, e := m.Request("other", []byte("x"), "text/plain"); e == nil {
		t.Fatal("unknown operation")
	}
	if _, _, e := m.Request("chat", []byte(strings.Repeat("x", 1025)), "text/plain"); e == nil {
		t.Fatal("input limit")
	}
	s := m.Settings()
	s.Operations[0].Path = "/changed"
	if m.Settings().Operations[0].Path != "/chat" {
		t.Fatal("mutable settings")
	}
}
func TestMappingValidation(t *testing.T) {
	cases := map[string]func(*Settings){
		"http":              func(s *Settings) { s.Origin = "http://example.com" },
		"userinfo":          func(s *Settings) { s.Origin = "https://user:password@example.com" },
		"origin-path":       func(s *Settings) { s.Origin += "/path" },
		"origin-query":      func(s *Settings) { s.Origin += "?secret=x" },
		"path-template":     func(s *Settings) { s.Operations[0].Path = "/../other" },
		"path-query":        func(s *Settings) { s.Operations[0].Path = "/chat?token=x" },
		"fixed-collision":   func(s *Settings) { s.Operations[0].Input.Fixed["input"] = map[string]any{"text": "fixed"} },
		"header-authority":  func(s *Settings) { s.Authentication = Authentication{"api-key", "credential", "Host"} },
		"cookie":            func(s *Settings) { s.Authentication = Authentication{"api-key", "credential", "Cookie"} },
		"unused-credential": func(s *Settings) { s.Authentication.CredentialID = "extra" },
		"broad-cidr":        func(s *Settings) { s.AllowedPrivateCIDRs = []string{"0.0.0.0/0"} },
		"link-local-cidr":   func(s *Settings) { s.AllowedPrivateCIDRs = []string{"169.254.0.0/16"} },
		"unbounded":         func(s *Settings) { s.Operations[0].MaximumResponseBytes = 0 },
		"duplicate":         func(s *Settings) { s.Operations = append(s.Operations, s.Operations[0]) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := fixture()
			change(&s)
			raw, _ := json.Marshal(s)
			if _, e := Parse(raw); e == nil {
				t.Fatal("accepted invalid mapping")
			}
		})
	}
	raw, _ := json.Marshal(fixture())
	raw = append(raw[:len(raw)-1], []byte(`,"script":"run"}`)...)
	if _, e := Parse(raw); e == nil {
		t.Fatal("unknown field")
	}
}
func TestDestinationPolicy(t *testing.T) {
	s := fixture()
	s.AllowedPrivateCIDRs = []string{"10.2.0.0/16", "127.0.0.1/32", "::1/128"}
	m := parsed(t, s)
	for _, ip := range []string{"8.8.8.8", "2606:4700:4700::1111", "10.2.1.1", "127.0.0.1", "::1"} {
		if !m.AllowsAddress(netip.MustParseAddr(ip)) {
			t.Fatal("denied", ip)
		}
	}
	for _, ip := range []string{"10.3.1.1", "192.168.1.1", "127.0.0.2", "169.254.169.254", "fd00:ec2::254", "168.63.129.16", "100.100.100.200", "0.0.0.0", "::", "fe80::1", "224.0.0.1", "192.0.2.1", "64:ff9b::a9fe:a9fe"} {
		if m.AllowsAddress(netip.MustParseAddr(ip)) {
			t.Fatal("allowed", ip)
		}
	}
}
