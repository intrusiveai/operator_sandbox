package contracts

import "regexp"

var attemptIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var attemptDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// AttemptAllocation is assigned by the trusted request builder, never the model.
type AttemptAllocation struct {
	RequestID string `json:"request_id"`
	AttemptID string `json:"attempt_id"`
	Index     int64  `json:"attempt_index"`
}

// AttemptAllocator belongs to one campaign and one serialized dispatcher. Keep
// the same instance across target restores. It does not generate IDs or persist.
type AttemptAllocator struct {
	highWater int64
	requests  map[string]bool
	attempts  map[string]bool
}

func NewAttemptAllocator(highWater int64) (*AttemptAllocator, error) {
	if highWater < 0 || highWater > MaxSafeInteger {
		return nil, ErrProtocol
	}
	return &AttemptAllocator{highWater: highWater, requests: map[string]bool{}, attempts: map[string]bool{}}, nil
}

func (a *AttemptAllocator) HighWatermark() int64 { return a.highWater }

// Allocate is only for a newly dispatched call. The dispatcher must resolve
// duplicates from its saved call record before invoking it. Malformed/non-object
// arguments allocate nothing. A nonzero allocation MUST be retained even when
// err != nil (model-supplied reserved fields are rejected after reservation).
// Other schema/semantic validation follows this call and cannot undo allocation.
func (a *AttemptAllocator) Allocate(raw []byte, requestID, attemptID string) (allocation AttemptAllocation, err error) {
	v, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		return allocation, err
	}
	args, ok := v.(map[string]any)
	if !ok || !attemptIDPattern.MatchString(requestID) || !attemptIDPattern.MatchString(attemptID) || a.requests[requestID] || a.attempts[attemptID] {
		return allocation, ErrProtocol
	}
	if a.highWater == MaxSafeInteger {
		return allocation, ErrLimit
	}
	a.highWater++
	a.requests[requestID], a.attempts[attemptID] = true, true
	allocation = AttemptAllocation{RequestID: requestID, AttemptID: attemptID, Index: a.highWater}
	for _, key := range []string{"request_id", "attempt_id", "attempt_index"} {
		if _, present := args[key]; present {
			return allocation, ErrProtocol
		}
	}
	return allocation, nil
}

// AttemptSubmission contains trusted bookkeeping extracted from a bounded
// request. IdentityDigest is the host-computed complete operation identity under
// SHARED_CONTRACT section 3, not a digest asserted by the guest or of payload alone.
type AttemptSubmission struct {
	AttemptAllocation
	IdentityDigest string `json:"identity_digest"`
}

type AttemptRecord struct {
	AttemptSubmission
	State        string `json:"state"`
	ResultDigest string `json:"result_digest,omitempty"`
}

// AttemptLedger is an in-memory semantic state machine, NOT a durable journal or
// an authorization check. One campaign/serialized owner; persist each successful
// transition before replying or beginning native effects. A persistence failure
// must close execution. Runtime policy bounds the total number of submissions.
type AttemptLedger struct {
	highWater, admitted, maximum int64
	records                      map[string]AttemptRecord
	attempts                     map[string]bool
	closed                       bool
}

func NewAttemptLedger(highWater, maximumAdmissions int64) (*AttemptLedger, error) {
	if highWater < 0 || highWater > MaxSafeInteger || maximumAdmissions < 1 || maximumAdmissions > MaxSafeInteger {
		return nil, ErrProtocol
	}
	return &AttemptLedger{highWater: highWater, maximum: maximumAdmissions, records: map[string]AttemptRecord{}, attempts: map[string]bool{}}, nil
}

func (l *AttemptLedger) HighWatermark() int64 { return l.highWater }
func (l *AttemptLedger) Admissions() int64    { return l.admitted }
func (l *AttemptLedger) Closed() bool         { return l.closed }
func (l *AttemptLedger) Close()               { l.closed = true }
func (l *AttemptLedger) Lookup(requestID string) (AttemptRecord, bool) {
	r, ok := l.records[requestID]
	return r, ok
}

// Observe records identifiable submissions before tactical validation. Exact
// replays are looked up before monotonicity/closure checks; replay=true NEVER
// grants permission to run an effect, even for a pending/admitted record.
func (l *AttemptLedger) Observe(s AttemptSubmission) (record AttemptRecord, replay bool, err error) {
	if !attemptIDPattern.MatchString(s.RequestID) || !attemptIDPattern.MatchString(s.AttemptID) || s.Index < 1 || s.Index > MaxSafeInteger || !attemptDigestPattern.MatchString(s.IdentityDigest) {
		return record, false, ErrProtocol
	}
	if previous, ok := l.records[s.RequestID]; ok {
		if previous.AttemptSubmission != s {
			return record, false, ErrProtocol
		}
		return previous, true, nil
	}
	if l.closed || s.Index <= l.highWater || l.attempts[s.AttemptID] {
		return record, false, ErrProtocol
	}
	record = AttemptRecord{AttemptSubmission: s, State: "observed"}
	l.records[s.RequestID], l.attempts[s.AttemptID], l.highWater = record, true, s.Index
	return record, false, nil
}

// Admit returns true only for a NEW admission. Persist execution intent before
// the first native operation. Duplicate admissions return false without charging
// again; never treat a false return as permission to repeat execution.
func (l *AttemptLedger) Admit(requestID string) (bool, error) {
	r, ok := l.records[requestID]
	if !ok || l.closed || r.State == "rejected" {
		return false, ErrProtocol
	}
	if r.State != "observed" {
		return false, nil
	}
	if l.admitted == l.maximum {
		return false, ErrLimit
	}
	r.State = "admitted"
	l.records[requestID] = r
	l.admitted++
	return true, nil
}

// Resolve pins a known result (stored separately by the caller). Only observed
// requests can be rejected; only admitted requests can succeed/fail/be unknown.
// Unknown outcome closes the ledger permanently and retains the admission.
func (l *AttemptLedger) Resolve(requestID, outcome, resultDigest string) error {
	r, ok := l.records[requestID]
	if !ok || !attemptDigestPattern.MatchString(resultDigest) {
		return ErrProtocol
	}
	if r.State == outcome && r.ResultDigest == resultDigest {
		return nil
	}
	if l.closed {
		return ErrProtocol
	}
	if !((r.State == "observed" && outcome == "rejected") || (r.State == "admitted" && (outcome == "succeeded" || outcome == "failed" || outcome == "unknown"))) {
		return ErrProtocol
	}
	r.State, r.ResultDigest = outcome, resultDigest
	l.records[requestID] = r
	if outcome == "unknown" {
		l.closed = true
	}
	return nil
}
