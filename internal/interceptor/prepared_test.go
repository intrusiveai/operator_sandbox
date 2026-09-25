package interceptor

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSavedNativeFramesPreserveBytes(t *testing.T) {
	q := OperationRequest{RequestID: "request-1", OperationID: "operation-1", Operation: "application.invoke", SessionID: "session-1", CampaignID: "campaign-1", WorkerInstanceID: "worker-1", RunRevision: 1, Deadline: time.Now().Add(time.Minute)}
	p, err := PrepareOperation(q, []byte(`{ "literal": "<>&", "n": 1.0 }`))
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte(" \n"), p.Bytes()...)
	raw = append(raw, '\n')
	loaded, err := ParsePreparedOperation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, loaded.Bytes()) || loaded.CommandFingerprint() != p.CommandFingerprint() {
		t.Fatal("saved request changed")
	}
	q.WorkerInstanceID = "worker-2"
	q.RunRevision = 2
	other, err := PrepareOperation(q, []byte(`{ "literal": "<>&", "n": 1.0 }`))
	if err != nil || other.CommandFingerprint() != p.CommandFingerprint() {
		t.Fatal("attribution changed identity", err)
	}
	changed, err := PrepareOperation(q, []byte(`{"literal":"different"}`))
	if err != nil || changed.CommandFingerprint() == p.CommandFingerprint() {
		t.Fatal("changed command collided", err)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"body":`), []byte(`"BODY":`), 1), bytes.Replace(raw, []byte(`"literal"`), []byte(`"changed"`), 1), []byte(`{"request":null,"body":{}}`)} {
		if _, err := ParsePreparedOperation(bad); err == nil {
			t.Fatal("accepted malformed saved request")
		}
	}
	wire := []byte("{\n\"status\":200,\"session_revision\":18446744073709551615,\"body\": {\"literal\":\"<>&\"}\n}")
	r, err := ParseResponse(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.Bytes(), wire) {
		t.Fatal("response serialization lost")
	}
	copied := r.Bytes()
	copied[0] = '!'
	if !bytes.Equal(r.Bytes(), wire) {
		t.Fatal("mutable raw response")
	}
	if len((Response{Status: 200}).Bytes()) != 0 {
		t.Fatal("manufactured wire bytes")
	}
	if _, err := ParsePreparedOperation([]byte(strings.Repeat(" ", JSONLimit+1))); err == nil {
		t.Fatal("accepted oversized frame")
	}
}
