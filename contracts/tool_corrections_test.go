package contracts

import (
	"encoding/json"
	"errors"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSharedActionableToolCorrections(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/tool-corrections.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Tool, Path, Correction string
		Bad, Good                    json.RawMessage
		Contains                     []string
	}
	if json.Unmarshal(raw, &cases) != nil {
		t.Fatal("fixtures")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, err := p.ValidateToolArguments(c.Tool, c.Bad)
			var e *ArgumentError
			if !errors.As(err, &e) {
				t.Fatal(err)
			}
			if e.FieldPath != c.Path {
				t.Fatalf("path %q want %q: %v", e.FieldPath, c.Path, e)
			}
			if e.Correction != c.Correction {
				t.Fatalf("correction %q want %q", e.Correction, c.Correction)
			}
			for _, part := range c.Contains {
				if !strings.Contains(e.Correction, part) {
					t.Fatalf("missing %q: %v", part, e)
				}
			}
			if strings.Contains(e.Error(), "SYNTHETIC_SECRET") {
				t.Fatal("leak")
			}
			if e.Constraint != nil {
				correction, ok := p.ConstraintCorrectionWithFields(e.Constraint, e.UnsupportedFields)
				if !ok || correction != e.Correction {
					t.Fatal("unreconstructible constraint", e)
				}
			}
			if _, err = p.ValidateToolArguments(c.Tool, c.Good); err != nil {
				t.Fatal("correction failed", err)
			}
		})
	}
}

func TestSchemaConstraintFamilies(t *testing.T) {
	cases := []struct{ Schema, Bad, Good, Expected string }{
		{`{"oneOf":[{"type":"integer","maximum":1},{"type":"string"}]}`, `2`, `1`, "less than or equal to 1"},
		{`{"const":"fixed"}`, `"other"`, `"fixed"`, "required value"},
		{`{"minProperties":1}`, `{}`, `{"x":1}`, "at least 1 properties"},
		{`{"maxProperties":1}`, `{"x":1,"y":2}`, `{"x":1}`, "at most 1 properties"},
		{`{"minItems":1}`, `[]`, `[1]`, "at least 1 items"},
		{`{"maxItems":1}`, `[1,2]`, `[1]`, "at most 1 items"},
		{`{"exclusiveMinimum":0}`, `0`, `1`, "greater than 0"},
		{`{"exclusiveMaximum":2}`, `2`, `1`, "less than 2"},
		{`{"multipleOf":2}`, `3`, `4`, "multiple of 2"},
		{`{"oneOf":[{"required":["text"]},{"required":["json"]}]}`, `{}`, `{"text":"x"}`, "Choose exactly one alternative"},
		{`{"anyOf":[{"required":["text"]},{"required":["json"]}]}`, `{}`, `{"text":"x"}`, "Choose a matching alternative"},
		{`{"if":{"required":["text"]},"then":{"required":["media_type"]}}`, `{"text":"x"}`, `{"text":"x","media_type":"text/plain"}`, "media_type"},
	}
	for _, test := range cases {
		t.Run(test.Schema, func(t *testing.T) {
			var node map[string]any
			json.Unmarshal([]byte(test.Schema), &node)
			const id = "urn:operator:schema:fixture:v1alpha1"
			node["$id"] = id
			node["$schema"] = "https://json-schema.org/draft/2020-12/schema"
			raw, _ := json.Marshal(node)
			c, err := LoadCatalog(fstest.MapFS{"catalog.json": {Data: []byte(`{"` + id + `":"fixture.schema.json"}`)}, "fixture.schema.json": {Data: raw}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Validate(id, []byte(test.Bad), OrdinaryLimit)
			var issue *ArgumentError
			if !errors.As(err, &issue) || !strings.Contains(issue.Correction, test.Expected) {
				t.Fatal(issue, err)
			}
			if _, err = c.Validate(id, []byte(test.Good), OrdinaryLimit); err != nil {
				t.Fatal(err)
			}
		})
	}
}
