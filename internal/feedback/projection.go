package feedback

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

const MaxArtifact = 16 << 20
const MaxChunk = 256 << 10

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Source struct {
	CampaignID           string `json:"campaign_id"`
	SessionID            string `json:"session_id"`
	AttemptID            string `json:"attempt_id"`
	AttemptContextDigest string `json:"attempt_context_digest"`
	TurnID               string `json:"turn_id"`
	RunRevision          uint64 `json:"run_revision"`
	SessionRevision      uint64 `json:"session_revision"`
}
type Artifact struct {
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
	MediaType string `json:"media_type"`
}
type Entry struct {
	ID                string    `json:"entry_id"`
	Kind              string    `json:"kind"`
	Visibility        string    `json:"visibility"`
	Source            string    `json:"source"`
	Assurance         string    `json:"assurance"`
	Availability      string    `json:"availability"`
	Reason            string    `json:"reason,omitempty"`
	Artifact          *Artifact `json:"artifact,omitempty"`
	Truncated         bool      `json:"truncated"`
	OriginalSizeBytes *int64    `json:"original_size_bytes,omitempty"`
}
type Manifest struct {
	Profile         string     `json:"profile"`
	CollectionState string     `json:"collection_state"`
	Boundary        string     `json:"boundary"`
	Categories      []Category `json:"categories"`
	Entries         []Entry    `json:"entries"`
}
type retained struct {
	nativeID string
	entry    Entry
	content  []byte
}

// Receipt is an immutable projection. Persist RecordJSON and every Content value
// before publishing ManifestJSON. This object does not implement durable storage.
type Receipt struct {
	source       Source
	id, nativeID string
	manifest     []byte
	entries      []retained
}

func (r *Receipt) ManifestJSON() []byte { return bytes.Clone(r.manifest) }
func (r *Receipt) RecordJSON() []byte {
	mapping := map[string]string{}
	for _, e := range r.entries {
		mapping[e.entry.ID] = e.nativeID
	}
	raw, _ := json.Marshal(map[string]any{"receipt_id": r.id, "source": r.source, "native_receipt_id": r.nativeID, "entries": mapping, "feedback": json.RawMessage(r.manifest)})
	return raw
}
func (r *Receipt) Content(entryID string) ([]byte, bool) {
	for _, e := range r.entries {
		if e.entry.ID == entryID && e.entry.Availability == "available" {
			return bytes.Clone(e.content), true
		}
	}
	return nil, false
}

// VerifyView authenticates the native typed hash and original invocation binding.
// Native aliases are validated as framing but never used for guest data.
func VerifyView(raw []byte, source Source, p *Policy) (interceptor.ObservationView, error) {
	var v interceptor.ObservationView
	if p == nil || !p.Collect() || interceptor.DecodeTypedBody(raw, &v, 256<<10) != nil {
		return v, ErrFeedback
	}
	if v.APIVersion != "interceptor.dev/observation-view/v1alpha2" || v.Hash != interceptor.ObservationViewDigest(v) || v.ReceiptID != interceptor.FeedbackReceiptID(source.SessionID, source.TurnID) || v.CampaignID != source.CampaignID || v.SessionID != source.SessionID || v.AttemptID != source.AttemptID || v.AttemptContextDigest != source.AttemptContextDigest || v.TurnID != source.TurnID || v.FeedbackProfile != p.native || v.SessionRevision != source.SessionRevision || v.Operation.ReceiptID != source.TurnID || !slices.Contains([]string{"SUCCEEDED", "FAILED"}, v.Operation.State) || v.CapturedAt.IsZero() || v.WindowStart.IsZero() || v.WindowEnd.Before(v.WindowStart) || v.CapturedAt.Before(v.WindowEnd) || !slices.Contains([]string{"complete", "partial"}, v.CollectionState) || len(v.Entries) > 64 || len(v.Categories) > 4 || len(v.Observations) > 64 {
		return v, ErrFeedback
	}
	seen := map[string]bool{}
	for _, c := range v.Categories {
		if !slices.Contains(kinds, c.Kind) || seen[c.Kind] || !slices.Contains([]string{"available", "empty", "withheld", "not_requested", "unavailable", "partial"}, c.State) {
			return v, ErrFeedback
		}
		seen[c.Kind] = true
	}
	seen = map[string]bool{}
	for _, e := range v.Entries {
		if !idPattern.MatchString(e.ID) || seen[e.ID] || !slices.Contains(kinds, e.Kind) || !slices.Contains([]interceptor.Visibility{interceptor.TargetVisible, interceptor.HarnessVisible, interceptor.OperatorOnly, interceptor.Protected}, e.Visibility) || !slices.Contains([]string{"available", "unavailable"}, e.Availability) || e.OriginalSizeBytes < 0 {
			return v, ErrFeedback
		}
		seen[e.ID] = true
		if e.Availability == "available" && e.Artifact == nil {
			return v, ErrFeedback
		}
		if a := e.Artifact; a != nil {
			if !digestPattern.MatchString(a.Digest) || a.SizeBytes < 0 || a.SizeBytes > MaxArtifact || len(a.MediaType) == 0 || len(a.MediaType) > 128 || a.Canonicalization != "raw" || e.OriginalSizeBytes < a.SizeBytes || e.Truncated && e.OriginalSizeBytes <= a.SizeBytes || !e.Truncated && e.OriginalSizeBytes != a.SizeBytes {
				return v, ErrFeedback
			}
		}
	}
	for _, alias := range v.Observations {
		if !slices.Contains(kinds, alias.Kind) {
			return v, ErrFeedback
		}
	}
	return v, nil
}

// Project uses only permitted, selected, visible entries. content contains fully
// verified native artifacts keyed by entry ID; absence means unavailable. The
// aggregate byte ceiling bounds local retention independently of native limits.
// actionHandles maps known native injection IDs to submitted action IDs.
func Project(catalog *contracts.Catalog, p *Policy, source Source, receiptID string, raw []byte, content map[string][]byte, actionHandles map[string]string, maximumBytes int64) (*Receipt, error) {
	if catalog == nil || p == nil || !idPattern.MatchString(receiptID) || maximumBytes < 0 || maximumBytes > 64*MaxArtifact || source.RunRevision == 0 || source.RunRevision > contracts.MaxSafeInteger || source.SessionRevision == 0 || !digestPattern.MatchString(source.AttemptContextDigest) {
		return nil, ErrFeedback
	}
	for _, id := range []string{source.CampaignID, source.SessionID, source.AttemptID, source.TurnID} {
		if !idPattern.MatchString(id) {
			return nil, ErrFeedback
		}
	}
	m := Manifest{p.effective, "complete", "invocation-completed", p.categories(), []Entry{}}
	r := &Receipt{source: source, id: receiptID, entries: []retained{}}
	var v interceptor.ObservationView
	if p.Collect() {
		var err error
		v, err = VerifyView(raw, source, p)
		if err != nil {
			return nil, err
		}
		r.nativeID = v.ReceiptID
	} else if len(raw) != 0 || len(content) != 0 {
		return nil, ErrFeedback
	}
	for i := range m.Categories {
		c := &m.Categories[i]
		if !slices.Contains(p.selected, c.Kind) {
			continue
		}
		for _, native := range v.Categories {
			if native.Kind != c.Kind {
				continue
			}
			switch native.State {
			case "empty":
				c.State, c.Reason = "empty", safeCategoryReason(native.Reason)
			case "available":
				c.State, c.Reason = "available", ""
			case "partial":
				c.State, c.Reason = "partial", "capture_partial"
			default:
				c.State, c.Reason = "unavailable", "feedback_unavailable"
			}
		}
		count := 0
		for _, native := range v.Entries {
			if native.Kind != c.Kind || (native.Visibility != interceptor.TargetVisible && native.Visibility != interceptor.HarnessVisible) {
				continue
			}
			count++
			e := Entry{ID: handle("entry", receiptID, native.ID), Kind: c.Kind, Visibility: string(native.Visibility), Source: "interceptor", Assurance: "recorded-attributed-fact", Availability: "unavailable", Reason: "content_unavailable", Truncated: native.Truncated}
			if c.Kind == "target_output" {
				e.Source, e.Assurance = "application", "target-response"
			}
			var data []byte
			if b, ok := content[native.ID]; ok && native.Availability == "available" && native.Artifact != nil {
				if int64(len(b)) != native.Artifact.SizeBytes || contracts.RawDigest(b) != native.Artifact.Digest {
					return nil, ErrFeedback
				}
				var err error
				data, err = normalize(c.Kind, b, native.Truncated, actionHandles, source.SessionID)
				if err != nil {
					return nil, err
				}
				if data != nil && int64(len(data)) <= maximumBytes {
					maximumBytes -= int64(len(data))
					media := "application/json"
					if c.Kind == "target_output" {
						media = native.Artifact.MediaType
					}
					e.Availability, e.Reason = "available", ""
					e.Artifact = &Artifact{contracts.RawDigest(data), int64(len(data)), media}
					if c.Kind == "target_output" {
						n := native.OriginalSizeBytes
						e.OriginalSizeBytes = &n
					}
				} else {
					data = nil
					e.Reason = "capture_limit"
				}
			}
			if e.Availability != "available" || e.Truncated {
				c.State, c.Reason = "partial", "capture_partial"
			}
			m.Entries = append(m.Entries, e)
			r.entries = append(r.entries, retained{native.ID, e, bytes.Clone(data)})
		}
		if count == 0 && c.State == "available" {
			c.State, c.Reason = "unavailable", "feedback_unavailable"
		}
		if count > 0 && c.State == "empty" {
			return nil, ErrFeedback
		}
		if c.State == "unavailable" || c.State == "partial" {
			m.CollectionState = "partial"
		}
	}
	var err error
	r.manifest, err = json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if _, err = catalog.Validate("urn:operator:schema:feedback-manifest:v1alpha1", r.manifest, contracts.OrdinaryLimit); err != nil {
		return nil, err
	}
	return r, nil
}

func handle(prefix string, parts ...string) string {
	raw, _ := json.Marshal(parts)
	return prefix + "-" + contracts.RawDigest(raw)[7:]
}
func safeCategoryReason(s string) string {
	switch s {
	case "":
		return ""
	case "no_recorded_observation_not_proof_of_absence", "no_positive_attributed_result_not_proof_of_failure":
		return s
	default:
		return "no_recorded_feedback"
	}
}

func normalize(kind string, b []byte, truncated bool, actions map[string]string, session string) ([]byte, error) {
	if kind == "target_output" {
		return append([]byte{}, b...), nil
	}
	if truncated {
		return nil, nil
	} // partial JSON is never presented as a fact
	v, err := contracts.Decode(b, MaxArtifact)
	if err != nil {
		return nil, ErrFeedback
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrFeedback
	}
	var result any
	switch kind {
	case "operation_error":
		if m["code"] != "APPLICATION_OPERATION_FAILED" || m["failed"] != true {
			return nil, ErrFeedback
		}
		result = map[string]any{"code": "APPLICATION_OPERATION_FAILED", "failed": true}
	case "injection_delivery":
		id, _ := m["injection_id"].(string)
		action := actions[id]
		state, _ := m["state"].(string)
		if !idPattern.MatchString(action) || !slices.Contains([]string{"applied", "failed", "unknown"}, state) {
			return nil, ErrFeedback
		}
		r := map[string]any{"action_id": action, "state": state}
		if state == "unknown" {
			r["reason"] = "no_confirmed_delivery_in_window"
		} else {
			if m["scope"] != "turn" && m["scope"] != "attempt" {
				return nil, ErrFeedback
			}
			r["scope"] = m["scope"]
		}
		result = r
	case "oracle_outcome":
		id, _ := m["oracle_id"].(string)
		k, _ := m["oracle_kind"].(string)
		if id == "" || len(id) > 256 || m["fired"] != true || !slices.Contains([]string{"attacker_sink_match", "output_contains", "model_output_contains", "tool_call_match", "event_match"}, k) {
			return nil, ErrFeedback
		}
		result = map[string]any{"detector_id": handle("detector", session, id), "detector_kind": k, "fired": true}
	default:
		return nil, ErrFeedback
	}
	return json.Marshal(result)
}
