//go:build linux || darwin

package campaign

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

var ErrExecutionFailure = errors.New("native execution failed; execution closed")

// Each step reserves dispatch/result plus ONE reporting-only reconciliation.
// Native frames can exceed a journal content member; retain them in ordered parts.
const NativeStepReservation = 5*(MaxEventBytes+1) + 2*interceptor.JSONLimit + (64 << 10)

type NativeStep struct {
	ID                     string              `json:"id"`
	ParentID               string              `json:"parent_id"`
	IdentityDigest         string              `json:"identity_digest"`
	SessionID              string              `json:"session_id"`
	OperationID            string              `json:"operation_id"`
	Operation              string              `json:"operation"`
	WorkerInstanceID       string              `json:"worker_instance_id"`
	RunRevision            int64               `json:"run_revision"`
	State                  string              `json:"state"`
	Outcome                string              `json:"outcome,omitempty"`
	CompletionDigest       string              `json:"completion_digest,omitempty"`
	Request                []ContentDescriptor `json:"request"`
	Response               []ContentDescriptor `json:"response"`
	ReconciliationRequest  []ContentDescriptor `json:"reconciliation_request"`
	ReconciliationResponse []ContentDescriptor `json:"reconciliation_response"`
	ReconciliationIdentity string              `json:"reconciliation_identity,omitempty"`
	ReconciliationState    string              `json:"reconciliation_state,omitempty"`
	ReconciliationDigest   string              `json:"reconciliation_digest,omitempty"`
}

// NativeSteps shares its owning Attempts lock and records. Multiple handles do
// not create independent dispatch grants. There is no recovery/resume constructor.
type NativeSteps struct{ a *Attempts }

func (a *Attempts) NativeSteps() *NativeSteps { return &NativeSteps{a: a} }
func (s *NativeSteps) Fence() *Fence          { return s.a.w.Fence() }

func cloneStep(r NativeStep) NativeStep {
	r.Request = append([]ContentDescriptor{}, r.Request...)
	r.Response = append([]ContentDescriptor{}, r.Response...)
	r.ReconciliationRequest = append([]ContentDescriptor{}, r.ReconciliationRequest...)
	r.ReconciliationResponse = append([]ContentDescriptor{}, r.ReconciliationResponse...)
	return r
}
func stepID(p interceptor.PreparedOperation) string {
	q := p.Request()
	raw, _ := encode([]string{q.CampaignID, q.SessionID, q.OperationID}, MaxMetadataBytes)
	return "native:" + contracts.RawDigest(raw)[7:]
}
func nativeParts(raw []byte, role string) []Content {
	out := []Content{}
	for len(raw) > 0 {
		size := min(len(raw), MaxContentBytes)
		out = append(out, Content{Role: fmt.Sprintf("%s-%d", role, len(out)), MediaType: "application/octet-stream", Bytes: bytes.Clone(raw[:size])})
		raw = raw[size:]
	}
	return out
}
func descriptors(parts []Content, revision, sequence int64) []ContentDescriptor {
	out := []ContentDescriptor{}
	for i, p := range parts {
		out = append(out, ContentDescriptor{p.Role, p.MediaType, contentPath(revision, sequence, i), int64(len(p.Bytes)), contracts.RawDigest(p.Bytes)})
	}
	return out
}
func (s *NativeSteps) commit(r NativeStep, kind string, mark *OperationMark, parts []Content, reserve, release bool) ([]ContentDescriptor, error) {
	a := s.a
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	meta, err := encode(map[string]any{"native_step": r}, MaxMetadataBytes)
	if err != nil {
		return nil, a.failure(err)
	}
	action := "consume"
	size := int64(0)
	if reserve {
		action = "reserve"
		size = NativeStepReservation
	}
	entry := Entry{RunRevision: a.revision, Kind: kind, Operation: mark, Metadata: meta, Content: parts}
	if _, err = a.w.appendLocked(entry, &reservationChange{ID: r.ID, Action: action, Bytes: size, Release: release}); err != nil {
		return nil, a.failure(err)
	}
	return descriptors(parts, a.revision, a.w.head.Sequence), nil
}
func (s *NativeSteps) read(parts []ContentDescriptor) ([]byte, error) {
	a := s.a
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if a.w.closed {
		return nil, ErrClosed
	}
	var raw []byte
	for _, d := range parts {
		b, err := readFile(a.w.root, d.Path, MaxContentBytes)
		if err != nil || int64(len(b)) != d.SizeBytes || contracts.RawDigest(b) != d.Digest || len(raw)+len(b) > interceptor.JSONLimit {
			return nil, a.failure(ErrCorrupt)
		}
		raw = append(raw, b...)
	}
	return raw, nil
}

// Begin commits the exact native request and capacity reservation. It requires an
// already admitted/dispatched parent plan. This is bookkeeping, not authorization.
// Existing commands return replay=true across worker/revision changes; no repeat
// call obtains another dispatch grant. Changed actual commands conflict.
func (s *NativeSteps) Begin(parent string, p interceptor.PreparedOperation) (NativeStep, bool, error) {
	a := s.a
	a.mu.Lock()
	defer a.mu.Unlock()
	q := p.Request()
	id := stepID(p)
	identity := p.CommandFingerprint()
	if identity == "" || q.CampaignID != a.w.manifest.CampaignID || q.RunRevision > contracts.MaxSafeInteger {
		return NativeStep{}, false, ErrInvalid
	}
	if old, ok := a.nativeSteps[id]; ok {
		if old.ParentID != parent || old.IdentityDigest != identity {
			return NativeStep{}, false, ErrInvalid
		}
		return cloneStep(old), true, nil
	}
	if err := a.ready(); err != nil {
		return NativeStep{}, false, err
	}
	owner, ok := a.records[parent]
	if !ok || owner.State != "admitted" || !owner.Dispatched || q.SessionID != a.target.SessionID || q.SessionID != owner.Target.SessionID || q.RunRevision != uint64(a.revision) || q.AttemptID != "" && q.AttemptID != owner.AttemptID {
		return NativeStep{}, false, ErrInvalid
	}
	// One native step at a time, including unresolved requests. A partial plan must
	// never advance while the prior step's effect is still unknown.
	for _, r := range a.nativeSteps {
		if r.State == IntentCommitted || r.State == Dispatched {
			return NativeStep{}, false, ErrActive
		}
	}
	r := NativeStep{ID: id, ParentID: parent, IdentityDigest: identity, SessionID: q.SessionID, OperationID: q.OperationID, Operation: q.Operation, WorkerInstanceID: q.WorkerInstanceID, RunRevision: int64(q.RunRevision), State: IntentCommitted}
	parts, err := s.commit(r, "native.intent", &OperationMark{id, identity, IntentCommitted}, nativeParts(p.Bytes(), "native-request"), true, false)
	if err != nil {
		return NativeStep{}, false, err
	}
	r.Request = parts
	a.nativeSteps[id] = r
	if err := a.ready(); err != nil {
		return NativeStep{}, false, err
	}
	return cloneStep(r), false, nil
}
func (s *NativeSteps) MarkDispatched(id string) (bool, error) {
	a := s.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.nativeSteps[id]
	if !ok {
		return false, ErrInvalid
	}
	if r.State != IntentCommitted {
		return false, nil
	}
	if err := a.ready(); err != nil {
		return false, err
	}
	r.State = Dispatched
	if _, err := s.commit(r, "native.dispatched", &OperationMark{id, r.IdentityDigest, Dispatched}, nil, false, false); err != nil {
		return false, err
	}
	a.nativeSteps[id] = r
	if err := a.ready(); err != nil {
		return false, err
	}
	return true, nil
}

// Resolve receives an adapter-verified outcome: succeeded, failed, unknown, or
// not_dispatched. Failures/uncertainty fence BEFORE waiting on journal locks.
// Exact native envelopes are retained; malformed/oversized replies have no trusted
// envelope and are represented by unknown with nil response.
func (s *NativeSteps) Resolve(id, outcome string, response []byte) error {
	a := s.a
	if outcome == "unknown" {
		a.w.fence.Stop(ErrUnknownOutcome)
	} else if outcome == "failed" || outcome == "not_dispatched" {
		a.w.fence.Stop(ErrExecutionFailure)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.nativeSteps[id]
	if !ok {
		return ErrInvalid
	}
	if outcome != "succeeded" && outcome != "failed" && outcome != "unknown" && outcome != "not_dispatched" {
		return ErrInvalid
	}
	if len(response) > 0 {
		if _, err := interceptor.ParseResponse(response); err != nil {
			return a.failure(ErrInvalid)
		}
	} else if outcome == "succeeded" || outcome == "failed" {
		return a.failure(ErrInvalid)
	}
	if outcome == "not_dispatched" && len(response) > 0 {
		return a.failure(ErrInvalid)
	}
	completion, _ := encode(map[string]any{"outcome": outcome, "response_digest": contracts.RawDigest(response)}, MaxMetadataBytes)
	digest := contracts.RawDigest(completion)
	if r.CompletionDigest != "" {
		if r.CompletionDigest == digest {
			return nil
		}
		return ErrInvalid
	}
	if r.State != Dispatched && outcome != "not_dispatched" {
		return ErrInvalid
	}
	r.Outcome = outcome
	r.CompletionDigest = digest
	r.State = ResultCommitted
	if outcome == "unknown" {
		r.State = Unknown
	}
	parts, err := s.commit(r, "native.resolved", &OperationMark{id, r.IdentityDigest, r.State}, nativeParts(response, "native-response"), false, outcome != "unknown")
	if err != nil {
		return err
	}
	r.Response = parts
	a.nativeSteps[id] = r
	return nil
}
func (s *NativeSteps) Lookup(id string) (NativeStep, interceptor.PreparedOperation, []byte, error) {
	a := s.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.nativeSteps[id]
	if !ok {
		return NativeStep{}, interceptor.PreparedOperation{}, nil, ErrInvalid
	}
	raw, err := s.read(r.Request)
	if err != nil {
		return NativeStep{}, interceptor.PreparedOperation{}, nil, err
	}
	p, err := interceptor.ParsePreparedOperation(raw)
	if err != nil {
		return NativeStep{}, p, nil, a.failure(ErrCorrupt)
	}
	response, err := s.read(r.Response)
	return cloneStep(r), p, response, err
}

// BeginReconciliation commits ONE read-only operation.status query for an unknown
// step, spending capacity reserved before its original effect. It is permitted
// after terminal fencing; it cannot create a fresh effect or change the outcome.
func (s *NativeSteps) BeginReconciliation(id string, p interceptor.PreparedOperation) (bool, error) {
	a := s.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.nativeSteps[id]
	if !ok || r.Outcome != "unknown" || len(p.Bytes()) > 64<<10 {
		return false, ErrInvalid
	}
	originalBytes, err := s.read(r.Request)
	if err != nil {
		return false, err
	}
	original, err := interceptor.ParsePreparedOperation(originalBytes)
	if err != nil {
		return false, a.failure(ErrCorrupt)
	}
	expected, err := interceptor.PrepareOperationStatus(p.Request(), original)
	if err != nil || expected.CommandFingerprint() != p.CommandFingerprint() {
		return false, ErrInvalid
	}
	if r.ReconciliationState != "" {
		if r.ReconciliationIdentity != p.CommandFingerprint() {
			return false, ErrInvalid
		}
		return false, nil
	}
	r.ReconciliationIdentity = p.CommandFingerprint()
	r.ReconciliationState = Dispatched
	parts, err := s.commit(r, "native.reconciliation-dispatched", nil, nativeParts(p.Bytes(), "reconciliation-request"), false, false)
	if err != nil {
		return false, err
	}
	r.ReconciliationRequest = parts
	a.nativeSteps[id] = r
	return true, nil
}
func (s *NativeSteps) ResolveReconciliation(id string, response []byte) error {
	a := s.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.nativeSteps[id]
	if !ok || r.ReconciliationState == "" {
		return ErrInvalid
	}
	if len(response) > 0 {
		if _, err := interceptor.ParseResponse(response); err != nil {
			return a.failure(ErrInvalid)
		}
	}
	digest := contracts.RawDigest(response)
	if r.ReconciliationDigest != "" {
		if r.ReconciliationDigest == digest {
			return nil
		}
		return ErrInvalid
	}
	r.ReconciliationDigest = digest
	r.ReconciliationState = ResultCommitted
	parts, err := s.commit(r, "native.reconciliation-result", nil, nativeParts(response, "reconciliation-response"), false, true)
	if err != nil {
		return err
	}
	r.ReconciliationResponse = parts
	a.nativeSteps[id] = r
	return nil
}
func (s *NativeSteps) Reconciliation(id string) (interceptor.PreparedOperation, []byte, error) {
	a := s.a
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.nativeSteps[id]
	if !ok || r.ReconciliationState == "" {
		return interceptor.PreparedOperation{}, nil, ErrInvalid
	}
	raw, err := s.read(r.ReconciliationRequest)
	if err != nil {
		return interceptor.PreparedOperation{}, nil, err
	}
	p, err := interceptor.ParsePreparedOperation(raw)
	if err != nil {
		return p, nil, a.failure(ErrCorrupt)
	}
	response, err := s.read(r.ReconciliationResponse)
	return p, response, err
}
