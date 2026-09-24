package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
)

type Binding struct {
	SessionID        string `json:"session_id"`
	WorkerInstanceID string `json:"worker_instance_id"`
	RunRevision      uint64 `json:"run_revision"`
}

func (b Binding) valid() bool {
	return identifier.MatchString(b.SessionID) && identifier.MatchString(b.WorkerInstanceID) && b.RunRevision > 0
}

// Session is the small routing projection of native metadata. RawSession is
// retained separately on Attachment for host audit; never expose it to the guest.
type Session struct {
	ID                       string `json:"id"`
	CampaignID               string `json:"campaign_id"`
	OperationAPIVersion      string `json:"operation_api_version"`
	Revision                 uint64 `json:"revision"`
	Phase                    string `json:"phase"`
	FeedbackProfile          string `json:"feedback_profile"`
	EnvironmentDigest        string `json:"environment_digest"`
	AppDigest                string `json:"app_digest"`
	CapabilityManifestDigest string `json:"capability_manifest_digest"`
}
type Attachment struct {
	CampaignID               string
	Binding                  Binding
	Session                  Session
	EvidenceMaxBytes         int64
	RawSession, Capabilities json.RawMessage
}

func (c *Client) Attach(ctx context.Context, campaign, worker string, allowTargetStop bool) (Attachment, error) {
	if !identifier.MatchString(campaign) || !identifier.MatchString(worker) {
		return Attachment{}, ErrRequest
	}
	request := struct {
		Version  string `json:"api_version"`
		Campaign string `json:"campaign_id"`
		Worker   string `json:"worker_instance_id"`
		Allow    bool   `json:"allow_target_stop,omitempty"`
	}{LifecycleVersion, campaign, worker, allowTargetStop}
	raw, _ := json.Marshal(request)
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.post(ctx, "/v1/attach", raw)
	if err = success(r, err); err != nil {
		return Attachment{}, err
	}
	var body struct {
		Version      string          `json:"api_version"`
		Campaign     string          `json:"campaign_id"`
		Binding      Binding         `json:"binding"`
		Session      json.RawMessage `json:"session"`
		Capabilities json.RawMessage `json:"capabilities"`
		Evidence     int64           `json:"evidence_max_bytes"`
	}
	if decodeClosed(r.Body, &body, []string{"api_version", "campaign_id", "binding", "session", "capabilities", "evidence_max_bytes"}, nil) != nil || body.Version != LifecycleVersion || body.Campaign != campaign || !body.Binding.valid() || body.Binding.WorkerInstanceID != worker || body.Evidence <= 0 {
		return Attachment{}, invalidResponse()
	}
	fields, _ := object(r.Body)
	if decodeClosed(fields["binding"], &body.Binding, []string{"session_id", "worker_instance_id", "run_revision"}, nil) != nil {
		return Attachment{}, invalidResponse()
	}
	session, err := decodeRunningSession(body.Session)
	if err != nil || session.ID != body.Binding.SessionID || session.CampaignID != campaign {
		return Attachment{}, invalidResponse()
	}
	caps, err := object(body.Capabilities)
	if err != nil {
		return Attachment{}, invalidResponse()
	}
	for key, want := range map[string]string{"api_version": "interceptor.dev/capability-manifest/v1alpha2", "kind": "CapabilityManifest", "environment_digest": session.EnvironmentDigest, "application_digest": session.AppDigest, "digest": session.CapabilityManifestDigest} {
		var got string
		if json.Unmarshal(caps[key], &got) != nil || got != want {
			return Attachment{}, invalidResponse()
		}
	}
	return Attachment{campaign, body.Binding, session, body.Evidence, bytes.Clone(body.Session), bytes.Clone(body.Capabilities)}, nil
}

// decodeRunningSession selects exact routing keys from extensible host metadata.
// Unknown aliases must not override authoritative lowercase fields.
func decodeRunningSession(raw []byte) (Session, error) {
	var session Session
	metadata, err := object(raw)
	if err != nil {
		return Session{}, invalidResponse()
	}
	projection := map[string]json.RawMessage{}
	for _, key := range []string{"id", "campaign_id", "operation_api_version", "revision", "phase", "feedback_profile", "environment_digest", "app_digest", "capability_manifest_digest"} {
		v, ok := metadata[key]
		if !ok || bytes.Equal(v, []byte("null")) {
			return Session{}, invalidResponse()
		}
		projection[key] = v
	}
	projected, _ := json.Marshal(projection)
	if json.Unmarshal(projected, &session) != nil || !identifier.MatchString(session.ID) || !identifier.MatchString(session.CampaignID) || session.OperationAPIVersion != OperationVersion || session.Phase != "running" || !digest.MatchString(session.EnvironmentDigest) || !digest.MatchString(session.AppDigest) || !digest.MatchString(session.CapabilityManifestDigest) {
		return Session{}, invalidResponse()
	}
	if session.FeedbackProfile != "black-box" && session.FeedbackProfile != "diagnostic" && session.FeedbackProfile != "oracle-assisted" {
		return Session{}, invalidResponse()
	}
	return session, nil
}

type Failure struct {
	SessionID  string    `json:"session_id"`
	Reason     string    `json:"reason"`
	ObservedAt time.Time `json:"observed_at"`
}
type Status struct {
	InstanceID       string             `json:"instance_id"`
	CampaignID       string             `json:"campaign_id"`
	Active           Binding            `json:"active"`
	Sessions         map[string]Binding `json:"sessions"`
	Phase            string             `json:"phase"`
	Closed           bool               `json:"closed"`
	StoreAvailable   bool               `json:"store_available"`
	EvidenceMaxBytes int64              `json:"evidence_max_bytes"`
	Failure          *Failure           `json:"failure"`
}

func (s Status) Ready() bool {
	return s.Phase == "ready" && !s.Closed && s.StoreAvailable && s.Failure == nil
}

// Matches compares process/session/revision, not another worker's attribution.
func (s Status) Matches(instance string, b Binding) bool {
	return s.InstanceID == instance && s.Active.SessionID == b.SessionID && s.Active.RunRevision == b.RunRevision
}

func (c *Client) Status(ctx context.Context, campaign string) (Status, error) {
	if !identifier.MatchString(campaign) {
		return Status{}, ErrRequest
	}
	raw, _ := json.Marshal(map[string]string{"api_version": LifecycleVersion, "campaign_id": campaign})
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.post(ctx, "/v1/status", raw)
	if err = success(r, err); err != nil {
		return Status{}, err
	}
	var s Status
	if decodeClosed(r.Body, &s, []string{"instance_id", "campaign_id", "active", "sessions", "phase", "closed", "store_available", "evidence_max_bytes"}, []string{"failure"}) != nil || s.CampaignID != campaign || !identifier.MatchString(s.InstanceID) || !s.Active.valid() || s.EvidenceMaxBytes <= 0 {
		return Status{}, invalidResponse()
	}
	if s.Phase != "ready" && s.Phase != "transitioning" && s.Phase != "stopped" && s.Phase != "error" {
		return Status{}, invalidResponse()
	}
	if (s.Phase == "stopped" || s.Phase == "error") && !s.Closed {
		return Status{}, invalidResponse()
	}
	fields, _ := object(r.Body)
	if decodeClosed(fields["active"], &s.Active, []string{"session_id", "worker_instance_id", "run_revision"}, nil) != nil {
		return Status{}, invalidResponse()
	}
	bindings, err := object(fields["sessions"])
	if err != nil {
		return Status{}, invalidResponse()
	}
	for id, raw := range bindings {
		var b Binding
		if decodeClosed(raw, &b, []string{"session_id", "worker_instance_id", "run_revision"}, nil) != nil || !b.valid() || b.SessionID != id {
			return Status{}, invalidResponse()
		}
	}
	if b, ok := s.Sessions[s.Active.SessionID]; !ok || b.SessionID != s.Active.SessionID || b.RunRevision != s.Active.RunRevision {
		return Status{}, invalidResponse()
	}
	if s.Failure != nil {
		if decodeClosed(fields["failure"], s.Failure, []string{"session_id", "reason", "observed_at"}, nil) != nil || !identifier.MatchString(s.Failure.SessionID) || s.Failure.Reason == "" || len(s.Failure.Reason) > 4096 || s.Failure.ObservedAt.IsZero() || !s.Closed {
			return Status{}, invalidResponse()
		}
	}
	return s, nil
}

type Owner struct {
	Principal        string     `json:"principal"`
	CampaignID       string     `json:"campaign_id"`
	WorkerInstanceID string     `json:"worker_instance_id"`
	RunRevision      uint64     `json:"run_revision"`
	AllowTargetStop  bool       `json:"allow_target_stop"`
	BoundAt          time.Time  `json:"bound_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	ClosureReason    string     `json:"closure_reason,omitempty"`
}

func PrepareClose(q OperationRequest) (PreparedOperation, error) {
	if q.Operation != "" && q.Operation != "session.owner" {
		return PreparedOperation{}, ErrRequest
	}
	q.Operation = "session.owner"
	body, _ := json.Marshal(struct {
		Action   string `json:"action"`
		Session  string `json:"session_id"`
		Campaign string `json:"campaign_id"`
	}{"close_execution", q.SessionID, q.CampaignID})
	return PrepareOperation(q, body)
}

// DecodeClosure accepts the direct native owner body for live or retained
// sessions. Historical worker/revision attribution need not match the caller.
// Confirmed admission closure does not establish target/container termination.
func DecodeClosure(r Response, campaign string) (Owner, error) {
	if err := success(r, nil); err != nil {
		return Owner{}, err
	}
	var owner Owner
	if decodeClosed(r.Body, &owner, []string{"principal", "campaign_id", "worker_instance_id", "run_revision", "allow_target_stop", "bound_at", "closed_at"}, []string{"closure_reason"}) != nil || owner.CampaignID != campaign || !identifier.MatchString(owner.Principal) || !identifier.MatchString(owner.WorkerInstanceID) || owner.RunRevision == 0 || owner.BoundAt.IsZero() || owner.ClosedAt == nil || owner.ClosedAt.IsZero() {
		return Owner{}, invalidResponse()
	}
	return owner, nil
}
