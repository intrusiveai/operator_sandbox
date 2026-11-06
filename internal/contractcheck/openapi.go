// Package contractcheck supports offline build-time comparison with Interceptor's
// OpenAPI document. It is used by integration tests, never campaign admission.
package contractcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Document struct {
	Value    map[string]any
	compiler *jsonschema.Compiler
}
type offline struct{}

func (offline) Load(string) (any, error) {
	return nil, fmt.Errorf("external OpenAPI references are forbidden")
}

func Load(defaultPath string) (*Document, error) {
	path := os.Getenv("OPERATOR_INTERCEPTOR_CONTRACT")
	if path == "" {
		path = defaultPath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}
func Parse(raw []byte) (*Document, error) {
	var doc map[string]any
	if len(raw) > 4<<20 || json.Unmarshal(raw, &doc) != nil || doc["openapi"] != "3.1.0" {
		return nil, fmt.Errorf("expected bounded OpenAPI 3.1.0 JSON")
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "$ref" {
					if ref, ok := v.(string); !ok || !strings.HasPrefix(ref, "#/") {
						return fmt.Errorf("external OpenAPI reference")
					}
				}
				if err := walk(v); err != nil {
					return err
				}
			}
		case []any:
			for _, v := range x {
				if err := walk(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(doc); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.AssertContent()
	c.UseLoader(offline{})
	if err := c.AddResource("urn:operator:interceptor", doc); err != nil {
		return nil, err
	}
	d := &Document{doc, c}
	components, ok := doc["components"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing schemas")
	}
	for name := range schemas {
		if _, err := c.Compile("urn:operator:interceptor#/components/schemas/" + Escape(name)); err != nil {
			return nil, err
		}
	}
	return d, nil
}
func Escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func (d *Document) At(pointer string) map[string]any {
	var v any = d.Value
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
	}
	m, _ := v.(map[string]any)
	return m
}
func (d *Document) Validate(pointer string, raw []byte) error {
	s, err := d.compiler.Compile("urn:operator:interceptor" + pointer)
	if err != nil {
		return err
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return s.Validate(v)
}
func (d *Document) Expand(s map[string]any) map[string]any {
	if ref, ok := s["$ref"].(string); ok {
		return d.Expand(d.At(ref))
	}

	return s
}

// Compare checks actual serialized Go field names and JSON kinds recursively.
// Projection is explicit only for native session metadata, whose extra fields
// Operator deliberately retains without decoding. Arbitrary JSON remains opaque;
// operation-specific exchanges test its required body shape separately.
func (d *Document) Compare(typ reflect.Type, pointer string, projection bool) error {
	var visit func(reflect.Type, map[string]any, string, bool, bool) error
	visit = func(t reflect.Type, s map[string]any, path string, project, variant bool) error {
		s = d.Expand(s)
		if t == reflect.TypeFor[json.RawMessage]() || t.Kind() == reflect.Interface {
			return nil
		}
		for _, keyword := range []string{"anyOf", "oneOf"} {
			if variants, ok := s[keyword].([]any); ok {
				for _, raw := range variants {
					variant := d.Expand(raw.(map[string]any))
					if variant["type"] == "null" {
						continue
					}
					if err := visit(t, variant, path+"/"+keyword, project, true); err != nil {
						return err
					}
				}
				return nil
			}
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		kind := ""
		switch t.Kind() {
		case reflect.Struct, reflect.Map:
			kind = "object"
		case reflect.Slice, reflect.Array:
			kind = "array"
			if t.Elem().Kind() == reflect.Uint8 {
				kind = "string"
			}
		case reflect.String:
			kind = "string"
		case reflect.Bool:
			kind = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			kind = "integer"
		case reflect.Float32, reflect.Float64:
			kind = "number"
		}
		if t == reflect.TypeFor[time.Time]() {
			kind = "string"
		}
		match := s["type"] == kind
		if types, ok := s["type"].([]any); ok {
			for _, v := range types {
				match = match || v == kind
			}
		}
		if !match {
			return fmt.Errorf("%s: Go %s does not match OpenAPI type %v", path, t, s["type"])
		}
		if t == reflect.TypeFor[time.Time]() {
			return nil
		}
		if (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && kind == "array" {
			item, _ := s["items"].(map[string]any)
			return visit(t.Elem(), item, path+"/*", false, false)
		}
		if t.Kind() == reflect.Map {
			if item, ok := s["additionalProperties"].(map[string]any); ok {
				return visit(t.Elem(), item, path+"/*", false, false)
			}
			return nil
		}
		if t.Kind() != reflect.Struct {
			return nil
		}
		props, _ := s["properties"].(map[string]any)
		fields := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.PkgPath != "" || tag == "-" {
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			fields[tag] = true
			field, ok := props[tag].(map[string]any)
			if !ok {
				// A union variant may omit an optional Go field entirely (for
				// example, a pending response has no body). Concrete outgoing
				// bytes must still pass Validate; required fields cannot vanish.
				if variant && strings.Contains(","+f.Tag.Get("json")+",", ",omitempty,") {
					continue
				}
				return fmt.Errorf("%s/%s: Go field absent from OpenAPI", path, tag)
			}
			if err := visit(f.Type, field, path+"/"+tag, false, false); err != nil {
				return err
			}
		}
		if !project {
			for key := range props {
				if !fields[key] {
					return fmt.Errorf("%s/%s: OpenAPI field absent from Go decoder", path, key)
				}
			}
		}
		return nil
	}
	return visit(typ, d.At(pointer), pointer, projection, false)
}

// NativeExchange validates a production-encoded command and its specific
// response, rather than accepting any unrelated successful native response.
func (d *Document) NativeExchange(request, response []byte) error {
	if err := d.Validate("#/components/schemas/NativeRequest", request); err != nil {
		return err
	}
	var q struct {
		Request struct {
			Operation string `json:"operation"`
		} `json:"request"`
	}
	if err := json.Unmarshal(request, &q); err != nil {
		return err
	}
	for _, raw := range d.At("#/components/schemas/NativeRequest")["oneOf"].([]any) {
		s := d.Expand(raw.(map[string]any))
		header := s["properties"].(map[string]any)["request"].(map[string]any)
		if header["properties"].(map[string]any)["operation"].(map[string]any)["const"] == q.Request.Operation {
			return d.Validate(s["x-response-schema"].(map[string]any)["$ref"].(string), response)
		}
	}
	return fmt.Errorf("native operation has no response schema")
}
