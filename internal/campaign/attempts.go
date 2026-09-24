//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"errors"
	"math/big"
	"sync"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

// Capacity for admission/dispatch/completion event envelopes, one translated plan,
// one native result and one guest result. Observation itself is charged separately.
const AttemptAuditReservation = 3*(MaxEventBytes+1) + 3*MaxContentBytes

// Leave room for the attempt/counter/reservation metadata in the 64 KiB event
// metadata ceiling. The native adapter must bound receipt metadata accordingly.
const MaxAttemptReceiptBytes = 60 << 10

// AttemptInput follows outer transport/campaign validation, but precedes tactical
// body validation. Worker/revision are attribution, not ownership of prior work.
type AttemptInput struct {
	CampaignID       string
	WorkerInstanceID string
	RunRevision      int64
	Body             []byte
}

type AttemptCompletion struct {
	Outcome      string          // rejected, succeeded, failed, unknown (host determined).
	Result       []byte          // Exact bounded guest-visible JSON object, without the envelope.
	NativeResult []byte          // Optional exact bounded native response bytes.
	Receipts     json.RawMessage // Host-verified receipt/usage/cleanup metadata object.
}

type SavedAttempt struct {
	contracts.AttemptRecord
	Target            TargetBinding      `json:"target"`
	SubmittedRevision int64              `json:"submitted_revision"`
	WorkerInstanceID  string             `json:"worker_instance_id"`
	Dispatched        bool               `json:"dispatched"`
	PlanDigest        string             `json:"plan_digest"`
	CompletionDigest  string             `json:"completion_digest"`
	Result            *ContentDescriptor `json:"result"`
}

type AttemptStatus struct {
	HighWatermark int64
	Admissions    int64
	Submissions   int64
	RunRevision   int64
	Closed        bool
}

// Attempts owns the campaign's sole durable attempt ledger. No restore/recovery
// constructor exists. Callers still perform authorization, native lineage checks,
// typed response validation, live-container/deadline gates and actual dispatch.
type Attempts struct {
	mu                 sync.Mutex
	w                  *Writer
	ledger             *contracts.AttemptLedger
	records            map[string]SavedAttempt
	target             TargetBinding
	revision           int64
	maximumAdmissions  int64
	maximumSubmissions int64
}

func exactInteger(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	i := r.Num().Int64()
	return i, i >= 0 && i <= contracts.MaxSafeInteger
}

func NewAttempts(w *Writer, initialHighWatermark int64) (*Attempts, error) {
	if w == nil {
		return nil, ErrInvalid
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return nil, err
	}
	if err := w.fence.Err(); err != nil {
		return nil, err
	}
	if w.attemptOwner || !w.spaceConfigured || w.revision != w.manifest.InitialRevision {
		return nil, ErrInvalid
	}
	limits, _ := contracts.Decode(w.manifest.RemainingLimits, ManifestLimit)
	harness, _ := contracts.Decode(w.manifest.HarnessLimits, ManifestLimit)
	maximum, _ := exactInteger(limits.(map[string]any)["attempt_admissions"])
	submissions, _ := exactInteger(harness.(map[string]any)["max_tool_calls"])
	ledger, err := contracts.NewAttemptLedger(initialHighWatermark, max(1, maximum))
	if err != nil {
		return nil, err
	}
	metadata, _ := encode(map[string]any{"attempt_index_high_watermark": initialHighWatermark, "maximum_admissions": maximum, "maximum_submissions": submissions}, MaxMetadataBytes)
	if _, err := w.appendLocked(Entry{RunRevision: w.revision, Kind: "attempt.ledger-started", Metadata: metadata}, nil); err != nil {
		return nil, err
	}
	w.attemptOwner = true
	return &Attempts{w: w, ledger: ledger, records: map[string]SavedAttempt{}, target: w.manifest.Target, revision: w.revision, maximumAdmissions: maximum, maximumSubmissions: submissions}, nil
}

func (a *Attempts) failure(err error) error {
	a.ledger.Close()
	a.w.fence.Stop(err)
	return err
}

func (a *Attempts) ready() error {
	if err := a.w.fence.Err(); err != nil {
		return err
	}
	return nil
}

func cloneAttempt(r SavedAttempt) SavedAttempt {
	if r.Result != nil {
		d := *r.Result
		r.Result = &d
	}
	return r
}

// Observe commits the request and high-water mark before tactical validation.
// replay=true only returns a previously committed record, never dispatch permission.
// Existing keys use their ORIGINAL effect target even after a healthy restore.
func (a *Attempts) Observe(in AttemptInput) (record SavedAttempt, replay bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if in.CampaignID != a.w.manifest.CampaignID || !validID(in.WorkerInstanceID) || in.RunRevision < 0 || in.RunRevision > contracts.MaxSafeInteger {
		return record, false, ErrInvalid
	}
	v, err := contracts.Decode(in.Body, MaxContentBytes)
	if err != nil {
		return record, false, err
	}
	body, ok := v.(map[string]any)
	if !ok {
		return record, false, ErrInvalid
	}
	requestID, _ := body["request_id"].(string)
	attemptID, _ := body["attempt_id"].(string)
	index, ok := exactInteger(body["attempt_index"])
	if !validID(requestID) || !validID(attemptID) || !ok || index == 0 {
		return record, false, ErrInvalid
	}
	target := a.target
	previous, exists := a.records[requestID]
	if exists {
		target = previous.Target
	}
	identity, err := encode(map[string]any{"campaign_id": in.CampaignID, "operation": "engine.attempt_execute", "operation_id": requestID, "body": body,
		"effect_target": map[string]any{"adapter": target.Adapter, "session_id": target.SessionID, "capability_source_digest": target.CapabilitySourceDigest, "capability_projection_digest": target.CapabilityProjectionDigest, "native_feedback_profile": target.NativeFeedbackProfile}}, MaxContentBytes+ManifestLimit)
	if err != nil {
		return record, false, err
	}
	submission := contracts.AttemptSubmission{AttemptAllocation: contracts.AttemptAllocation{RequestID: requestID, AttemptID: attemptID, Index: index}, IdentityDigest: contracts.RawDigest(identity)}
	if exists {
		if previous.AttemptSubmission != submission {
			return record, false, ErrInvalid
		}
		return cloneAttempt(previous), true, nil
	}
	if err := a.ready(); err != nil {
		return record, false, err
	}
	if in.RunRevision != a.revision {
		return record, false, ErrInvalid
	}
	if int64(len(a.records)) >= a.maximumSubmissions {
		return record, false, a.failure(ErrQuota)
	}
	observed, _, err := a.ledger.Observe(submission)
	if err != nil {
		return record, false, err
	}
	record = SavedAttempt{AttemptRecord: observed, Target: target, SubmittedRevision: in.RunRevision, WorkerInstanceID: in.WorkerInstanceID}
	entry, err := a.audit("attempt.observed", record, &OperationMark{requestID, submission.IdentityDigest, IntentCommitted}, nil,
		[]Content{{Role: "guest-request", MediaType: "application/json", Bytes: in.Body}})
	if err != nil {
		return SavedAttempt{}, false, a.failure(err)
	}
	if _, err := a.commit(entry, &reservationChange{ID: requestID, Action: "reserve", Bytes: AttemptAuditReservation}); err != nil {
		return SavedAttempt{}, false, a.failure(err)
	}
	a.records[requestID] = record
	if err := a.ready(); err != nil {
		return SavedAttempt{}, false, err
	}
	return cloneAttempt(record), false, nil
}

// Admit charges once and commits the authorized translated plan. A true return
// is a fresh admission, not permission to skip MarkDispatched or live gates.
func (a *Attempts) Admit(id string, plan []byte) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validJSONObject(plan, MaxContentBytes) {
		return false, ErrInvalid
	}
	digest, err := contracts.CanonicalDigest(plan, MaxContentBytes)
	if err != nil {
		return false, err
	}
	r, ok := a.records[id]
	if !ok {
		return false, ErrInvalid
	}
	if r.State != "observed" {
		if r.PlanDigest != digest {
			return false, ErrInvalid
		}
		return false, nil
	}
	if err := a.ready(); err != nil {
		return false, err
	}
	if a.ledger.Admissions() >= a.maximumAdmissions {
		return false, contracts.ErrLimit
	}
	fresh, err := a.ledger.Admit(id)
	if err != nil || !fresh {
		return false, err
	}
	r.AttemptRecord, _ = a.ledger.Lookup(id)
	r.PlanDigest = digest
	entry, err := a.audit("attempt.admitted", r, nil, nil, []Content{{Role: "translated-plan", MediaType: "application/json", Bytes: plan}})
	if err != nil {
		return false, a.failure(err)
	}
	if _, err := a.commit(entry, &reservationChange{ID: id, Action: "consume"}); err != nil {
		return false, a.failure(err)
	}
	a.records[id] = r
	if err := a.ready(); err != nil {
		return false, err
	}
	return true, nil
}

// MarkDispatched durably records the plan's possible-contact boundary once.
// After true, the native adapter still checks the terminal fence, deadline and
// exact live target before each step, with separate step audit/reservations.
// This method never authorizes a second execution of the plan or hidden retries.
func (a *Attempts) MarkDispatched(id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.records[id]
	if !ok {
		return false, ErrInvalid
	}
	if r.Dispatched {
		return false, nil
	}
	if err := a.ready(); err != nil {
		return false, err
	}
	if r.State != "admitted" {
		return false, ErrInvalid
	}
	r.Dispatched = true
	entry, err := a.audit("attempt.dispatched", r, &OperationMark{id, r.IdentityDigest, Dispatched}, nil, nil)
	if err != nil {
		return false, a.failure(err)
	}
	if _, err := a.commit(entry, &reservationChange{ID: id, Action: "consume"}); err != nil {
		return false, a.failure(err)
	}
	a.records[id] = r
	if err := a.ready(); err != nil {
		return false, err
	}
	return true, nil
}

// Resolve commits exact result bytes before they can be delivered. A saved
// identical completion is idempotent; changed content/receipts/outcome conflicts.
// The native adapter, not a guest assertion, determines outcome and receipts.
func (a *Attempts) Resolve(id string, c AttemptCompletion) error {
	// The native adapter already determined uncertainty. Signal even if another
	// attempt is holding the coordinator mutex while blocked on journal storage.
	if c.Outcome == "unknown" {
		a.w.fence.Stop(ErrUnknownOutcome)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.records[id]
	if !ok {
		return ErrInvalid
	}
	if !validJSONObject(c.Result, MaxContentBytes) || len(c.NativeResult) > MaxContentBytes || len(c.Receipts) > MaxAttemptReceiptBytes || !validateMetadata(c.Receipts) {
		return a.failure(ErrInvalid)
	}
	completion, err := encode(map[string]any{"outcome": c.Outcome, "result_digest": contracts.RawDigest(c.Result), "native_present": c.NativeResult != nil, "native_digest": contracts.RawDigest(c.NativeResult), "receipts": c.Receipts}, MaxMetadataBytes)
	if err != nil {
		return a.failure(err)
	}
	digest := contracts.RawDigest(completion)
	if r.CompletionDigest != "" {
		if r.CompletionDigest == digest {
			return nil
		}
		return ErrInvalid
	}
	if c.Outcome == "succeeded" && !r.Dispatched {
		return ErrInvalid
	}
	if err := a.ledger.Resolve(id, c.Outcome, contracts.RawDigest(c.Result)); err != nil {
		return err
	}
	r.AttemptRecord, _ = a.ledger.Lookup(id)
	r.CompletionDigest = digest
	mark := ResultCommitted
	if c.Outcome == "unknown" {
		mark = Unknown
	}
	content := []Content{{Role: "guest-result", MediaType: "application/json", Bytes: c.Result}}
	if c.NativeResult != nil {
		content = append(content, Content{Role: "native-result", MediaType: "application/octet-stream", Bytes: c.NativeResult})
	}
	entry, err := a.audit("attempt.resolved", r, &OperationMark{id, r.IdentityDigest, mark}, c.Receipts, content)
	if err != nil {
		return a.failure(err)
	}
	sequence, err := a.commit(entry, &reservationChange{ID: id, Action: "consume", Release: true})
	if err != nil {
		return a.failure(err)
	}
	r.Result = &ContentDescriptor{Role: "guest-result", MediaType: "application/json", Path: contentPath(a.revision, sequence, 0), SizeBytes: int64(len(c.Result)), Digest: contracts.RawDigest(c.Result)}
	a.records[id] = r
	return nil
}

var ErrUnknownOutcome = errors.New("attempt outcome is unknown; execution closed")

func validJSONObject(raw []byte, limit int) bool {
	v, err := contracts.Decode(raw, limit)
	_, ok := v.(map[string]any)
	return err == nil && ok
}

func (a *Attempts) audit(kind string, r SavedAttempt, mark *OperationMark, receipts json.RawMessage, content []Content) (Entry, error) {
	metadata, err := encode(map[string]any{"attempt": r, "attempt_index_high_watermark": a.ledger.HighWatermark(), "attempt_admissions": a.ledger.Admissions(), "receipts": receipts}, MaxMetadataBytes)
	return Entry{RunRevision: a.revision, Kind: kind, Operation: mark, Metadata: metadata, Content: content}, err
}

func (a *Attempts) commit(entry Entry, reservation *reservationChange) (int64, error) {
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if _, err := a.w.appendLocked(entry, reservation); err != nil {
		return 0, err
	}
	return a.w.head.Sequence, nil
}

// Lookup reads verified retained bytes, not a response body cached in memory.
// Pending attempts have nil result bytes. It never grants execution permission.
func (a *Attempts) Lookup(id string) (SavedAttempt, []byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.records[id]
	if !ok {
		return SavedAttempt{}, nil, ErrInvalid
	}
	if r.Result == nil {
		return cloneAttempt(r), nil, nil
	}
	a.w.mu.Lock()
	defer a.w.mu.Unlock()
	if a.w.closed {
		return SavedAttempt{}, nil, ErrClosed
	}
	raw, err := readFile(a.w.root, r.Result.Path, MaxContentBytes)
	if err != nil || int64(len(raw)) != r.Result.SizeBytes || contracts.RawDigest(raw) != r.Result.Digest {
		return SavedAttempt{}, nil, a.failure(ErrCorrupt)
	}
	return cloneAttempt(r), raw, nil
}

func (a *Attempts) Status() AttemptStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AttemptStatus{a.ledger.HighWatermark(), a.ledger.Admissions(), int64(len(a.records)), a.revision, a.w.fence.Err() != nil}
}

// Rebind records a caller-verified successful native restore while retaining the
// same ledger. The caller must first drain work; it cannot abandon pending results.
func (a *Attempts) Rebind(revision int64, target TargetBinding) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return err
	}
	if a.revision == contracts.MaxSafeInteger || revision != a.revision+1 || target.SessionID == a.target.SessionID {
		return ErrInvalid
	}
	m := a.w.manifest
	m.Target = target
	if err := m.Validate(); err != nil {
		return err
	}
	for _, r := range a.records {
		if r.State == "observed" || r.State == "admitted" {
			return ErrActive
		}
	}
	metadata, _ := encode(map[string]any{"target": target, "previous_revision": a.revision, "attempt_index_high_watermark": a.ledger.HighWatermark(), "attempt_admissions": a.ledger.Admissions()}, MaxMetadataBytes)
	if _, err := a.commit(Entry{RunRevision: revision, Kind: "attempt.target-restored", Metadata: metadata}, nil); err != nil {
		return a.failure(err)
	}
	a.target, a.revision = target, revision
	return a.ready()
}
