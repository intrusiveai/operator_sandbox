package interceptor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSessionStatusDistinguishesNativeRevisionAndClosure(t *testing.T) {
	testDigest := "sha256:" + strings.Repeat("0", 64)
	binding := Binding{"session-1", "worker-new", 3}
	session := Session{ID: binding.SessionID, CampaignID: "campaign-1", OperationAPIVersion: OperationVersion, Revision: 41, Phase: "running", FeedbackProfile: "black-box", EnvironmentDigest: testDigest, AppDigest: testDigest, CapabilityManifestDigest: testDigest}
	now := time.Now().UTC()
	owner := Owner{Principal: "operator", CampaignID: "campaign-1", WorkerInstanceID: "worker-old", RunRevision: 1, BoundAt: now}
	response := func(available bool) Response {
		body, _ := json.Marshal(map[string]any{"session": session, "owner": owner, "execution_available": available})
		return Response{Status: 200, Body: body, SessionRevision: 41}
	}
	if got, err := DecodeSessionStatus(response(true), "campaign-1", binding); err != nil || !got.Available || got.Session.Revision != 41 {
		t.Fatal(got, err)
	}
	owner.ClosedAt = &now
	if got, err := DecodeSessionStatus(response(false), "campaign-1", binding); err != nil || got.Available {
		t.Fatal(got, err)
	}
	if _, err := DecodeSessionStatus(response(true), "campaign-1", binding); err == nil {
		t.Fatal("closed owner advertised as executable")
	}
	session.Revision = 42
	if _, err := DecodeSessionStatus(response(false), "campaign-1", binding); err == nil {
		t.Fatal("mismatched mutation revision")
	}
}
