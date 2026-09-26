package contracts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"testing/fstest"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestSharedValidationFixtures(t *testing.T) {
	catalog, err := LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/validation-foundation.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string  `json:"name"`
		Raw     string  `json:"raw_base64"`
		Maximum int     `json:"maximum_bytes"`
		Schema  *string `json:"schema"`
		Valid   bool    `json:"valid"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(c.Raw)
			if err != nil {
				t.Fatal(err)
			}
			if c.Schema == nil {
				_, err = Decode(data, c.Maximum)
			} else {
				_, err = catalog.Validate(*c.Schema, data, c.Maximum)
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
			if err != nil && !errors.Is(err, ErrJSON) && !errors.Is(err, ErrSchema) && !errors.Is(err, ErrLimit) {
				t.Fatalf("unexpected public error: %v", err)
			}
		})
	}
	t.Logf("%d shared cases; %d schemas", len(cases), len(catalog.IDs()))
}
func TestCatalogRejectsExternalReferencesAndInvalidInventory(t *testing.T) {
	for _, ref := range []string{"https://example.invalid/forbidden.json", "file:///etc/passwd", "urn:missing"} {
		schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:test","anyOf":[{"type":"null"},{"$ref":"` + ref + `"}]}`
		files := fstest.MapFS{"catalog.json": {Data: []byte(`{"urn:test":"test.schema.json"}`)}, "test.schema.json": {Data: []byte(schema)}}
		if _, err := LoadCatalog(files); !errors.Is(err, ErrCatalog) {
			t.Fatalf("external schema accepted: %s %v", ref, err)
		}
	}
	for _, catalog := range []string{`{}`, `{"urn:test":"../outside.schema.json"}`, `{"urn:test":"x.schema.json","urn:test":"y.schema.json"}`} {
		if _, err := LoadCatalog(fstest.MapFS{"catalog.json": {Data: []byte(catalog)}}); !errors.Is(err, ErrCatalog) {
			t.Fatal(err)
		}
	}
	catalog, err := LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Validate("https://example.invalid/unknown", []byte(`{}`), OrdinaryLimit); !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
}
