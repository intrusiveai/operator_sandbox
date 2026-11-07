package contracts

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestSharedBudgetDiagnostics(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/budget-diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string
		Request json.RawMessage
		Valid   bool
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, err := p.ValidateRequest(c.Request)
			if (err == nil) != c.Valid {
				t.Fatalf("valid %v: %v", c.Valid, err)
			}
		})
	}
}
func TestSharedFinishCorrections(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/finish-diagnostics.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Path, Correction string
		Arguments              json.RawMessage
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, err := p.ValidateToolArguments("finish", c.Arguments)
			var issue *ArgumentError
			if !errors.As(err, &issue) || issue.FieldPath != c.Path || issue.Correction != c.Correction || strings.Contains(issue.Error(), "SYNTHETIC_SECRET") {
				t.Fatalf("%+v", issue)
			}
		})
	}
	for _, name := range []string{"finish", "observation_read"} {
		raw, err := p.ToolGuidance(name)
		if err != nil {
			t.Fatal(err)
		}
		var guide map[string]any
		json.Unmarshal(raw, &guide)
		examples := guide["examples"].(map[string]any)
		if name == "finish" {
			for _, k := range []string{"minimal_inline_example"} {
				b, _ := json.Marshal(examples[k])
				if _, err = p.ValidateToolArguments(name, b); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			ex := examples["pagination_example"].(map[string]any)
			b, _ := json.Marshal(ex["next_request"])
			if _, err = p.ValidateToolArguments(name, b); err != nil {
				t.Fatal(err)
			}
		}
	}
}
