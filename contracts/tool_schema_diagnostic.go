package contracts

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// SchemaConstraint identifies an installed rule, never a submitted expectation.
// Hosts reconstruct its correction from their own approved schema bytes.
type SchemaConstraint struct {
	Schema  string `json:"schema"`
	Path    string `json:"path"`
	Keyword string `json:"keyword"`
}

func nearestArgumentPath(path string) string {
	for path != "" {
		if p, ok := SafeArgumentPath(path); ok {
			return p
		}
		path = path[:strings.LastIndex(path, "/")]
	}
	return ""
}

func selectSchemaError(e *jsonschema.ValidationError) *jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		return e
	}
	switch e.ErrorKind.(type) {
	case *kind.OneOf, *kind.AnyOf:
		var mismatch func(*jsonschema.ValidationError) bool
		mismatch = func(c *jsonschema.ValidationError) bool {
			if _, ok := c.ErrorKind.(*kind.Type); ok && len(c.InstanceLocation) == len(e.InstanceLocation) {
				return true
			}
			if _, ok := c.ErrorKind.(*kind.Const); ok && len(c.InstanceLocation) == len(e.InstanceLocation)+1 {
				return true
			}
			for _, child := range c.Causes {
				if mismatch(child) {
					return true
				}
			}
			return false
		}
		candidates := []*jsonschema.ValidationError{}
		for _, c := range e.Causes {
			if !mismatch(c) {
				candidates = append(candidates, c)
			}
		}
		if len(candidates) != 1 {
			return e
		}
		return selectSchemaError(candidates[0])
	}
	children := append([]*jsonschema.ValidationError{}, e.Causes...)
	sort.SliceStable(children, func(i, j int) bool {
		a, b := selectSchemaError(children[i]), selectSchemaError(children[j])
		return strings.Join(a.InstanceLocation, "/")+strings.Join(a.ErrorKind.KeywordPath(), "/") < strings.Join(b.InstanceLocation, "/")+strings.Join(b.ErrorKind.KeywordPath(), "/")
	})
	return selectSchemaError(children[0])
}

func (c *Catalog) schemaArgumentIssue(err error) *ArgumentError {
	var prior *ArgumentError
	if errors.As(err, &prior) {
		return prior
	}
	result := &ArgumentError{Reason: "invalid_structure", Correction: "Read the indicated tool's guidance and supply its required structure.", Cause: ErrSchema}
	var e *jsonschema.ValidationError
	if !errors.As(err, &e) {
		return result
	}
	e = selectSchemaError(e)
	parts := append([]string{}, e.InstanceLocation...)
	var fields []string
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		fields = append(fields, k.Missing...)
	case *kind.AdditionalProperties:
		result.UnsupportedFields = NewUnsupportedFields(k.Properties)
	}
	if len(fields) > 0 {
		sort.Strings(fields)
		parts = append(parts, fields[0])
	}
	if len(parts) > 0 {
		for i, part := range parts {
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
		}
		location := "/" + strings.Join(parts, "/")
		result.FieldPath = nearestArgumentPath(location)
		if result.UnsupportedFields != nil {
			result.UnsupportedFields.LocationOmitted = result.FieldPath != location
		}
	}
	keyword := "not"
	if keys := e.ErrorKind.KeywordPath(); len(keys) > 0 {
		keyword = keys[0]
	}
	uri, path, _ := strings.Cut(e.SchemaURL, "#")
	ref := &SchemaConstraint{Schema: uri, Path: path, Keyword: keyword}
	if correction, ok := c.ConstraintCorrectionWithFields(ref, result.UnsupportedFields); ok {
		result.Correction = correction
		result.Constraint = ref
	}
	return result
}

func (p *Protocol) ConstraintCorrection(ref *SchemaConstraint) (string, bool) {
	return p.catalog.ConstraintCorrection(ref)
}
func (p *Protocol) ConstraintCorrectionWithFields(ref *SchemaConstraint, fields *UnsupportedFields) (string, bool) {
	return p.catalog.ConstraintCorrectionWithFields(ref, fields)
}
func (c *Catalog) ConstraintCorrection(ref *SchemaConstraint) (string, bool) {
	return c.ConstraintCorrectionWithFields(ref, nil)
}
func (c *Catalog) ConstraintCorrectionWithFields(ref *SchemaConstraint, fields *UnsupportedFields) (string, bool) {
	if ref == nil || len(ref.Path) > 2048 {
		return "", false
	}
	raw, ok := c.sources[ref.Schema]
	if !ok {
		return "", false
	}
	var node any
	if json.Unmarshal(raw, &node) != nil {
		return "", false
	}
	if ref.Path != "" {
		if !strings.HasPrefix(ref.Path, "/") {
			return "", false
		}
		for _, part := range strings.Split(ref.Path[1:], "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch n := node.(type) {
			case map[string]any:
				node = n[part]
			case []any:
				i, err := strconv.Atoi(part)
				if err != nil || i < 0 || i >= len(n) {
					return "", false
				}
				node = n[i]
			default:
				return "", false
			}
		}
	}
	n, ok := node.(map[string]any)
	if !ok {
		return "", false
	}
	if _, ok = n[ref.Keyword]; !ok {
		return "", false
	}
	if fields != nil {
		if !fields.Valid() || ref.Keyword != "additionalProperties" {
			return "", false
		}
		// Reject a claimed unsupported name when the selected schema permits it.
		props, _ := n["properties"].(map[string]any)
		patterns, _ := n["patternProperties"].(map[string]any)
		for _, name := range fields.Names {
			if _, exists := props[name]; exists {
				return "", false
			}
			for pattern := range patterns {
				re, err := regexp.Compile(pattern)
				if err != nil || re.MatchString(name) {
					return "", false
				}
			}
		}
		return unsupportedCorrection(n, fields), true
	}
	return schemaCorrection(n, ref.Keyword), true
}

func schemaCorrection(n map[string]any, k string) string {
	v := n[k]
	compact := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	number := func() string {
		if f, ok := v.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return fmt.Sprint(v)
	}
	text := ""
	switch k {
	case "type":
		types := []string{}
		switch v := v.(type) {
		case string:
			types = append(types, v)
		case []any:
			for _, x := range v {
				types = append(types, fmt.Sprint(x))
			}
		}
		text = "Expected " + strings.Join(types, " or ") + "."
	case "required":
		text = "Supply required fields: " + compact(v) + "."
	case "additionalProperties", "unevaluatedProperties":
		keys := []string{}
		if props, ok := n["properties"].(map[string]any); ok {
			for k := range props {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		text = "Remove unsupported fields. Allowed fields: " + compact(keys) + "."
	case "enum":
		text = "Allowed values: " + compact(v) + "."
	case "const":
		text = "Use the required value: " + compact(v) + "."
	case "minLength":
		text = "String must contain at least " + number() + " characters."
	case "maxLength":
		text = "String must contain at most " + number() + " characters."
	case "pattern":
		text = "Use a string matching pattern " + compact(v) + "."
	case "minimum":
		text = "Use a number greater than or equal to " + number() + "."
	case "maximum":
		text = "Use a number less than or equal to " + number() + "."
	case "exclusiveMinimum":
		text = "Use a number greater than " + number() + "."
	case "exclusiveMaximum":
		text = "Use a number less than " + number() + "."
	case "multipleOf":
		text = "Use a number that is a multiple of " + number() + "."
	case "minItems":
		text = "Array must contain at least " + number() + " items."
	case "maxItems":
		text = "Array must contain at most " + number() + " items."
	case "uniqueItems":
		text = "Array must contain unique entries. Remove duplicate entries."
	case "minProperties":
		text = "Object must contain at least " + number() + " properties."
	case "maxProperties":
		text = "Object must contain at most " + number() + " properties."
	case "oneOf", "anyOf":
		choices := []any{}
		for _, x := range v.([]any) {
			b, ok := x.(map[string]any)
			if !ok {
				continue
			}
			values := map[string]any{}
			if props, ok := b["properties"].(map[string]any); ok {
				for key, x := range props {
					if s, ok := x.(map[string]any); ok {
						if c, ok := s["const"]; ok {
							values[key] = c
						}
					}
				}
			}
			required := b["required"]
			if required == nil {
				required = []any{}
			}
			choice := map[string]any{"values": values, "required": required}
			if t, ok := b["type"]; ok {
				choice["type"] = t
			}
			choices = append(choices, choice)
		}
		text = "Choose a matching alternative: "
		if k == "oneOf" {
			text = "Choose exactly one alternative: "
		}
		text += compact(choices) + "."
	case "not":
		required := any([]any{})
		if v, ok := v.(map[string]any); ok {
			if r, ok := v["required"]; ok {
				required = r
			}
		}
		text = "Do not combine these fields: " + compact(required) + ". Follow the mutually exclusive choices in tool guidance."
	default:
		text = "Follow the " + k + " constraint in this field's tool guidance."
	}
	if semantics, ok := n["x-operator-correction"].(string); ok {
		text = semantics + " " + text
	}
	if len(text) > 512 {
		text = "Read tool guidance for this field. Rule " + k + " requires the documented alternatives or fields; the complete expectation exceeds the diagnostic limit."
	}
	return text
}
