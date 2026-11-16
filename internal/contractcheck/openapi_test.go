package contractcheck

import (
	"encoding/json"
	"reflect"
	"testing"
)

type request struct {
	Operation string `json:"operation"`
	Limit     int    `json:"limit"`
}

func TestComparisonRejectsTypeFieldAndRequiredRequestDrift(t *testing.T) {
	base := `{"openapi":"3.1.0","components":{"schemas":{"Request":{"type":"object","additionalProperties":false,"properties":{"operation":{"type":"string","const":"invoke"},"limit":{"type":"integer","maximum":100}},"required":["operation","limit"]}}}}`
	for _, tc := range []struct {
		name      string
		mutate    func(map[string]any)
		typeError bool
	}{
		{"unchanged", func(map[string]any) {}, false},
		{"type", func(s map[string]any) { s["properties"].(map[string]any)["limit"].(map[string]any)["type"] = "string" }, true},
		{"removed", func(s map[string]any) {
			delete(s["properties"].(map[string]any), "limit")
			s["required"] = []any{"operation"}
		}, true},
		{"new", func(s map[string]any) {
			s["properties"].(map[string]any)["new_field"] = map[string]any{"type": "string"}
		}, true},
		{"bound", func(s map[string]any) { s["properties"].(map[string]any)["limit"].(map[string]any)["maximum"] = 50 }, false},
		{"constant", func(s map[string]any) {
			s["properties"].(map[string]any)["operation"].(map[string]any)["const"] = "other"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal([]byte(base), &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc["components"].(map[string]any)["schemas"].(map[string]any)["Request"].(map[string]any))
			raw, _ := json.Marshal(doc)
			d, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			err = d.Compare(reflect.TypeFor[request](), "#/components/schemas/Request", false)
			if (err != nil) != tc.typeError {
				t.Fatalf("type comparison: %v", err)
			}
			raw, _ = json.Marshal(request{"invoke", 100})
			err = d.Validate("#/components/schemas/Request", raw)
			if (err == nil) != (tc.name == "unchanged" || tc.name == "new") {
				t.Fatalf("request comparison: %v", err)
			}
		})
	}
}

func TestRejectExternalReferences(t *testing.T) {
	if _, err := Parse([]byte(`{"openapi":"3.1.0","components":{"schemas":{"X":{"$ref":"https://example.invalid/schema"}}}}`)); err == nil {
		t.Fatal("external ref accepted")
	}
}

func TestUnionCanOmitOptionalFieldButCannotRemoveRequiredField(t *testing.T) {
	type response struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body,omitempty"`
	}
	d, err := Parse([]byte(`{"openapi":"3.1.0","components":{"schemas":{"Response":{"anyOf":[{"type":"object","properties":{"status":{"type":"integer"},"body":{}},"required":["status","body"],"additionalProperties":false},{"type":"object","properties":{"status":{"type":"integer","const":0}},"required":["status"],"additionalProperties":false}]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Compare(reflect.TypeFor[response](), "#/components/schemas/Response", false); err != nil {
		t.Fatal(err)
	}
	if err = d.Validate("#/components/schemas/Response", []byte(`{"status":0}`)); err != nil {
		t.Fatal(err)
	}
	bad := d.At("#/components/schemas/Response")["anyOf"].([]any)[1].(map[string]any)
	delete(bad["properties"].(map[string]any), "status")
	if err = d.Compare(reflect.TypeFor[response](), "#/components/schemas/Response", false); err == nil {
		t.Fatal("required Go field removed")
	}
}

func TestUnionRefinementsInheritCommonWireShape(t *testing.T) {
	type item struct {
		Kind  string `json:"kind"`
		Value string `json:"value,omitempty"`
	}
	for _, branch := range []string{
		`{"properties":{"kind":{"const":"one"}}}`,
		`{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"one"},"value":{"type":"string"}}}`,
	} {
		raw := []byte(`{"openapi":"3.1.0","components":{"schemas":{"Item":{"type":"object","properties":{"kind":{"type":"string"},"value":{"type":"string"}},"oneOf":[` + branch + `]}}}}`)
		d, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.Compare(reflect.TypeFor[item](), "#/components/schemas/Item", false); err != nil {
			t.Fatal(err)
		}
		variant := d.At("#/components/schemas/Item")["oneOf"].([]any)[0].(map[string]any)
		variant["properties"].(map[string]any)["kind"].(map[string]any)["type"] = "integer"
		if d.Compare(reflect.TypeFor[item](), "#/components/schemas/Item", false) == nil {
			t.Fatal("branch type change accepted")
		}
	}
}
