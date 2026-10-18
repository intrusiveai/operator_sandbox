package contracts

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// ConclusionError identifies a validation rule without retaining document prose,
// schema error messages or response bodies. Hosts must sanitize identifiers before
// exposing them and attach the operation ID from their validated request envelope.
type ConclusionError struct {
	Reason     string
	FieldPath  string
	Identifier string
	Cause      error
}

func (e *ConclusionError) Error() string { return "conclusion validation failed: " + e.Reason }
func (e *ConclusionError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrProtocol
}
func conclusionIssue(reason, path, id string) error {
	return &ConclusionError{Reason: reason, FieldPath: path, Identifier: id}
}

func conclusionSchemaPath(err error) string {
	var e *jsonschema.ValidationError
	if !errors.As(err, &e) {
		return ""
	}
	for len(e.Causes) > 0 {
		e = e.Causes[0]
	}
	parts := append([]string{}, e.InstanceLocation...)
	var fields []string
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		fields = append(fields, k.Missing...)
	case *kind.AdditionalProperties:
		fields = append(fields, k.Properties...)
	}
	if len(fields) > 0 {
		sort.Strings(fields)
		parts = append(parts, fields[0])
	}
	path := ""
	for _, part := range parts {
		path += "/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	return path
}

// ConclusionHypotheses checks local definitions as a set, independent of order.
// The caller must first validate the conclusion schema.
// Missing parents may be historical references and require host resolution.
func ConclusionHypotheses(c map[string]any) (map[string]string, error) {
	parents := map[string]string{}
	for i, item := range c["hypotheses"].([]any) {
		h := item.(map[string]any)
		id := h["hypothesis_id"].(string)
		path := "/hypotheses/" + strconv.Itoa(i)
		if _, exists := parents[id]; exists {
			return nil, conclusionIssue("duplicate_hypothesis", path+"/hypothesis_id", id)
		}
		parent, _ := h["provenance"].(map[string]any)["parent_hypothesis_id"].(string)
		if parent == id {
			return nil, conclusionIssue("self_parent", path+"/provenance/parent_hypothesis_id", id)
		}
		parents[id] = parent
	}
	for i, item := range c["hypotheses"].([]any) {
		id := item.(map[string]any)["hypothesis_id"].(string)
		seen := map[string]bool{}
		for next := id; next != ""; next = parents[next] {
			if seen[next] {
				return nil, conclusionIssue("hypothesis_cycle", "/hypotheses/"+strconv.Itoa(i)+"/provenance/parent_hypothesis_id", next)
			}
			seen[next] = true
		}
	}
	return parents, nil
}
