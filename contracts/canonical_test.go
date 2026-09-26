package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestSharedCanonicalization(t *testing.T) {
	raw, err := os.ReadFile("../schemas/fixtures/canonicalization.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string `json:"name"`
		Raw       string `json:"raw_base64"`
		Canonical string `json:"canonical_base64"`
		Digest    string `json:"digest"`
		Valid     bool   `json:"valid"`
		Maximum   int    `json:"maximum"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			input, err := base64.StdEncoding.DecodeString(c.Raw)
			if err != nil {
				t.Fatal(err)
			}
			output, err := Canonicalize(input, c.Maximum)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
			digest, digestErr := CanonicalDigest(input, c.Maximum)
			if (digestErr == nil) != c.Valid {
				t.Fatalf("digest error=%v", digestErr)
			}
			if !c.Valid {
				return
			}
			expected, err := base64.StdEncoding.DecodeString(c.Canonical)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(output, expected) || digest != c.Digest {
				t.Fatalf("canonical bytes or digest differ: got %q want %q", output, expected)
			}
			again, err := Canonicalize(output, c.Maximum)
			if err != nil || !bytes.Equal(output, again) {
				t.Fatalf("not idempotent: %v", err)
			}
		})
	}
	t.Logf("%d shared canonicalization cases", len(cases))
}

func TestSharedIdentities(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/identity-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		inputFixture
		Request json.RawMessage `json:"request"`
		Content string          `json:"content_base64"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			decode := func(s string) []byte {
				b, err := base64.StdEncoding.DecodeString(s)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			var err error
			switch c.Mode {
			case "artifact":
				err = p.ValidateArtifactContent(c.Request, decode(c.Content))
			case "launch":
				messages, skills := [][]byte{}, [][]byte{}
				for _, m := range c.Messages {
					messages = append(messages, m)
				}
				for _, s := range c.Skills {
					skills = append(skills, decode(s))
				}
				// Negative identity vectors deliberately pass the earlier content checks.
				if e := p.ValidateLaunchContent(messages, decode(c.Tree), decode(c.Set), skills, decode(c.Context), decode(c.Bundle), decode(c.Prompt)); e != nil {
					t.Fatalf("fixture failed before identity verification: %v", e)
				}
				err = p.ValidateLaunchIdentities(messages, decode(c.Tree), decode(c.Set), skills, decode(c.Context), decode(c.Bundle), decode(c.Prompt))
			default:
				t.Fatal("unknown fixture mode")
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
		})
	}
	t.Logf("%d shared identity cases", len(cases))
}

func TestRegistryDigestIsolation(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	pins := p.RegistryDigests()
	pins["catalog_digest"] = "changed"
	if p.RegistryDigests()["catalog_digest"] == "changed" {
		t.Fatal("caller changed installed identity")
	}
}
