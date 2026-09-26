//go:build linux || darwin

package campaignservice

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/feedback"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
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

// Schemas bound the recursive structures before this resolver runs. These checks
// establish reference membership/visibility, never truth of a harness assessment.
func (s *Service) checkReferences(v any, newHyp string, supported bool) error {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if err := s.checkReferences(item, newHyp, supported); err != nil {
				return err
			}
		}
	case map[string]any:
		supported = supported || x["interpretation"] == "supported"
		if a, ok := x["assessment"].(map[string]any); ok && a["interpretation"] == "supported" {
			supported = true
		}
		for key, value := range x {
			switch key {
			case "objective_id":
				if !s.completion.objectives[value.(string)] {
					return errReference
				}
			case "scenario_id":
				if !s.completion.scenarios[value.(string)] {
					return errReference
				}
			case "surface_ref":
				if !s.completion.surfaces[value.(string)] {
					return errReference
				}
			case "hypothesis_id":
				if id := value.(string); id != newHyp && !s.completion.hypotheses[id] {
					return errReference
				}
			case "parent_hypothesis_id":
				if !s.completion.hypotheses[value.(string)] {
					return errReference
				}
			case "objective_refs", "hypothesis_refs", "record_refs", "attempt_receipt_refs":
				for _, ref := range value.([]any) {
					id := ref.(string)
					switch key {
					case "objective_refs":
						if !s.completion.objectives[id] {
							return errReference
						}
					case "hypothesis_refs":
						if !s.completion.hypotheses[id] {
							return errReference
						}
					case "record_refs":
						r, ok := s.completion.records[id]
						if !ok {
							return errReference
						}
						if _, err := s.writer.ReadContent(r.request); err != nil {
							return err
						}
						if _, err := s.writer.ReadContent(r.response); err != nil {
							return err
						}
					case "attempt_receipt_refs":
						if _, err := s.attempts.FindPublication(id); err != nil {
							return referenceLookupError(err)
						}
					}
				}
			case "attempt_receipt_id", "parent_attempt_receipt_id":
				if _, err := s.attempts.FindPublication(value.(string)); err != nil {
					return referenceLookupError(err)
				}
			case "observation_refs":
				for _, ref := range value.([]any) {
					if err := s.checkObservation(ref.(map[string]any), supported); err != nil {
						return err
					}
				}
			case "input_refs":
				for _, ref := range value.([]any) {
					r := ref.(map[string]any)
					size, ok := s.completion.inputSizes[r["entry_id"].(string)]
					if !ok || !validRange(r, size) {
						return errReference
					}
				}
			default:
				if err := s.checkReferences(value, newHyp, supported); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func referenceLookupError(err error) error {
	if errors.Is(err, campaign.ErrInvalid) {
		return errReference
	}
	return err
}
func (s *Service) checkObservation(ref map[string]any, supported bool) error {
	receipt, err := s.attempts.FindPublication(ref["attempt_receipt_id"].(string))
	if err != nil {
		return referenceLookupError(err)
	}
	retained, err := feedback.OpenRecord(s.target().Protocol().Catalog(), receipt.Feedback)
	if err != nil {
		return campaign.ErrCorrupt
	}
	if retained.Source().CampaignID != s.writer.Manifest().CampaignID {
		return campaign.ErrCorrupt
	}
	for _, e := range retained.Entries() {
		if e.ID != ref["entry_id"] {
			continue
		}
		if !slices.Contains(s.target().ReadKinds(), e.Kind) || (e.Visibility != string(interceptor.TargetVisible) && e.Visibility != string(interceptor.HarnessVisible)) {
			return errReference
		}
		if e.Availability != "available" || e.Artifact == nil {
			if _, hasRange := ref["range"]; hasRange || supported {
				return errReference
			}
			return nil
		}
		if !validRange(ref, e.Artifact.SizeBytes) {
			return errReference
		}
		raw, err := s.attempts.ReadPublicationObject(receipt, e.ID)
		// Missing optional feedback cannot support an evidentiary assertion, but a
		// range-free gap reference may still identify the retained entry metadata.
		if err != nil || int64(len(raw)) != e.Artifact.SizeBytes || contracts.RawDigest(raw) != e.Artifact.Digest {
			_, hasRange := ref["range"]
			if supported || hasRange {
				return errReference
			}
		}
		return nil
	}
	return errReference
}
