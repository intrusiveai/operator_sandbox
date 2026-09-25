//go:build linux || darwin

package campaign

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/feedback"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

const MaxPublicationIndex = 1 << 20

type InjectionHandle struct {
	CampaignID  string `json:"campaign_id"`
	SessionID   string `json:"session_id"`
	AttemptID   string `json:"attempt_id"`
	ReceiptID   string `json:"receipt_id"`
	ActionID    string `json:"action_id"`
	InjectionID string `json:"injection_id"`
	Deleted     bool   `json:"deleted"`
}
type Publication struct {
	Receipt    *feedback.Receipt
	Injections []InjectionHandle
}
type PublicationIndex struct {
	APIVersion     string                         `json:"api_version"`
	RequestID      string                         `json:"request_id"`
	ReceiptID      string                         `json:"receipt_id"`
	IdentityDigest string                         `json:"identity_digest"`
	Feedback       json.RawMessage                `json:"feedback,omitempty"`
	Objects        map[string][]ContentDescriptor `json:"objects"`
	Injections     []InjectionHandle              `json:"injections"`
}

// ReservePublication runs before native dispatch. It reserves retained feedback,
// bounded per-entry events and the receipt index independently of native audit.
func (a *Attempts) ReservePublication(id string, maximumBytes int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.records[id]
	if !ok || maximumBytes < 0 || maximumBytes > 64*feedback.MaxArtifact {
		return ErrInvalid
	}
	if old, ok := a.publicationBytes[id]; ok {
		if old == maximumBytes {
			return nil
		}
		return ErrInvalid
	}
	if r.State != "admitted" || r.Dispatched {
		return ErrInvalid
	}
	if err := a.ready(); err != nil {
		return err
	}
	metadata, _ := encode(map[string]any{"request_id": id, "maximum_feedback_bytes": maximumBytes}, MaxMetadataBytes)
	bound := maximumBytes + MaxPublicationIndex + 65*(MaxEventBytes+1)
	if _, err := a.w.AppendReserving(Entry{RunRevision: a.revision, Kind: "attempt.publication-reserved", Metadata: metadata}, publicationReservation(id), bound); err != nil {
		return a.failure(err)
	}
	if a.publicationBytes == nil {
		a.publicationBytes = map[string]int64{}
	}
	a.publicationBytes[id] = maximumBytes
	return nil
}

// Publish stores immutable feedback and an index, then atomically adopts their
// descriptor in the final attempt result event. An interrupted staging sequence
// has no readable/public receipt. Exact duplicate publication does no writes.
func (a *Attempts) Publish(catalog *contracts.Catalog, id string, c AttemptCompletion, p Publication) error {
	if c.Outcome == "unknown" {
		a.w.fence.Stop(ErrUnknownOutcome)
	}
	if c.Outcome == "failed" {
		a.w.fence.Stop(ErrExecutionFailure)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.records[id]
	maximum, reserved := a.publicationBytes[id]
	if !ok || !reserved || catalog == nil || len(p.Injections) > 16 || len(c.NativeResult) != 0 || len(c.Receipts) != 0 {
		return ErrInvalid
	}
	if _, err := catalog.Validate(contracts.EngineAttemptResultSchema, c.Result, MaxContentBytes); err != nil {
		return ErrInvalid
	}
	var result struct {
		RequestID string          `json:"request_id"`
		AttemptID string          `json:"attempt_id"`
		ReceiptID string          `json:"receipt_id"`
		Status    string          `json:"status"`
		Feedback  json.RawMessage `json:"feedback"`
	}
	_ = json.Unmarshal(c.Result, &result)
	outcome := map[string]string{"completed": "succeeded", "failed": "failed", "unknown": "unknown"}[result.Status]
	if outcome == "" || c.Outcome != outcome || result.RequestID != id || result.AttemptID != r.AttemptID || !validID(result.ReceiptID) {
		return ErrInvalid
	}
	index := PublicationIndex{APIVersion: "operator.dev/attempt-publication/v1alpha1", RequestID: id, ReceiptID: result.ReceiptID, Objects: map[string][]ContentDescriptor{}, Injections: append([]InjectionHandle{}, p.Injections...)}
	objects := map[string][]byte{}
	if p.Receipt != nil {
		index.Feedback = p.Receipt.RecordJSON()
		record, err := feedback.OpenRecord(catalog, index.Feedback)
		if err != nil {
			return ErrInvalid
		}
		s := record.Source()
		if record.ID() != result.ReceiptID || s.CampaignID != a.w.manifest.CampaignID || s.AttemptID != r.AttemptID || s.SessionID != r.Target.SessionID || s.RunRevision != uint64(r.SubmittedRevision) || !bytes.Equal(record.ManifestJSON(), result.Feedback) {
			return ErrInvalid
		}
		for _, entry := range record.Entries() {
			if entry.Availability != "available" {
				continue
			}
			data, ok := p.Receipt.Content(entry.ID)
			if !ok || int64(len(data)) > maximum || int64(len(data)) != entry.Artifact.SizeBytes || contracts.RawDigest(data) != entry.Artifact.Digest {
				return ErrInvalid
			}
			maximum -= int64(len(data))
			objects[entry.ID] = data
		}
	} else if len(result.Feedback) > 0 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, h := range index.Injections {
		if h.CampaignID != a.w.manifest.CampaignID || h.SessionID != r.Target.SessionID || h.AttemptID != r.AttemptID || h.ReceiptID != result.ReceiptID || !validID(h.ActionID) || !validID(h.InjectionID) || seen[h.ActionID] || seen[h.InjectionID] {
			return ErrInvalid
		}
		seen[h.ActionID], seen[h.InjectionID] = true, true
		armed, deleted := false, false
		for _, step := range a.nativeSteps {
			if step.ParentID != id || step.Outcome != "succeeded" {
				continue
			}
			raw, err := a.NativeSteps().read(step.Request)
			if err != nil {
				return err
			}
			var request struct {
				Body struct {
					InjectionID string                 `json:"injection_id"`
					Definition  interceptor.Definition `json:"definition"`
				} `json:"body"`
			}
			if json.Unmarshal(raw, &request) != nil {
				return a.failure(ErrCorrupt)
			}
			armed = armed || step.Operation == "injection.arm" && request.Body.Definition.ID == h.InjectionID
			deleted = deleted || step.Operation == "injection.delete" && request.Body.InjectionID == h.InjectionID
		}
		if !armed || h.Deleted != deleted {
			return ErrInvalid
		}
	}
	// Bind repeat identity to all bytes and metadata, before assigning storage paths.
	objectDigests := map[string]string{}
	for id, data := range objects {
		objectDigests[id] = contracts.RawDigest(data)
	}
	identityRaw, _ := json.Marshal(struct {
		Index   PublicationIndex
		Result  []byte
		Objects map[string]string
	}{index, c.Result, objectDigests})
	identity := contracts.RawDigest(identityRaw)
	index.IdentityDigest = identity
	if r.Result != nil {
		stored, err := a.readPublicationLocked(r)
		if err != nil {
			return err
		}
		if stored.IdentityDigest != identity {
			return ErrInvalid
		}
		return nil
	}
	if r.Publication != nil {
		return a.failure(ErrCorrupt)
	}
	for otherID, other := range a.records {
		if otherID == id || other.Result == nil || other.Publication == nil {
			continue
		}
		stored, err := a.readPublicationLocked(other)
		if err != nil {
			return err
		}
		if stored.ReceiptID == result.ReceiptID {
			return ErrInvalid
		}
	}
	// Sort by the frozen manifest order; never depend on Go map iteration.
	if p.Receipt != nil {
		record, _ := feedback.OpenRecord(catalog, index.Feedback)
		for _, entry := range record.Entries() {
			data, ok := objects[entry.ID]
			if !ok {
				continue
			}
			parts := []Content{}
			for offset := 0; offset < len(data) || offset == 0; offset += MaxContentBytes {
				end := min(len(data), offset+MaxContentBytes)
				parts = append(parts, Content{Role: publicationRole(len(parts)), MediaType: "application/octet-stream", Bytes: data[offset:end]})
				if end == len(data) {
					break
				}
			}
			meta, _ := encode(map[string]any{"request_id": id, "entry_id": entry.ID}, MaxMetadataBytes)
			descriptors, err := a.w.AppendStored(Entry{RunRevision: a.revision, Kind: "attempt.feedback-staged", Metadata: meta, Content: parts}, publicationReservation(id), false)
			if err != nil {
				return a.failure(err)
			}
			index.Objects[entry.ID] = descriptors
		}
	}
	encoded, err := json.Marshal(index)
	if err != nil || len(encoded) > MaxPublicationIndex {
		return a.failure(ErrInvalid)
	}
	meta, _ := encode(map[string]any{"request_id": id}, MaxMetadataBytes)
	descriptors, err := a.w.AppendStored(Entry{RunRevision: a.revision, Kind: "attempt.publication-staged", Metadata: meta, Content: []Content{{Role: "publication-index", MediaType: "application/json", Bytes: encoded}}}, publicationReservation(id), true)
	if err != nil {
		return a.failure(err)
	}
	r.Publication = &descriptors[0]
	a.records[id] = r
	c.Receipts, _ = encode(map[string]any{"publication": descriptors[0], "publication_digest": identity}, MaxAttemptReceiptBytes)
	// No receipt is exposed before the final attempt result commits.
	if err = a.resolveLocked(id, c); err != nil {
		return a.failure(err)
	}
	return nil
}

func (a *Attempts) readPublicationLocked(r SavedAttempt) (PublicationIndex, error) {
	if r.Result == nil || r.Publication == nil {
		return PublicationIndex{}, ErrActive
	}
	raw, err := a.w.ReadContent(*r.Publication)
	if err != nil {
		return PublicationIndex{}, a.failure(err)
	}
	var index PublicationIndex
	if decode(raw, &index, MaxPublicationIndex) != nil || index.APIVersion != "operator.dev/attempt-publication/v1alpha1" || index.RequestID != r.RequestID || !validDigest(index.IdentityDigest) {
		return PublicationIndex{}, a.failure(ErrCorrupt)
	}
	return index, nil
}
func (a *Attempts) Publication(id string) (PublicationIndex, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.records[id]
	if !ok {
		return PublicationIndex{}, ErrInvalid
	}
	return a.readPublicationLocked(r)
}

// FindPublication resolves only adopted same-campaign receipts. It deliberately
// has no current-worker or current-revision access restriction.
func (a *Attempts) FindPublication(receipt string) (PublicationIndex, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validID(receipt) {
		return PublicationIndex{}, ErrInvalid
	}
	for _, r := range a.records {
		if r.Result == nil || r.Publication == nil {
			continue
		}
		p, err := a.readPublicationLocked(r)
		if err != nil {
			return PublicationIndex{}, err
		}
		if p.ReceiptID == receipt {
			return p, nil
		}
	}
	return PublicationIndex{}, ErrInvalid
}
func (a *Attempts) ReadPublicationObject(index PublicationIndex, entry string) ([]byte, error) {
	parts, ok := index.Objects[entry]
	if !ok || len(parts) == 0 || len(parts) > 4 {
		return nil, ErrInvalid
	}
	var raw []byte
	for i, d := range parts {
		if d.Role != fmt.Sprintf("feedback-%d", i) {
			return nil, ErrCorrupt
		}
		b, err := a.w.ReadContent(d)
		if err != nil {
			return nil, err
		}
		raw = append(raw, b...)
	}
	return raw, nil
}
