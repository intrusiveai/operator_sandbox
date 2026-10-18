package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

const ConclusionLimit = 1 << 20

// CompletionInput contains retained messages and bytes, not guest attestations.
// The caller must resolve these from its trusted launch/receipt records, including
// campaign membership and visibility. This helper performs no storage lookups.
type CompletionInput struct {
	Conclusion             []byte
	Binding                []byte
	ArtifactCommitResponse []byte
	RecordRequest          []byte
	RecordResponse         []byte
	StopRequest            []byte
}

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

// ValidateCompletion checks the committed-conclusion -> record -> stop chain.
// Successful validation does not persist stop, establish admission or certify any
// claimed effect. The explicit unavailable-conclusion stop variant bypasses this
// chain and remains subject to ordinary validation and host stop policy.
func (p *Protocol) ValidateCompletion(input CompletionInput) error {
	c, err := p.ValidateConclusion(input.Conclusion)
	if err != nil {
		return err
	}
	v, err := p.catalog.Validate(ConclusionBindingSchema, input.Binding, ControlLimit)
	if err != nil {
		return err
	}
	binding := v.(map[string]any)
	for key, value := range binding {
		if !same(c["binding"].(map[string]any)[key], value) {
			return ErrProtocol
		}
	}
	r, err := p.ValidateRequest(input.RecordRequest)
	if err != nil {
		return err
	}
	if r["operation"] != "engine.record_append" {
		return ErrProtocol
	}
	rb := r["body"].(map[string]any)
	if rb["record_kind"] != "conclusion" {
		return ErrProtocol
	}
	rv, err := p.ValidateResponse(input.RecordRequest, input.RecordResponse)
	if err != nil {
		return err
	}
	rs, ok := rv["result"].(map[string]any)
	if !ok {
		return ErrProtocol
	}
	stop, err := p.ValidateRequest(input.StopRequest)
	if err != nil {
		return err
	}
	if stop["operation"] != "engine.request_stop" {
		return ErrProtocol
	}
	sb := stop["body"].(map[string]any)
	for _, message := range []map[string]any{r, stop, rs["attribution"].(map[string]any)} {
		for _, key := range []string{"campaign_id", "launch_id", "run_revision"} {
			if !same(message[key], binding[key]) {
				return ErrProtocol
			}
		}
	}
	av, err := p.catalog.Validate(EnginePipeResponseSchema, input.ArtifactCommitResponse, OrdinaryLimit)
	if err != nil {
		return err
	}
	a := av.(map[string]any)
	if a["operation"] != "engine.artifact_commit" {
		return ErrProtocol
	}
	for _, key := range []string{"campaign_id", "launch_id"} {
		if !same(a[key], binding[key]) {
			return ErrProtocol
		}
	}
	artifact, ok := a["result"].(map[string]any)
	if !ok || artifact["purpose"] != "conclusion" {
		return ErrProtocol
	}
	descriptor := artifact["artifact"].(map[string]any)
	digest := sha256.Sum256(input.Conclusion)
	if descriptor["digest"] != "sha256:"+hex.EncodeToString(digest[:]) || number(descriptor["size_bytes"]) != int64(len(input.Conclusion)) || descriptor["media_type"] != "application/json" {
		return ErrProtocol
	}
	record := rb["record"].(map[string]any)
	conclusion := sb["conclusion"].(map[string]any)
	if conclusion["state"] != "committed" || !same(record["artifact_receipt"], artifact["artifact_receipt"]) || !same(conclusion["artifact_receipt"], artifact["artifact_receipt"]) || !same(conclusion["record_receipt"], rs["receipt_id"]) || !same(record["finish_reason"], c["finish_reason"]) || !same(sb["finish_reason"], c["finish_reason"]) {
		return ErrProtocol
	}
	return nil
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
