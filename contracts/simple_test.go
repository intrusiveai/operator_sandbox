package contracts

import (
	"encoding/json"
	"errors"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"testing"
)

func TestSharedSimpleWorkflow(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/simple-workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Binding json.RawMessage
		Finish  string `json:"finish_reason"`
		Cases   []struct {
			Name, Mode       string
			Value            json.RawMessage
			Valid            bool
			Bytes            string `json:"bytes_utf8"`
			Media            string `json:"media_type"`
			Canonicalization string
			Receipt          string `json:"artifact_receipt"`
		}
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixtures.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Mode == "input" {
				value, e := Decode(c.Value, ControlLimit)
				if e != nil {
					t.Fatal(e)
				}
				got, e := p.ResolveDirectInput(value.(map[string]any))
				if (e == nil) != c.Valid {
					t.Fatalf("valid=%v expected=%v: %v", e == nil, c.Valid, e)
				}
				if e == nil && (string(got.Bytes) != c.Bytes || got.MediaType != c.Media || got.Canonicalization != c.Canonicalization || got.ArtifactReceipt != c.Receipt) {
					t.Fatalf("input differs: %+v", got)
				}
			} else {
				got, e := p.NormalizeAssessment(c.Value, fixtures.Binding, fixtures.Finish)
				if (e == nil) != c.Valid {
					t.Fatalf("valid=%v expected=%v: %v", e == nil, c.Valid, e)
				}
				if e == nil {
					if got["status"] != "partial" {
						t.Fatal("default changed")
					}
					for _, field := range []string{"hypotheses", "claims", "coverage", "uncertainties", "record_refs"} {
						if _, ok := got[field].([]any); !ok {
							t.Fatal("missing resolved collection", field)
						}
					}
				}
			}
		})
	}
}

func TestSharedArgumentDiagnostics(t *testing.T) {
	p, e := LoadProtocol(schemas.Files)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../schemas/fixtures/argument-diagnostics.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Cases []struct{ Name, Tool, Raw, Reason, Path string }
	}
	if e = json.Unmarshal(raw, &f); e != nil {
		t.Fatal(e)
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			_, e := p.ValidateToolArguments(c.Tool, []byte(c.Raw))
			var issue *ArgumentError
			if !errors.As(e, &issue) || issue.Reason != c.Reason || issue.FieldPath != c.Path {
				t.Fatal(e, issue, c.Path)
			}
		})
	}
}
