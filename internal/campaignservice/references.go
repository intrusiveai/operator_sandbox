//go:build linux || darwin

package campaignservice

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func integer(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case float64:
		return int64(n)
	}
	return -1
}
func validRange(ref map[string]any, size int64) bool {
	value, ok := ref["range"]
	if !ok {
		return true
	}
	r := value.(map[string]any)
	offset, length := integer(r["offset"]), integer(r["length"])
	return offset >= 0 && length > 0 && offset <= size && length <= size-offset
}
func referenceIssue(reason, path, id string, cause error) error {
	if cause == nil || errors.Is(cause, campaign.ErrInvalid) {
		cause = errReference
	}
	return &contracts.ConclusionError{Reason: reason, FieldPath: path, Identifier: id, Cause: cause}
}

// Historical record validation retains its existing registration requirements.
func (s *Service) checkReferences(v any, newHyp string, supported bool) error {
	return s.resolveReferences(v, newHyp, nil, "", supported)
}

// Local definitions supplement (and shadow) historical definitions only within
// this conclusion. Never register, synthesize or append them as earlier records.
func (s *Service) checkConclusionReferences(c map[string]any) error {
	local, err := contracts.ConclusionHypotheses(c)
	if err != nil {
		return err
	}
	for i, item := range c["hypotheses"].([]any) {
		id := item.(map[string]any)["hypothesis_id"].(string)
		seen := map[string]bool{}
		for next := id; next != ""; {
			if seen[next] {
				return referenceIssue("hypothesis_cycle", "/hypotheses/"+strconv.Itoa(i)+"/provenance/parent_hypothesis_id", next, nil)
			}
			seen[next] = true
			if parent, ok := local[next]; ok {
				next = parent
			} else {
				next = s.completion.parents[next]
			}
		}
	}
	return s.resolveReferences(c, "", local, "", false)
}
func (s *Service) resolveReferences(v any, newHyp string, local map[string]string, path string, supported bool) error {
	knownHyp := func(id string) bool { _, ok := local[id]; return ok || s.completion.hypotheses[id] }
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			if err := s.resolveReferences(item, newHyp, local, path+"/"+strconv.Itoa(i), supported); err != nil {
				return err
			}
		}
	case map[string]any:
		supported = supported || x["interpretation"] == "supported"
		if a, ok := x["assessment"].(map[string]any); ok && a["interpretation"] == "supported" {
			supported = true
		}
		// Deterministic order makes the retained first failing rule reproducible.
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := x[key]
			field := path + "/" + key
			switch key {
			case "objective_id", "scenario_id", "surface_ref":
				id := value.(string)
				known := s.completion.objectives
				reason := "unknown_objective"
				if key == "scenario_id" {
					known = s.completion.scenarios
					reason = "unknown_scenario"
				} else if key == "surface_ref" {
					known = s.completion.surfaces
					reason = "unknown_surface"
				}
				if !known[id] {
					return referenceIssue(reason, field, id, nil)
				}
			case "hypothesis_id":
				if id := value.(string); id != newHyp && !knownHyp(id) {
					return referenceIssue("unresolved_hypothesis", field, id, nil)
				}
			case "parent_hypothesis_id":
				if !knownHyp(value.(string)) {
					return referenceIssue("unresolved_hypothesis", field, value.(string), nil)
				}
			case "objective_refs", "hypothesis_refs", "record_refs", "attempt_receipt_refs":
				for i, ref := range value.([]any) {
					id := ref.(string)
					at := field + "/" + strconv.Itoa(i)
					switch key {
					case "objective_refs":
						if !s.completion.objectives[id] {
							return referenceIssue("unknown_objective", at, id, nil)
						}
					case "hypothesis_refs":
						if !knownHyp(id) {
							return referenceIssue("unresolved_hypothesis", at, id, nil)
						}
					case "record_refs":
						r, ok := s.completion.records[id]
						if !ok {
							return referenceIssue("unresolved_record", at, id, nil)
						}
						if _, err := s.writer.ReadContent(r.request); err != nil {
							return referenceIssue("evidence_unavailable", at, id, err)
						}
						if _, err := s.writer.ReadContent(r.response); err != nil {
							return referenceIssue("evidence_unavailable", at, id, err)
						}
					case "attempt_receipt_refs":
						if _, err := s.attempts.FindPublication(id); err != nil {
							return referenceIssue("invalid_attempt", at, id, err)
						}
					}
				}
			case "attempt_receipt_id", "parent_attempt_receipt_id":
				if _, err := s.attempts.FindPublication(value.(string)); err != nil {
					return referenceIssue("invalid_attempt", field, value.(string), err)
				}
			case "observation_refs":
				for i, ref := range value.([]any) {
					if err := s.checkObservationAt(ref.(map[string]any), supported, field+"/"+strconv.Itoa(i)); err != nil {
						return err
					}
				}
			case "input_refs":
				for i, ref := range value.([]any) {
					r := ref.(map[string]any)
					id := r["entry_id"].(string)
					at := field + "/" + strconv.Itoa(i)
					size, ok := s.completion.inputSizes[id]
					if !ok {
						return referenceIssue("unknown_input", at+"/entry_id", id, nil)
					}
					if !validRange(r, size) {
						return referenceIssue("invalid_range", at+"/range", id, nil)
					}
				}
			default:
				if err := s.resolveReferences(value, newHyp, local, field, supported); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (s *Service) checkObservationAt(ref map[string]any, supported bool, path string) error {
	id := ref["attempt_receipt_id"].(string)
	receipt, err := s.attempts.FindPublication(id)
	if err != nil {
		return referenceIssue("invalid_attempt", path+"/attempt_receipt_id", id, err)
	}
	retained, err := feedback.OpenRecord(s.target().Protocol().Catalog(), receipt.Feedback)
	if err != nil || retained.Source().CampaignID != s.writer.Manifest().CampaignID {
		return referenceIssue("evidence_unavailable", path, id, campaign.ErrCorrupt)
	}
	id = ref["entry_id"].(string)
	for _, e := range retained.Entries() {
		if e.ID != id {
			continue
		}
		if !slices.Contains(s.target().ReadKinds(), e.Kind) || (e.Visibility != string(interceptor.TargetVisible) && e.Visibility != string(interceptor.HarnessVisible)) {
			return referenceIssue("observation_not_visible", path+"/entry_id", id, nil)
		}
		if e.Availability != "available" || e.Artifact == nil {
			if _, hasRange := ref["range"]; hasRange || supported {
				return referenceIssue("evidence_unavailable", path+"/entry_id", id, nil)
			}
			return nil
		}
		if !validRange(ref, e.Artifact.SizeBytes) {
			return referenceIssue("invalid_range", path+"/range", id, nil)
		}
		raw, err := s.attempts.ReadPublicationObject(receipt, e.ID)
		// Range-free gap references may identify metadata even when bytes are absent.
		if err != nil || int64(len(raw)) != e.Artifact.SizeBytes || contracts.RawDigest(raw) != e.Artifact.Digest {
			if _, hasRange := ref["range"]; supported || hasRange {
				return referenceIssue("evidence_unavailable", path+"/entry_id", id, nil)
			}
		}
		return nil
	}
	return referenceIssue("invalid_observation", path+"/entry_id", id, nil)
}
