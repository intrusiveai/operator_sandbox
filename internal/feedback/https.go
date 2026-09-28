package feedback

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"slices"
)

// HTTPS projects host-observed application feedback without a native receipt or
// any claim of remote oracle authority. Empty content may be an available result.
func HTTPS(c *contracts.Catalog, p *Policy, s Source, id string, content []byte, media, code string, complete, truncated bool, maximum int64) (*Receipt, error) {
	if c == nil || p == nil || maximum < 0 || maximum > 64*MaxArtifact {
		return nil, ErrFeedback
	}
	r := &Receipt{source: s, id: id, sourceAdapter: "https/v1", entries: []retained{}}
	m := Manifest{p.effective, "complete", "request-ended", p.categories(), []Entry{}}

	for i := range m.Categories {
		cat := &m.Categories[i]
		if !slices.Contains(p.selected, cat.Kind) {
			continue
		}
		var raw []byte
		mt := "application/json"
		available := true
		switch cat.Kind {
		case "target_output":
			raw = content
			mt = media
			available = complete
		case "operation_error":
			if code == "" {
				cat.State = "empty"
				cat.Reason = ""
				continue
			}
			raw, _ = json.Marshal(map[string]string{"code": code})
		default:
			cat.State = "unavailable"
			cat.Reason = "unsupported_by_adapter"
			continue
		}
		e := Entry{ID: handle("entry", id, cat.Kind), Kind: cat.Kind, Visibility: "harness_visible", Source: "https", Assurance: "declared-observer", Availability: "unavailable", Reason: "content_unavailable", Truncated: truncated && cat.Kind == "target_output"}
		if available && int64(len(raw)) <= maximum {
			maximum -= int64(len(raw))
			e.Availability = "available"
			e.Reason = ""
			e.Artifact = &Artifact{contracts.RawDigest(raw), int64(len(raw)), mt}
			cat.State = "available"
			cat.Reason = ""
		} else {
			raw = nil
			cat.State = "partial"
			cat.Reason = "capture_partial"
			m.CollectionState = "partial"
			if available {
				e.Reason = "capture_limit"
			}
		}
		m.Entries = append(m.Entries, e)
		r.entries = append(r.entries, retained{cat.Kind, e, bytes.Clone(raw)})
	}
	var err error
	r.manifest, err = json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if _, err = OpenRecord(c, r.RecordJSON()); err != nil {
		return nil, err
	}
	return r, nil
}
