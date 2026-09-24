package interceptor

import (
	"bytes"
	"encoding/json"
	"time"
)

type OperationRecord struct {
	CommandFingerprint string           `json:"command_fingerprint,omitempty"`
	Request            OperationRequest `json:"request"`
	Fingerprint        string           `json:"fingerprint"`
	State              string           `json:"state"`
	Response           Response         `json:"response"`
	AdmittedAt         time.Time        `json:"admitted_at"`
	FinishedAt         *time.Time       `json:"finished_at,omitempty"`
}

// PrepareOperationStatus uses a fresh read-only query identity/deadline while
// addressing the original operation's campaign and session, even after restore.
// Persist the query if required by the host journal, then Execute and decode.
func PrepareOperationStatus(q OperationRequest, original PreparedOperation) (PreparedOperation, error) {
	if len(original.envelope) == 0 || (q.Operation != "" && q.Operation != "operation.status") || q.CampaignID != original.request.CampaignID || q.SessionID != original.request.SessionID || q.OperationID == original.request.OperationID || q.RequestID == original.request.RequestID || q.AttemptID != "" || q.AttemptContextDigest != "" {
		return PreparedOperation{}, ErrRequest
	}
	q.Operation = "operation.status"
	body, _ := json.Marshal(struct {
		OperationID string `json:"operation_id"`
	}{original.request.OperationID})
	return PrepareOperation(q, body)
}

func operationFingerprint(q OperationRequest) string {
	raw, _ := json.Marshal(q)
	return rawDigest(raw)
}

func operationCommandFingerprint(q OperationRequest, body json.RawMessage) string {
	q.WorkerInstanceID, q.RunRevision = "", 0
	if q.Operation == "session.owner" {
		// Public prepared owner requests can only contain these three fields.
		// The native store normalizes OwnerRequest before hashing its body.
		var owner struct {
			Action     string `json:"action"`
			SessionID  string `json:"session_id"`
			CampaignID string `json:"campaign_id"`
		}
		_ = json.Unmarshal(body, &owner)
		normalized, _ := json.Marshal(owner)
		q.BodyDigest = rawDigest(normalized)
	}
	return operationFingerprint(q)
}

// DecodeOperationRecord binds a status result to the saved command. The complete
// original request fingerprint includes attribution; command comparison excludes
// only worker/revision. Neither a missing record nor an unknown outcome authorizes
// a replacement effect or reopening a terminal campaign.
func DecodeOperationRecord(r Response, original PreparedOperation) (OperationRecord, error) {
	if len(original.envelope) == 0 {
		return OperationRecord{}, ErrRequest
	}
	if err := success(r, nil); err != nil {
		return OperationRecord{}, err
	}
	var rec OperationRecord
	if decodeClosed(r.Body, &rec, []string{"request", "fingerprint", "state", "response", "admitted_at"}, []string{"command_fingerprint", "finished_at"}) != nil {
		return OperationRecord{}, invalidResponse()
	}
	fields, _ := object(r.Body)
	if decodeClosed(fields["request"], &rec.Request, []string{"api_version", "request_id", "operation_id", "operation", "session_id", "campaign_id", "worker_instance_id", "run_revision", "body_digest", "deadline"}, []string{"expected_session_revision", "attempt_id", "attempt_context_digest"}) != nil {
		return OperationRecord{}, invalidResponse()
	}
	envelope, _ := object(original.envelope)
	p, err := PrepareOperation(rec.Request, envelope["body"])
	if err != nil || rec.Fingerprint != operationFingerprint(p.request) || rec.AdmittedAt.IsZero() {
		return OperationRecord{}, invalidResponse()
	}
	want := operationCommandFingerprint(original.request, envelope["body"])
	if operationCommandFingerprint(rec.Request, envelope["body"]) != want || rec.CommandFingerprint != "" && rec.CommandFingerprint != want {
		return OperationRecord{}, invalidResponse()
	}
	switch rec.State {
	case "running":
		// The native store persists an empty GatewayResponse at admission.
		// Do not turn its status:0 into an execution success or HTTP response.
		var pending Response
		if decodeClosed(fields["response"], &pending, []string{"status", "session_revision"}, []string{"body"}) != nil || pending.Status != 0 || pending.SessionRevision != 0 || len(pending.Body) != 0 && !bytes.Equal(pending.Body, []byte("null")) || rec.FinishedAt != nil {
			return OperationRecord{}, invalidResponse()
		}
	case "completed", "unknown":
		if rec.FinishedAt == nil || rec.FinishedAt.IsZero() {
			return OperationRecord{}, invalidResponse()
		}
		response, err := decodeResponse(fields["response"])
		if err != nil {
			return OperationRecord{}, err
		}
		rec.Response = response
	default:
		return OperationRecord{}, invalidResponse()
	}
	return rec, nil
}
