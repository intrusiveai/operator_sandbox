package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"time"
)

// Field order is native: Interceptor hashes json.Marshal of this structure,
// with attribution cleared, for its lifecycle command fingerprint.
type LifecycleRequest struct {
	WorkerInstanceID string `json:"worker_instance_id"`
	RunRevision      uint64 `json:"run_revision"`
	APIVersion       string `json:"api_version"`
	CampaignID       string `json:"campaign_id"`
	SessionID        string `json:"session_id"`
	OperationID      string `json:"operation_id"`
	Operation        string `json:"operation"`
	CheckpointID     string `json:"checkpoint_id,omitempty"`
	SourceSessionID  string `json:"source_session_id,omitempty"`
}

type PreparedLifecycle struct {
	request  LifecycleRequest
	envelope []byte
	deadline time.Time
}

func (p PreparedLifecycle) Request() LifecycleRequest { return p.request }
func (p PreparedLifecycle) Bytes() []byte             { return bytes.Clone(p.envelope) }
func (p PreparedLifecycle) Deadline() time.Time       { return p.deadline }

func validLifecycle(q LifecycleRequest) bool {
	if q.APIVersion != LifecycleVersion || q.RunRevision == 0 {
		return false
	}
	for _, id := range []string{q.WorkerInstanceID, q.CampaignID, q.SessionID, q.OperationID} {
		if !identifier.MatchString(id) {
			return false
		}
	}
	switch q.Operation {
	case "session.stop":
		return q.CheckpointID == "" && q.SourceSessionID == ""
	case "snapshot.restore":
		return identifier.MatchString(q.CheckpointID) && (q.SourceSessionID == "" || identifier.MatchString(q.SourceSessionID))
	}
	return false
}

// PrepareLifecycle freezes a native command plus an absolute host deadline.
// Journal both before dispatch. The native wire has no deadline field; accepted
// server work may continue after this client's deadline or disconnection.
func PrepareLifecycle(q LifecycleRequest, deadline time.Time) (PreparedLifecycle, error) {
	if q.APIVersion == "" {
		q.APIVersion = LifecycleVersion
	}
	if !validLifecycle(q) || deadline.IsZero() {
		return PreparedLifecycle{}, ErrRequest
	}
	raw, err := json.Marshal(q)
	if err != nil || len(raw) > 8192 {
		return PreparedLifecycle{}, ErrRequest
	}
	return PreparedLifecycle{q, raw, deadline}, nil
}

func lifecycleFingerprint(q LifecycleRequest) string {
	q.WorkerInstanceID, q.RunRevision = "", 0
	raw, _ := json.Marshal(q)
	return rawDigest(raw)
}

// ExecuteLifecycle does not retry, adopt a replacement binding, or reopen host
// admission. The original absolute deadline also bounds an explicit resend.
func (c *Client) ExecuteLifecycle(ctx context.Context, p PreparedLifecycle) (Response, error) {
	if len(p.envelope) == 0 {
		return Response{}, &CallError{Kind: "invalid_request"}
	}
	deadline := p.deadline
	if cap := time.Now().Add(MaxOperationTimeout); deadline.After(cap) {
		deadline = cap
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return c.post(ctx, "/v1/lifecycle", p.envelope)
}

type RestoreResult struct {
	Binding                       Binding
	Session                       Session
	SourceSessionID, CheckpointID string
	RawSession                    json.RawMessage
}

// DecodeRestore validates lineage against the persisted pre-transition binding
// and target identity. prior.RunRevision is authoritative; request.RunRevision
// is attribution only. Historical worker attribution may differ after replay.
// A valid result is evidence, not permission to resume a terminal campaign.
func DecodeRestore(r Response, p PreparedLifecycle, prior Binding, target Session) (RestoreResult, error) {
	q := p.request
	if len(p.envelope) == 0 || q.Operation != "snapshot.restore" || !prior.valid() || prior.SessionID != q.SessionID || target.ID != prior.SessionID || target.CampaignID != q.CampaignID || prior.RunRevision == math.MaxUint64 {
		return RestoreResult{}, ErrRequest
	}
	if err := success(r, nil); err != nil {
		return RestoreResult{}, err
	}
	var body struct {
		Binding    json.RawMessage `json:"binding"`
		Session    json.RawMessage `json:"session"`
		Source     string          `json:"source_session_id"`
		Checkpoint string          `json:"checkpoint_id"`
	}
	if decodeClosed(r.Body, &body, []string{"binding", "session", "source_session_id", "checkpoint_id"}, nil) != nil {
		return RestoreResult{}, invalidResponse()
	}
	source := q.SourceSessionID
	if source == "" {
		source = q.SessionID
	}
	var b Binding
	if decodeClosed(body.Binding, &b, []string{"session_id", "worker_instance_id", "run_revision"}, nil) != nil || !b.valid() || b.RunRevision != prior.RunRevision+1 || b.SessionID == prior.SessionID || b.SessionID == source || body.Source != source || body.Checkpoint != q.CheckpointID {
		return RestoreResult{}, invalidResponse()
	}
	s, err := decodeRunningSession(body.Session)
	if err != nil || s.ID != b.SessionID || s.CampaignID != q.CampaignID || s.EnvironmentDigest != target.EnvironmentDigest || s.AppDigest != target.AppDigest || s.CapabilityManifestDigest != target.CapabilityManifestDigest || s.FeedbackProfile != target.FeedbackProfile {
		return RestoreResult{}, invalidResponse()
	}
	fields, _ := object(body.Session)
	var parent, checkpoint string
	if json.Unmarshal(fields["parent_session_id"], &parent) != nil || json.Unmarshal(fields["parent_checkpoint"], &checkpoint) != nil || parent != source || checkpoint != q.CheckpointID {
		return RestoreResult{}, invalidResponse()
	}
	return RestoreResult{b, s, source, q.CheckpointID, bytes.Clone(body.Session)}, nil
}

type StopResult struct {
	SessionID string `json:"session_id"`
	Phase     string `json:"phase"`
}

func DecodeStop(r Response, p PreparedLifecycle) (StopResult, error) {
	if len(p.envelope) == 0 || p.request.Operation != "session.stop" {
		return StopResult{}, ErrRequest
	}
	if err := success(r, nil); err != nil {
		return StopResult{}, err
	}
	var result StopResult
	if decodeClosed(r.Body, &result, []string{"session_id", "phase"}, nil) != nil || result.SessionID != p.request.SessionID || result.Phase != "stopped" {
		return StopResult{}, invalidResponse()
	}
	return result, nil
}

type LifecycleRecord struct {
	Request     LifecycleRequest `json:"request"`
	Fingerprint string           `json:"fingerprint"`
	State       string           `json:"state"`
	Response    Response         `json:"response"`
}

// LifecycleStatus uses the lifecycle ledger, not native operation.status.
// A missing record, running record or failed query cannot prove non-execution.
func (c *Client) LifecycleStatus(ctx context.Context, p PreparedLifecycle) (LifecycleRecord, error) {
	if len(p.envelope) == 0 {
		return LifecycleRecord{}, ErrRequest
	}
	raw, _ := json.Marshal(map[string]string{"api_version": LifecycleVersion, "campaign_id": p.request.CampaignID, "operation_id": p.request.OperationID})
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.post(ctx, "/v1/status", raw)
	if err = success(r, err); err != nil {
		return LifecycleRecord{}, err
	}
	return DecodeLifecycleRecord(r, p)
}

func DecodeLifecycleRecord(r Response, p PreparedLifecycle) (LifecycleRecord, error) {
	if len(p.envelope) == 0 {
		return LifecycleRecord{}, ErrRequest
	}
	if err := success(r, nil); err != nil {
		return LifecycleRecord{}, err
	}
	var rec LifecycleRecord
	if decodeClosed(r.Body, &rec, []string{"request", "fingerprint", "state", "response"}, nil) != nil {
		return LifecycleRecord{}, invalidResponse()
	}
	fields, _ := object(r.Body)
	if decodeClosed(fields["request"], &rec.Request, []string{"worker_instance_id", "run_revision", "api_version", "campaign_id", "session_id", "operation_id", "operation"}, []string{"checkpoint_id", "source_session_id"}) != nil || !validLifecycle(rec.Request) || rec.Fingerprint != lifecycleFingerprint(rec.Request) || rec.Fingerprint != lifecycleFingerprint(p.request) {
		return LifecycleRecord{}, invalidResponse()
	}
	response, err := decodeResponse(fields["response"])
	if err != nil || (rec.State != "running" && rec.State != "completed") || rec.State == "running" && (response.Status != 202 || response.Code() != "operation_in_progress") || rec.State == "completed" && response.Status == 202 {
		return LifecycleRecord{}, invalidResponse()
	}
	rec.Response = response
	return rec, nil
}
