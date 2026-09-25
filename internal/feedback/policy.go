// Package feedback projects native observations into receipt-scoped harness data.
// It never invokes the target or treats diagnostic text as host authority.
package feedback

import (
	"errors"
	"slices"

	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

var ErrFeedback = errors.New("invalid native feedback or receipt binding")
var ErrRead = errors.New("feedback handle, permission or byte range is invalid")
var kinds = []string{"target_output", "operation_error", "injection_delivery", "oracle_outcome"}

type Policy struct {
	native, effective string
	allowed, selected []string
}

func rank(p string) int {
	switch p {
	case "black-box":
		return 0
	case "diagnostic":
		return 1
	case "oracle-assisted":
		return 2
	}
	return -1
}
func permitted(p, k string) bool {
	return slices.Contains(kinds, k) && (k == "target_output" || rank(p) >= 1 && k != "oracle_outcome" || rank(p) == 2)
}

// New freezes an already resolved campaign policy and per-attempt selection.
// allowed is explicit: nil and empty both permit no kinds here.
func New(native, effective string, allowed []string, selection *interceptor.ObservationSelection) (*Policy, error) {
	if rank(native) < 0 || rank(effective) < 0 || rank(effective) > rank(native) {
		return nil, ErrFeedback
	}
	seen := map[string]bool{}
	for _, k := range allowed {
		if !permitted(effective, k) || seen[k] {
			return nil, ErrFeedback
		}
		seen[k] = true
	}
	requested := kinds
	if selection != nil {
		switch selection.Mode {
		case "all-permitted":
			if len(selection.Kinds) != 0 {
				return nil, ErrFeedback
			}
		case "selected":
			if len(selection.Kinds) == 0 {
				return nil, ErrFeedback
			}
			seen := map[string]bool{}
			for _, k := range selection.Kinds {
				if !slices.Contains(kinds, k) || seen[k] {
					return nil, ErrFeedback
				}
				seen[k] = true
			}
			requested = selection.Kinds
		default:
			return nil, ErrFeedback
		}
	}
	p := &Policy{native: native, effective: effective, allowed: []string{}, selected: []string{}}
	for _, k := range kinds {
		if slices.Contains(allowed, k) {
			p.allowed = append(p.allowed, k)
			if slices.Contains(requested, k) {
				p.selected = append(p.selected, k)
			}
		}
	}
	return p, nil
}

func (p *Policy) NativeProfile() string    { return p.native }
func (p *Policy) EffectiveProfile() string { return p.effective }
func (p *Policy) Collect() bool            { return len(p.selected) > 0 }
func (p *Policy) NativeSelection() *interceptor.ObservationSelection {
	if !p.Collect() {
		return nil
	}
	return &interceptor.ObservationSelection{Mode: "selected", Kinds: slices.Clone(p.selected)}
}

type Category struct {
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

func (p *Policy) categories() []Category {
	result := []Category{}
	for _, k := range kinds {
		c := Category{k, "unavailable", "feedback_unavailable"}
		if !slices.Contains(p.allowed, k) {
			c.State, c.Reason = "withheld", "effective_feedback_policy"
		} else if !slices.Contains(p.selected, k) {
			c.State, c.Reason = "not_requested", "selection"
		}
		result = append(result, c)
	}
	return result
}
