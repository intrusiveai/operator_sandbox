package feedback

import (
	"encoding/json"
	"slices"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// Record is a validated, immutable receipt index. It contains no execution grant
// or content cache. Its loader is supplied by trusted campaign storage.
type Record struct {
	SourceAdapter   string            `json:"source_adapter,omitempty"`
	ReceiptID       string            `json:"receipt_id"`
	Source          Source            `json:"source"`
	NativeReceiptID string            `json:"native_receipt_id"`
	Entries         map[string]string `json:"entries"`
	Feedback        json.RawMessage   `json:"feedback"`
}
type Retained struct {
	record   Record
	manifest Manifest
}

func OpenRecord(catalog *contracts.Catalog, raw []byte) (*Retained, error) {
	var r Record
	if catalog == nil || interceptor.DecodeTypedBody(raw, &r, 512<<10) != nil || !idPattern.MatchString(r.ReceiptID) || r.Source.RunRevision == 0 || r.Source.SessionRevision == 0 || !digestPattern.MatchString(r.Source.AttemptContextDigest) {
		return nil, ErrFeedback
	}
	for _, s := range []string{r.Source.CampaignID, r.Source.SessionID, r.Source.AttemptID, r.Source.TurnID} {
		if !idPattern.MatchString(s) {
			return nil, ErrFeedback
		}
	}
	if r.SourceAdapter != "" && r.SourceAdapter != "https/v1" {
		return nil, ErrFeedback
	}
	if r.SourceAdapter == "https/v1" && r.NativeReceiptID != "" {
		return nil, ErrFeedback
	}
	if r.NativeReceiptID != "" && r.NativeReceiptID != interceptor.FeedbackReceiptID(r.Source.SessionID, r.Source.TurnID) {
		return nil, ErrFeedback
	}
	if _, err := catalog.Validate(contracts.FeedbackManifestSchema, r.Feedback, contracts.OrdinaryLimit); err != nil {
		return nil, ErrFeedback
	}
	var m Manifest
	_ = json.Unmarshal(r.Feedback, &m)
	states := map[string]string{}
	for _, c := range m.Categories {
		if states[c.Kind] != "" {
			return nil, ErrFeedback
		}
		states[c.Kind] = c.State
	}
	seen := map[string]bool{}
	for _, e := range m.Entries {
		if r.SourceAdapter == "https/v1" && (e.Source != "https" || e.Assurance != "declared-observer" || (e.Kind != "target_output" && e.Kind != "operation_error")) {
			return nil, ErrFeedback
		}
		if seen[e.ID] || !idPattern.MatchString(r.Entries[e.ID]) || !permitted(m.Profile, e.Kind) || !slices.Contains([]string{"available", "partial", "unavailable"}, states[e.Kind]) || e.Availability == "unavailable" && e.Artifact != nil {
			return nil, ErrFeedback
		}
		seen[e.ID] = true
	}
	if len(seen) != len(r.Entries) || len(seen) > 0 && r.NativeReceiptID == "" && r.SourceAdapter != "https/v1" {
		return nil, ErrFeedback
	}
	return &Retained{r, m}, nil
}
func (r *Retained) ID() string           { return r.record.ReceiptID }
func (r *Retained) Source() Source       { return r.record.Source }
func (r *Retained) ManifestJSON() []byte { return slices.Clone(r.record.Feedback) }
func (r *Retained) Entries() []Entry {
	raw, _ := json.Marshal(r.manifest.Entries)
	var out []Entry
	_ = json.Unmarshal(raw, &out)
	return out
}

// Read verifies a complete retained artifact before slicing. Missing/corrupt
// optional content yields explicit unavailability; malformed receipt metadata
// is rejected by OpenRecord. Policy checks precede all content access.
func (r *Retained) Read(campaign, receipt, entry string, offset int64, maximum int, admitted bool, allowed []string, load func(string, Artifact) ([]byte, error)) ([]byte, error) {
	if !admitted || campaign != r.record.Source.CampaignID || receipt != r.record.ReceiptID || offset < 0 || maximum < 1 || maximum > MaxChunk {
		return nil, ErrRead
	}
	for _, e := range r.manifest.Entries {
		if e.ID != entry {
			continue
		}
		if !slices.Contains(allowed, e.Kind) {
			return nil, ErrRead
		}
		result := ReadResult{ReceiptID: receipt, EntryID: entry, Offset: offset, Content: []byte{}, Availability: e.Availability, Reason: e.Reason, Truncated: e.Truncated}
		if e.Availability == "available" {
			if offset > e.Artifact.SizeBytes {
				return nil, ErrRead
			}
			if load == nil {
				return nil, ErrRead
			}
			data, err := load(e.ID, *e.Artifact)
			if err != nil || int64(len(data)) != e.Artifact.SizeBytes || contracts.RawDigest(data) != e.Artifact.Digest {
				result.Availability = "unavailable"
				result.Reason = "content_missing_or_corrupt"
			} else {
				end := min(int64(len(data)), offset+int64(maximum))
				result.Content = append([]byte{}, data[offset:end]...)
				result.RawLength = len(result.Content)
				result.EOF = end == int64(len(data))
				result.Artifact = e.Artifact
			}
		}
		return json.Marshal(result)
	}
	return nil, ErrRead
}
