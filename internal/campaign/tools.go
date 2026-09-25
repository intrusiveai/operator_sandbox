//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"errors"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

var ErrConflict = errors.New("operation identity conflicts with saved command")
var ErrFeedbackBudget = errors.New("campaign feedback allowance exhausted")

// Tools shares the attempt ledger's lock, namespace, target binding and limits.
// It cannot create attempt admissions or resume work from a recovered journal.
type Tools struct{ a *Attempts }

func (a *Attempts) Tools() *Tools { return &Tools{a} }

type ToolInput struct {
	CampaignID, OperationID, Operation, WorkerInstanceID string
	RunRevision                                          int64
	Body                                                 []byte
}
type SavedTool struct {
	ID               string             `json:"id"`
	Operation        string             `json:"operation"`
	IdentityDigest   string             `json:"identity_digest"`
	Target           TargetBinding      `json:"target"`
	RunRevision      int64              `json:"run_revision"`
	WorkerInstanceID string             `json:"worker_instance_id"`
	Body             json.RawMessage    `json:"body"`
	InjectionID      string             `json:"injection_id,omitempty"`
	ReadReserved     int64              `json:"read_reserved"`
	ReadCharged      bool               `json:"read_charged"`
	Result           *ContentDescriptor `json:"result,omitempty"`
	ResultDigest     string             `json:"result_digest,omitempty"`
}

func cloneTool(r SavedTool) SavedTool {
	r.Body = append(json.RawMessage{}, r.Body...)
	if r.Result != nil {
		d := *r.Result
		r.Result = &d
	}
	return r
}
func toolReservation(id string) string { return "tool:" + contracts.RawDigest([]byte(id))[7:] }
func (t *Tools) commit(r SavedTool, kind string, result []byte, reserve, release bool) ([]ContentDescriptor, error) {
	a := t.a
	metadata, err := encode(map[string]any{"tool": r, "read_requests": a.readRequests, "read_bytes": a.readBytes}, MaxMetadataBytes)
	if err != nil {
		return nil, a.failure(err)
	}
	entry := Entry{RunRevision: a.revision, Kind: kind, Metadata: metadata}
	if result != nil {
		entry.Content = []Content{{Role: "tool-result", MediaType: "application/json", Bytes: result}}
	}
	if reserve {
		_, err = a.w.AppendReserving(entry, toolReservation(r.ID), MaxContentBytes+4*(MaxEventBytes+1))
		if err != nil {
			return nil, a.failure(err)
		}
		return nil, nil
	}
	parts, err := a.w.AppendStored(entry, toolReservation(r.ID), release)
	if err != nil {
		return nil, a.failure(err)
	}
	return parts, nil
}
func (t *Tools) Observe(in ToolInput) (SavedTool, bool, error) {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if in.CampaignID != a.w.manifest.CampaignID || !validID(in.OperationID) || !validID(in.WorkerInstanceID) || in.RunRevision < 0 || in.RunRevision > contracts.MaxSafeInteger || (in.Operation != "engine.observation_read" && in.Operation != "engine.injection_delete") || !validJSONObject(in.Body, 4096) {
		return SavedTool{}, false, ErrInvalid
	}
	if _, ok := a.records[in.OperationID]; ok {
		return SavedTool{}, false, ErrConflict
	}
	target := a.target
	old, exists := a.tools[in.OperationID]
	if exists {
		target = old.Target
	}
	raw, _ := encode(map[string]any{"campaign_id": in.CampaignID, "operation_id": in.OperationID, "operation": in.Operation, "body": json.RawMessage(in.Body), "target": map[string]any{"adapter": target.Adapter, "session_id": target.SessionID, "capability_source_digest": target.CapabilitySourceDigest, "capability_projection_digest": target.CapabilityProjectionDigest, "native_feedback_profile": target.NativeFeedbackProfile}}, MaxMetadataBytes)
	digest := contracts.RawDigest(raw)
	if exists {
		if digest != old.IdentityDigest {
			return SavedTool{}, false, ErrConflict
		}
		return cloneTool(old), true, nil
	}
	if err := a.ready(); err != nil {
		return SavedTool{}, false, err
	}
	if in.RunRevision != a.revision {
		return SavedTool{}, false, ErrInvalid
	}
	if int64(len(a.records)+len(a.tools)) >= a.maximumSubmissions {
		return SavedTool{}, false, contracts.ErrLimit
	}
	r := SavedTool{ID: in.OperationID, Operation: in.Operation, IdentityDigest: digest, Target: target, RunRevision: in.RunRevision, WorkerInstanceID: in.WorkerInstanceID, Body: append(json.RawMessage{}, in.Body...)}
	if _, err := t.commit(r, "tool.observed", nil, true, false); err != nil {
		return SavedTool{}, false, err
	}
	if a.tools == nil {
		a.tools = map[string]SavedTool{}
	}
	a.tools[r.ID] = r
	return cloneTool(r), false, nil
}
func (t *Tools) Lookup(id string) (SavedTool, []byte, error) {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.tools[id]
	if !ok {
		return SavedTool{}, nil, ErrInvalid
	}
	if r.Result == nil {
		return cloneTool(r), nil, nil
	}
	raw, err := a.w.ReadContent(*r.Result)
	if err != nil {
		return SavedTool{}, nil, a.failure(err)
	}
	return cloneTool(r), raw, nil
}

// ReserveRead charges one new operation and its maximum range before disk I/O.
// Finish settles actual delivery; lost storage retains the reservation and fences.
func (t *Tools) ReserveRead(id string, maximum int64) error {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.tools[id]
	if !ok || r.Operation != "engine.observation_read" || r.Result != nil || r.ReadCharged || maximum < 1 || maximum > 262144 {
		return ErrInvalid
	}
	if err := a.ready(); err != nil {
		return err
	}
	var limits struct {
		Reads int64 `json:"observation_reads"`
		Bytes int64 `json:"observation_bytes"`
	}
	_ = json.Unmarshal(a.w.manifest.RemainingLimits, &limits)
	pending := int64(0)
	for _, tool := range a.tools {
		pending += tool.ReadReserved
	}
	if a.readRequests >= limits.Reads || maximum > limits.Bytes-a.readBytes-pending {
		return ErrFeedbackBudget
	}
	r.ReadReserved = maximum
	r.ReadCharged = true
	a.readRequests++
	if _, err := t.commit(r, "tool.read-reserved", nil, false, false); err != nil {
		return err
	}
	a.tools[id] = r
	return nil
}

// AuthorizeCleanup resolves only an adopted same-campaign successful arm. Old
// deletion flags are historical and never authorize a shortcut after restore.
func (t *Tools) AuthorizeCleanup(id string) (InjectionHandle, error) {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.tools[id]
	if !ok || r.Operation != "engine.injection_delete" || r.Result != nil {
		return InjectionHandle{}, ErrInvalid
	}
	if err := a.ready(); err != nil {
		return InjectionHandle{}, err
	}
	var body struct {
		Receipt string `json:"attempt_receipt_id"`
		Action  string `json:"action_id"`
	}
	if interceptor.DecodeTypedBody(r.Body, &body, 4096) != nil {
		return InjectionHandle{}, ErrInvalid
	}
	for _, attempt := range a.records {
		if attempt.Result == nil || attempt.Publication == nil {
			continue
		}
		p, err := a.readPublicationLocked(attempt)
		if err != nil {
			return InjectionHandle{}, err
		}
		if p.ReceiptID != body.Receipt {
			continue
		}
		for _, h := range p.Injections {
			if h.ActionID != body.Action {
				continue
			}
			if r.InjectionID != "" {
				if r.InjectionID != h.InjectionID {
					return InjectionHandle{}, a.failure(ErrCorrupt)
				}
				return h, nil
			}
			r.InjectionID = h.InjectionID
			if _, err := t.commit(r, "tool.cleanup-authorized", nil, false, false); err != nil {
				return InjectionHandle{}, err
			}
			a.tools[id] = r
			return h, nil
		}
	}
	return InjectionHandle{}, ErrInvalid
}

// Finish stores a schema-checked {result:...} or {error:...} payload. The broker
// owns schema/correlation validation; the ledger owns durability and read charges.
func (t *Tools) Finish(id string, payload []byte, actual int64) error {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.tools[id]
	if !ok || !validJSONObject(payload, MaxContentBytes) || actual < 0 {
		return ErrInvalid
	}
	digest := contracts.RawDigest(payload)
	if r.Result != nil {
		if r.ResultDigest == digest {
			return nil
		}
		return ErrConflict
	}
	if actual > r.ReadReserved {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(payload, &fields)
	_, success := fields["result"]
	_, failure := fields["error"]
	if success == failure || len(fields) != 1 {
		return ErrInvalid
	}
	for _, step := range a.nativeSteps {
		if step.ParentID == id && success && (step.State != ResultCommitted || step.Outcome != "succeeded") {
			return ErrActive
		}
	}
	r.ResultDigest = digest
	r.ReadReserved = 0
	a.readBytes += actual
	parts, err := t.commit(r, "tool.resolved", payload, false, true)
	if err != nil {
		a.readBytes -= actual
		return err
	}
	r.Result = &parts[0]
	a.tools[id] = r
	return nil
}

type ReadUsage struct{ Requests, Bytes, Reserved int64 }

func (t *Tools) Usage() ReadUsage {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	u := ReadUsage{Requests: a.readRequests, Bytes: a.readBytes}
	for _, r := range a.tools {
		u.Reserved += r.ReadReserved
	}
	return u
}
