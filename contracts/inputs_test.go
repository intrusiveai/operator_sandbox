package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

type inputFixture struct {
	Name        string            `json:"name"`
	Mode        string            `json:"mode"`
	Valid       bool              `json:"valid"`
	Document    json.RawMessage   `json:"document"`
	Messages    []json.RawMessage `json:"messages"`
	Tree        string            `json:"tree_base64"`
	Set         string            `json:"skill_set_base64"`
	Skills      []string          `json:"skills_base64"`
	Context     string            `json:"context_base64"`
	Bundle      string            `json:"bundle_base64"`
	Prompt      string            `json:"prompt_base64"`
	PromptMode  string            `json:"prompt_mode"`
	Base        string            `json:"base_base64"`
	Replacement *string           `json:"replacement_base64"`
	Appends     []string          `json:"appends_base64"`
	Effective   string            `json:"effective_base64"`
}

func TestSharedInputFixtures(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/engine-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []inputFixture
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
			case "context":
				_, err = p.ValidateEngineContext(c.Document)
			case "provenance":
				_, err = p.ValidatePromptProvenance(c.Document)
			case "compose":
				var replacement []byte
				if c.Replacement != nil {
					replacement = decode(*c.Replacement)
				}
				appends := [][]byte{}
				for _, s := range c.Appends {
					appends = append(appends, decode(s))
				}
				var output []byte
				var provenance map[string]any
				output, provenance, err = ComposePrompt(c.PromptMode, decode(c.Base), replacement, appends)
				if err == nil && c.Valid {
					if !bytes.Equal(output, decode(c.Effective)) {
						t.Fatal("effective prompt differs")
					}
					encoded, e := json.Marshal(provenance)
					if e != nil {
						t.Fatal(e)
					}
					if _, e := p.ValidatePromptProvenance(encoded); e != nil {
						t.Fatal(e)
					}
					if !wireEqual(provenance["effective"], rawDescriptor(output)) {
						t.Fatal("effective identity differs")
					}
				}
			case "launch":
				messages, skills := [][]byte{}, [][]byte{}
				for _, m := range c.Messages {
					messages = append(messages, m)
				}
				for _, s := range c.Skills {
					skills = append(skills, decode(s))
				}
				err = p.ValidateLaunchContent(messages, decode(c.Tree), decode(c.Set), skills, decode(c.Context), decode(c.Bundle), decode(c.Prompt))
			default:
				t.Fatal("unknown fixture mode")
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
		})
	}
	t.Logf("%d shared input cases", len(cases))
}

func TestPromptByteLimits(t *testing.T) {
	for _, mode := range []string{"default", "replacement", "extension"} {
		t.Run(mode, func(t *testing.T) {
			base := bytes.Repeat([]byte("x"), PromptLimit)
			var replacement []byte
			var appends [][]byte
			if mode == "replacement" {
				replacement = base
				base = []byte("base")
			}
			if mode == "extension" {
				base = base[:PromptLimit-3]
				appends = [][]byte{[]byte("y")}
			}
			output, _, err := ComposePrompt(mode, base, replacement, appends)
			if err != nil || len(output) != PromptLimit {
				t.Fatalf("limit failed: %v", err)
			}
			if mode == "replacement" {
				replacement = append(replacement, 'x')
			} else {
				base = append(base, 'x')
			}
			if _, _, err := ComposePrompt(mode, base, replacement, appends); err == nil {
				t.Fatal("over-limit prompt accepted")
			}
		})
	}
	raw := bytes.Repeat([]byte("é"), PromptLimit/2)
	if err := ValidatePrompt(raw); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrompt(append(raw, 'x')); err == nil {
		t.Fatal("UTF-8 byte ceiling not enforced")
	}
}
