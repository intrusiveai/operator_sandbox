package contracts

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSharedUnsupportedFieldDetails(t *testing.T) {
	raw, err := os.ReadFile("../schemas/fixtures/unsupported-fields.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Path      string
		Schema          map[string]any
		Bad, Good       json.RawMessage
		Names, Contains []string
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			const uri = "urn:operator:schema:fixture:v1alpha1"
			tc.Schema["$id"] = uri
			tc.Schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
			schema, _ := json.Marshal(tc.Schema)
			c, err := LoadCatalog(fstest.MapFS{"catalog.json": {Data: []byte(`{"` + uri + `":"fixture.schema.json"}`)}, "fixture.schema.json": {Data: schema}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Validate(uri, tc.Bad, OrdinaryLimit)
			var issue *ArgumentError
			if !errors.As(err, &issue) || issue.UnsupportedFields == nil {
				t.Fatal(err)
			}
			if issue.FieldPath != tc.Path || !reflect.DeepEqual(issue.UnsupportedFields.Names, tc.Names) {
				t.Fatalf("%+v %+v", issue, issue.UnsupportedFields)
			}
			for _, part := range tc.Contains {
				if !strings.Contains(issue.Correction, part) {
					t.Fatalf("missing %s: %s", part, issue.Correction)
				}
			}
			if strings.Contains(issue.Correction, "SYNTHETIC_SECRET") {
				t.Fatal("value leaked")
			}
			correction, ok := c.ConstraintCorrectionWithFields(issue.Constraint, issue.UnsupportedFields)
			if !ok || correction != issue.Correction {
				t.Fatal("reconstruction differs")
			}
			if _, err = c.Validate(uri, tc.Good, OrdinaryLimit); err != nil {
				t.Fatal("correction rejected", err)
			}
		})
	}
}
