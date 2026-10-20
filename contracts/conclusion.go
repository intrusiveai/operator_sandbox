package contracts

import (
	"sort"
	"strconv"
)

const ConclusionLimit = 1 << 20

// ValidateConclusion checks bounded content and internal references. It does not
// verify evidence truth, external receipt membership or current launch identity.
func (p *Protocol) ValidateConclusion(raw []byte) (map[string]any, error) {
	schema, ok := p.catalog.schemas[EngineConclusionSchema]
	if !ok {
		return nil, ErrCatalog
	}
	value, err := Decode(raw, ConclusionLimit)
	if err != nil {
		return nil, &ConclusionError{Reason: "invalid_structure", Cause: err}
	}
	if err := schema.Validate(value); err != nil {
		return nil, &ConclusionError{Reason: "invalid_structure", FieldPath: conclusionSchemaPath(err), Cause: ErrSchema}
	}
	c := value.(map[string]any)
	if path, ok := assessmentRangeError(c, ""); !ok {
		return nil, conclusionIssue("invalid_range", path, "")
	}
	if _, err := ConclusionHypotheses(c); err != nil {
		return nil, err
	}
	for _, pair := range [][2]string{{"objectives", "objective_id"}, {"claims", "claim_id"}, {"uncertainties", "gap_id"}} {
		field, id := pair[0], pair[1]
		if !uniqueEntries(c[field].([]any), id) {
			return nil, conclusionIssue("duplicate_entry", "/"+field, "")
		}
	}
	gaps := map[string]bool{}
	for i, item := range c["uncertainties"].([]any) {
		gap := item.(map[string]any)
		gaps[gap["gap_id"].(string)] = true
		if gap["kind"] == "coverage-limit" && c["status"] == "completed" {
			return nil, conclusionIssue("coverage_status", "/uncertainties/"+strconv.Itoa(i)+"/kind", gap["gap_id"].(string))
		}
	}
	for i, item := range c["claims"].([]any) {
		claim := item.(map[string]any)
		if !observationsLinked(claim) {
			return nil, conclusionIssue("observation_attempt_mismatch", "/claims/"+strconv.Itoa(i)+"/observation_refs", "")
		}
		for _, id := range claim["uncertainty_refs"].([]any) {
			if !gaps[id.(string)] {
				return nil, conclusionIssue("unresolved_uncertainty", "/claims/"+strconv.Itoa(i)+"/uncertainty_refs", id.(string))
			}
		}
	}
	return c, nil
}

func recordOK(body map[string]any) bool {
	r := body["record"].(map[string]any)
	if !assessmentRangesOK(r) {
		return false
	}
	switch body["record_kind"] {
	case "hypothesis":
		return hypothesisOK(r)
	case "progress":
		return observationsLinked(r)
	case "lineage":
		if parent, ok := r["parent_attempt_receipt_id"]; ok && same(parent, r["attempt_receipt_id"]) {
			return false
		}
	}
	return true
}

func hypothesisOK(h map[string]any) bool {
	parent, ok := h["provenance"].(map[string]any)["parent_hypothesis_id"]
	return !ok || !same(parent, h["hypothesis_id"])
}

// All ranges in assessment schemas describe nonempty half-open raw-byte spans.
func assessmentRangesOK(value any) bool {
	_, ok := assessmentRangeError(value, "")
	return ok
}
func assessmentRangeError(value any, path string) (string, bool) {
	switch v := value.(type) {
	case map[string]any:
		if span, ok := v["range"]; ok {
			r := span.(map[string]any)
			if number(r["offset"])+number(r["length"]) > MaxSafeInteger {
				return path + "/range", false
			}
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if field, ok := assessmentRangeError(v[key], path+"/"+key); !ok {
				return field, false
			}
		}
	case []any:
		for i, child := range v {
			if field, ok := assessmentRangeError(child, path+"/"+strconv.Itoa(i)); !ok {
				return field, false
			}
		}
	}
	return "", true
}

func observationsLinked(object map[string]any) bool {
	refs := map[string]bool{}
	for _, id := range object["attempt_receipt_refs"].([]any) {
		refs[id.(string)] = true
	}
	for _, item := range object["observation_refs"].([]any) {
		if !refs[item.(map[string]any)["attempt_receipt_id"].(string)] {
			return false
		}
	}
	return true
}

func uniqueEntries(entries []any, field string) bool {
	seen := map[string]bool{}
	for _, entry := range entries {
		id := entry.(map[string]any)[field].(string)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
