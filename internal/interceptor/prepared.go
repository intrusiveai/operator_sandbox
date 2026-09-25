package interceptor

import (
	"bytes"
	"encoding/json"
)

// ParsePreparedOperation validates an exact saved native envelope without
// refreshing its IDs, deadline, attribution or body serialization.
func ParsePreparedOperation(raw []byte) (PreparedOperation, error) {
	var frame struct {
		Request json.RawMessage `json:"request"`
		Body    json.RawMessage `json:"body"`
	}
	if decodeClosed(raw, &frame, []string{"request", "body"}, nil) != nil {
		return PreparedOperation{}, ErrRequest
	}
	var q OperationRequest
	if decodeClosed(frame.Request, &q, []string{"api_version", "request_id", "operation_id", "operation", "session_id", "campaign_id", "worker_instance_id", "run_revision", "body_digest", "deadline"}, []string{"expected_session_revision", "attempt_id", "attempt_context_digest"}) != nil {
		return PreparedOperation{}, ErrRequest
	}
	p, err := PrepareOperation(q, frame.Body)
	if err != nil {
		return PreparedOperation{}, err
	}
	p.envelope = bytes.Clone(raw)
	return p, nil
}

// CommandFingerprint follows native duplicate identity. Worker/revision are
// attribution; the body digest, original request ID and deadline remain pinned.
func (p PreparedOperation) CommandFingerprint() string {
	if len(p.envelope) == 0 {
		return ""
	}
	fields, _ := object(p.envelope)
	return operationCommandFingerprint(p.request, fields["body"])
}

// ParseResponse validates saved native framing. Operation-specific receipt and
// result validation is still required before calling a native effect successful.
func ParseResponse(raw []byte) (Response, error) { return decodeResponse(raw) }

// Bytes returns the exact received envelope, including its original JSON body.
// A manually constructed Response has no verified wire bytes and returns nil.
func (r Response) Bytes() []byte { return bytes.Clone(r.raw) }
