package interceptor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func savedOperation(t *testing.T) PreparedOperation {
	t.Helper()
	q := query()
	q.Operation = "injection.delete"
	p, err := PrepareOperation(q, []byte(`{"id":"injection-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func operationRecord(t *testing.T, p PreparedOperation, state string) OperationRecord {
	t.Helper()
	q := p.Request()
	q.WorkerInstanceID, q.RunRevision = "worker-original", 1
	fields, _ := object(p.Bytes())
	now := time.Now().UTC()
	r := OperationRecord{Request: q, Fingerprint: operationFingerprint(q), CommandFingerprint: operationCommandFingerprint(q, fields["body"]), State: state, AdmittedAt: now}
	if state != "running" {
		r.FinishedAt = &now
		r.Response = Response{Status: 204, SessionRevision: 5}
		if state == "unknown" {
			r.Response = Response{Status: 409, Body: []byte(`{"code":"outcome_unknown"}`)}
		}
	}
	return r
}

func TestNativeOperationReconciliation(t *testing.T) {
	for _, state := range []string{"running", "completed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			p := savedOperation(t)
			record := operationRecord(t, p, state)
			q := query()
			q.Operation, q.RequestID, q.OperationID = "", "lookup-request", "lookup-operation"
			// Current worker attribution and campaign revision need not match original.
			q.WorkerInstanceID, q.RunRevision = "new-worker", 5
			lookup, err := PrepareOperationStatus(q, p)
			if err != nil {
				t.Fatal(err)
			}
			c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/operations" {
					t.Fatal("wrong ledger")
				}
				raw, _ := io.ReadAll(r.Body)
				var wire struct {
					Request OperationRequest  `json:"request"`
					Body    map[string]string `json:"body"`
				}
				if json.Unmarshal(raw, &wire) != nil || wire.Request.Operation != "operation.status" || wire.Request.SessionID != p.Request().SessionID || wire.Request.WorkerInstanceID != "new-worker" || wire.Body["operation_id"] != p.Request().OperationID || len(wire.Body) != 1 {
					t.Fatal("wrong lookup", string(raw))
				}
				return wireResponse(t, 200, record), nil
			})}}
			r, err := c.Execute(context.Background(), lookup)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeOperationRecord(r, p)
			if err != nil || got.State != state || got.Request.WorkerInstanceID != "worker-original" {
				t.Fatal(got, err)
			}
			if state == "running" && got.Response.Status != 0 {
				t.Fatal("pending response became success")
			}
			if state == "unknown" && got.Response.Code() != "outcome_unknown" {
				t.Fatal("lost unknown outcome")
			}
		})
	}
}

func TestOperationLookupGuards(t *testing.T) {
	p := savedOperation(t)
	for _, change := range []func(*OperationRequest){
		func(q *OperationRequest) { q.Operation = "injection.delete" },
		func(q *OperationRequest) { q.OperationID = p.Request().OperationID },
		func(q *OperationRequest) { q.RequestID = p.Request().RequestID },
		func(q *OperationRequest) { q.CampaignID = "different" },
		func(q *OperationRequest) { q.SessionID = "replacement" },
		func(q *OperationRequest) { q.AttemptID = "attempt-1" },
	} {
		q := query()
		q.Operation, q.OperationID, q.RequestID = "", "lookup-operation", "lookup-request"
		change(&q)
		if _, err := PrepareOperationStatus(q, p); !errors.Is(err, ErrRequest) {
			t.Fatal("invalid lookup accepted", q, err)
		}
	}
}

func TestOperationRecordRejectsChangedCommandsAndMalformedResponses(t *testing.T) {
	p := savedOperation(t)
	for name, change := range map[string]func(*OperationRecord){
		"bad fingerprint":         func(r *OperationRecord) { r.Fingerprint = rawDigest([]byte("other")) },
		"bad command fingerprint": func(r *OperationRecord) { r.CommandFingerprint = rawDigest([]byte("other")) },
		"different session": func(r *OperationRecord) {
			r.Request.SessionID = "different"
			r.Fingerprint = operationFingerprint(r.Request)
		},
		"different operation": func(r *OperationRecord) {
			r.Request.Operation = "snapshot.create"
			r.Fingerprint = operationFingerprint(r.Request)
		},
		"different request id": func(r *OperationRecord) {
			r.Request.RequestID = "different"
			r.Fingerprint = operationFingerprint(r.Request)
		},
		"different body": func(r *OperationRecord) {
			r.Request.BodyDigest = rawDigest([]byte(`{}`))
			r.Fingerprint = operationFingerprint(r.Request)
		},
		"renewed deadline": func(r *OperationRecord) {
			r.Request.Deadline = r.Request.Deadline.Add(time.Minute)
			r.Fingerprint = operationFingerprint(r.Request)
		},
		"missing admitted time":        func(r *OperationRecord) { r.AdmittedAt = time.Time{} },
		"missing finished time":        func(r *OperationRecord) { r.FinishedAt = nil },
		"wrong state":                  func(r *OperationRecord) { r.State = "ready" },
		"bad nested status":            func(r *OperationRecord) { r.Response.Status = 302 },
		"bad nested body":              func(r *OperationRecord) { r.Response.Status = 200; r.Response.Body = []byte(`null`) },
		"pending with finished result": func(r *OperationRecord) { r.State = "running" },
	} {
		t.Run(name, func(t *testing.T) {
			record := operationRecord(t, p, "completed")
			change(&record)
			_, err := DecodeOperationRecord(Response{Status: 200, Body: encoded(t, record)}, p)
			assertUncertain(t, err)
		})
	}
	// Strictness applies inside nested request/response objects as well.
	for _, field := range []string{"request", "response"} {
		raw := encoded(t, operationRecord(t, p, "completed"))
		var record map[string]json.RawMessage
		_ = json.Unmarshal(raw, &record)
		var nested map[string]any
		_ = json.Unmarshal(record[field], &nested)
		if field == "request" {
			nested["Session_ID"] = "alias"
		} else {
			delete(nested, "session_revision")
		}
		record[field] = encoded(t, nested)
		_, err := DecodeOperationRecord(Response{Status: 200, Body: encoded(t, record)}, p)
		assertUncertain(t, err)
	}
	_, err := DecodeOperationRecord(Response{Status: 404, Body: []byte(`{"code":"operation_not_recorded_outcome_unknown"}`)}, p)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Response.Code() != "operation_not_recorded_outcome_unknown" {
		t.Fatal(err)
	}
}

func TestClosureRecordNativeCommandNormalization(t *testing.T) {
	q := query()
	q.Operation = "session.owner"
	// A saved owner body can have whitespace even though native command hashing
	// normalizes it. Its full request fingerprint still binds the raw body digest.
	p, err := PrepareOperation(q, []byte("{\n\"campaign_id\":\"campaign-1\",\"session_id\":\"sess-1\",\"action\":\"close_execution\"\n}"))
	if err != nil {
		t.Fatal(err)
	}
	record := operationRecord(t, p, "completed")
	q = record.Request
	q.WorkerInstanceID, q.RunRevision = "", 0
	q.BodyDigest = rawDigest([]byte(`{"action":"close_execution","session_id":"sess-1","campaign_id":"campaign-1"}`))
	if record.CommandFingerprint != operationFingerprint(q) {
		t.Fatal("native owner normalization mismatch")
	}
	if _, err := DecodeOperationRecord(Response{Status: 200, Body: encoded(t, record)}, p); err != nil {
		t.Fatal(err)
	}
	// Native legacy records may lack the supplemental command fingerprint.
	record.CommandFingerprint = ""
	if _, err := DecodeOperationRecord(Response{Status: 200, Body: encoded(t, record)}, p); err != nil {
		t.Fatal(err)
	}
}
