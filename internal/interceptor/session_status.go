package interceptor

import "encoding/json"

// SessionStatus preserves native metadata separately from its checked routing
// projection. SessionRevision is a native mutation counter, not run_revision.
type SessionStatus struct {
	Session    Session
	Owner      Owner
	Available  bool
	RawSession json.RawMessage
}

func DecodeSessionStatus(r Response, campaign string, binding Binding) (SessionStatus, error) {
	var body struct {
		Session   json.RawMessage `json:"session"`
		Owner     Owner           `json:"owner"`
		Available bool            `json:"execution_available"`
	}
	if r.Status != 200 || decodeClosed(r.Body, &body, []string{"session", "owner", "execution_available"}, nil) != nil {
		return SessionStatus{}, invalidResponse()
	}
	session, err := decodeRunningSession(body.Session)
	if err != nil || session.ID != binding.SessionID || session.CampaignID != campaign || session.Revision == 0 || session.Revision != r.SessionRevision || body.Owner.CampaignID != campaign || !identifier.MatchString(body.Owner.WorkerInstanceID) || body.Owner.RunRevision == 0 || body.Owner.BoundAt.IsZero() || body.Available != (body.Owner.ClosedAt == nil) {
		return SessionStatus{}, invalidResponse()
	}
	return SessionStatus{session, body.Owner, body.Available, body.Session}, nil
}
