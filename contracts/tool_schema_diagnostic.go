package contracts

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// schemaArgumentIssue uses schema-authored expectations, never validator prose
// or submitted values. The field path is separately allowlisted.
func schemaArgumentIssue(err error) *ArgumentError {
	path, _ := SafeArgumentPath(conclusionSchemaPath(err))
	correction := "Correct the indicated field to match its tool schema."
	var e *jsonschema.ValidationError
	if errors.As(err, &e) {
		for len(e.Causes) > 0 {
			e = e.Causes[0]
		}
		switch k := e.ErrorKind.(type) {
		case *kind.Type:
			correction = "Expected " + strings.Join(k.Want, " or ") + "."
			if len(k.Want) == 1 && k.Want[0] == "object" && strings.HasPrefix(path, "/uncertainties/") && strings.Count(path, "/") == 2 {
				correction += " Supply an uncertainty object with required kind and description; a plain string is not valid."
			}
		case *kind.Required:
			correction = "Supply the required field identified by field_path."
		case *kind.AdditionalProperties:
			correction = "Remove this unsupported field."
			if path == "/observations" {
				correction += " Put inspected observation handles in objectives[i].observations or another supported assessment item, not at the top level."
			}
		case *kind.Enum:
			raw, _ := json.Marshal(k.Want)
			if len(raw) <= 384 {
				correction = "Allowed values: " + string(raw) + "."
			}
		}
	}
	return &ArgumentError{Reason: "invalid_structure", FieldPath: path, Correction: correction, Cause: ErrSchema}
}
