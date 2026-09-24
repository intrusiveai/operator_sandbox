package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func encoded(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func wireResponse(t *testing.T, status int, body any) *http.Response {
	raw := encoded(t, Response{Status: status, Body: encoded(t, body), SessionRevision: 4})
	httpStatus := status
	if status == 204 {
		httpStatus = 200
	}
	return &http.Response{StatusCode: httpStatus, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}
}
func fakeClient(t *testing.T, route string, body any) *Client {
	t.Helper()
	return &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != Address+route || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept-Encoding") != "identity" || r.GetBody != nil || r.Header.Get("Authorization") != "" {
			t.Error("unexpected HTTP request", r)
		}
		return wireResponse(t, 200, body), nil
	})}}
}
func query() OperationRequest {
	return OperationRequest{RequestID: "request-1", OperationID: "operation-1", Operation: "session.status", SessionID: "sess-1", CampaignID: "campaign-1", WorkerInstanceID: "worker-2", RunRevision: 2, ExpectedSessionRevision: 4, Deadline: time.Now().UTC().Add(time.Minute)}
}

func TestNativeGoldenEncoding(t *testing.T) {
	raw, e := os.ReadFile("testdata/operation-request-v1alpha2.json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Request OperationRequest `json:"request"`
		Body    json.RawMessage  `json:"body"`
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	got, e := encodeOperation(v.Request, v.Body)
	if e != nil || !bytes.Equal(got, bytes.TrimSpace(raw)) || rawDigest(v.Body) != v.Request.BodyDigest {
		t.Fatal(string(got), e)
	}
	// The codec can reproduce Interceptor's fixture, but public bind admission
	// must use attach. Operator must not issue this legacy bind operation.
	if _, e := PrepareOperation(v.Request, v.Body); e == nil {
		t.Fatal("public bind accepted")
	}
}

func TestExactBodyFreezeAndDispatch(t *testing.T) {
	q := query()
	q.Operation = "application.invoke"
	body := []byte("{\n  \"input\" : \"<tag>&\\u003e\", \"n\":1.0\n}")
	prepared, e := PrepareOperation(q, body)
	if e != nil {
		t.Fatal(e)
	}
	want := bytes.Clone(prepared.Bytes())
	body[0] = '!'
	prepared.Bytes()[0] = '!'
	c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		raw, e := io.ReadAll(r.Body)
		if e != nil || !bytes.Equal(raw, want) {
			t.Error("body changed", e)
		}
		var v struct {
			Request OperationRequest `json:"request"`
			Body    json.RawMessage  `json:"body"`
		}
		if e := json.Unmarshal(raw, &v); e != nil || rawDigest(v.Body) != v.Request.BodyDigest {
			t.Error(e)
		}
		if !bytes.Contains(v.Body, []byte("<tag>&")) || !bytes.Contains(v.Body, []byte("1.0")) {
			t.Error("body normalized")
		}
		return wireResponse(t, 202, map[string]string{"code": "operation_in_progress"}), nil
	})}}
	r, e := c.Execute(context.Background(), prepared)
	if e != nil || r.Status != 202 || r.Code() != "operation_in_progress" {
		t.Fatal(r, e)
	}
}

func TestRequestGuards(t *testing.T) {
	bodyAtNativeLimit := []byte(`{"x":` + strings.Repeat("[", 63) + "0" + strings.Repeat("]", 63) + "}")
	if _, err := object(bodyAtNativeLimit); err != nil {
		t.Fatal("fixture should fit as a standalone native object", err)
	}
	if _, err := PrepareOperation(query(), bodyAtNativeLimit); err == nil {
		t.Fatal("envelope nesting overhead ignored")
	}
	if _, err := PrepareOperation(query(), []byte(`{"x":"`+strings.Repeat("x", JSONLimit-16)+`"}`)); err == nil {
		t.Fatal("envelope byte overhead ignored")
	}
	for _, body := range []string{"null", "[]", "{} {}", "{\"x\":1,\"x\":2}", "{\"x\":{\"z\":1,\"z\":2}}", " {} ", "{\"x\":\"\xff\"}", "{\"x\":" + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + "}"} {
		if _, e := PrepareOperation(query(), []byte(body)); e == nil {
			t.Fatal("accepted", body)
		}
	}
	for _, change := range []func(*OperationRequest){func(q *OperationRequest) { q.Operation = "shell.exec" }, func(q *OperationRequest) { q.Operation = "session.stop" }, func(q *OperationRequest) { q.OperationID = "local-reserved" }, func(q *OperationRequest) { q.CampaignID = "../bad" }, func(q *OperationRequest) { q.RunRevision = 0 }, func(q *OperationRequest) { q.BodyDigest = rawDigest([]byte("other")) }, func(q *OperationRequest) { q.Deadline = time.Time{} }} {
		q := query()
		change(&q)
		if _, e := PrepareOperation(q, []byte(`{}`)); e == nil {
			t.Fatal("accepted", q)
		}
	}
	// Native values above the shared contract's safe-integer bound remain exact.
	if _, e := object([]byte(`{"native_revision":18446744073709551615}`)); e != nil {
		t.Fatal(e)
	}
}

func TestResponseFramingAndUncertainty(t *testing.T) {
	p, e := PrepareOperation(query(), []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"duplicate", "missing revision", "null revision", "wrong status", "unknown field", "redirect", "oversize", "compressed", "transport loss"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			c := &Client{http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				response := wireResponse(t, 200, map[string]string{"ok": "yes"})
				raw := ""
				switch name {
				case "duplicate":
					raw = `{"status":200,"body":{},"body":{},"session_revision":0}`
				case "missing revision":
					raw = `{"status":200,"body":{}}`
				case "null revision":
					raw = `{"status":200,"body":{},"session_revision":null}`
				case "unknown field":
					raw = `{"status":200,"body":{},"session_revision":0,"unexpected":true}`
				case "wrong status":
					response.StatusCode = 500
				case "redirect":
					response.StatusCode = 307
					response.Header.Set("Location", "https://example.invalid/secret")
				case "oversize":
					raw = strings.Repeat(" ", JSONLimit+1)
				case "compressed":
					response.Header.Set("Content-Encoding", "gzip")
				case "transport loss":
					return nil, io.ErrUnexpectedEOF
				}
				if raw != "" {
					response.ContentLength = -1
					response.Body = io.NopCloser(strings.NewReader(raw))
				}
				return response, nil
			})}}
			_, e := c.Execute(context.Background(), p)
			var failure *CallError
			if !errors.As(e, &failure) || !failure.Uncertain || calls != 1 {
				t.Fatal(e, calls)
			}
		})
	}
	for _, body := range []any{nil, map[string]any{}} {
		c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return wireResponse(t, 204, body), nil })}}
		r, e := c.Execute(context.Background(), p)
		if e != nil || r.Status != 204 {
			t.Fatal(r, e)
		}
	}
}

func TestNoDispatchAfterCancellationOrExpiredDeadline(t *testing.T) {
	calls := 0
	c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })}}
	q := query()
	q.Deadline = time.Now().Add(-time.Second)
	p, e := PrepareOperation(q, []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Execute(context.Background(), p)
	var failure *CallError
	if !errors.As(e, &failure) || failure.Uncertain || calls != 0 {
		t.Fatal(e, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.Attach(ctx, "campaign-1", "worker-1", false); !errors.As(e, &failure) || failure.Uncertain || calls != 0 {
		t.Fatal(e, calls)
	}
}

func attachmentBody(t *testing.T) map[string]any {
	caps, e := os.ReadFile("testdata/capability-delivery.json")
	if e != nil {
		t.Fatal(e)
	}
	m, e := object(caps)
	if e != nil {
		t.Fatal(e)
	}
	value := func(key string) string {
		var s string
		if e := json.Unmarshal(m[key], &s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	session := map[string]any{"id": "sess-1", "campaign_id": "campaign-1", "operation_api_version": OperationVersion, "revision": 4, "phase": "running", "feedback_profile": "diagnostic", "environment_digest": value("environment_digest"), "app_digest": value("application_digest"), "capability_manifest_digest": value("digest"), "environment_path": "/protected/host/environment"}
	return map[string]any{"api_version": LifecycleVersion, "campaign_id": "campaign-1", "binding": Binding{"sess-1", "worker-2", 2}, "session": session, "capabilities": json.RawMessage(caps), "evidence_max_bytes": int64(4 << 30)}
}

func TestAttachmentAndNativeBindings(t *testing.T) {
	body := attachmentBody(t)
	c := fakeClient(t, "/v1/attach", body)
	got, e := c.Attach(context.Background(), "campaign-1", "worker-2", false)
	if e != nil || got.Session.Revision != 4 || got.Binding.RunRevision != 2 || got.Session.FeedbackProfile != "diagnostic" || got.EvidenceMaxBytes != 4<<30 || len(got.Capabilities) == 0 {
		t.Fatal(got, e)
	}
	// Unknown metadata fields cannot override exact lowercase routing keys.
	body["session"].(map[string]any)["ID"] = "alias-session"
	got, e = fakeClient(t, "/v1/attach", body).Attach(context.Background(), "campaign-1", "worker-2", false)
	if e != nil || got.Session.ID != "sess-1" {
		t.Fatal(got, e)
	}
	for _, kind := range []string{"campaign", "worker", "session", "api", "profile", "digest", "null revision"} {
		t.Run(kind, func(t *testing.T) {
			body := attachmentBody(t)
			s := body["session"].(map[string]any)
			switch kind {
			case "campaign":
				body["campaign_id"] = "other"
			case "worker":
				body["binding"] = Binding{"sess-1", "wrong", 2}
			case "session":
				s["id"] = "other"
			case "api":
				s["operation_api_version"] = "v1"
			case "profile":
				s["feedback_profile"] = "full"
			case "digest":
				s["capability_manifest_digest"] = rawDigest(nil)
			case "null revision":
				s["revision"] = nil
			}
			if _, e := fakeClient(t, "/v1/attach", body).Attach(context.Background(), "campaign-1", "worker-2", false); e == nil {
				t.Fatal("invalid attach accepted")
			}
		})
	}
}

func TestStatusTerminalAndWorkerAttribution(t *testing.T) {
	body := map[string]any{"instance_id": "instance-1", "campaign_id": "campaign-1", "active": Binding{"sess-1", "original-worker", 2}, "sessions": map[string]Binding{"sess-1": {"sess-1", "original-worker", 2}}, "phase": "ready", "closed": false, "store_available": true, "evidence_max_bytes": 4 << 30, "failure": nil}
	s, e := fakeClient(t, "/v1/status", body).Status(context.Background(), "campaign-1")
	if e != nil || !s.Ready() || !s.Matches("instance-1", Binding{"sess-1", "different-worker", 2}) || s.Matches("replacement-instance", s.Active) {
		t.Fatal(s, e)
	}
	body["phase"] = "error"
	body["closed"] = true
	body["failure"] = Failure{"sess-1", "WALL_TIME_LIMIT", time.Now().UTC()}
	s, e = fakeClient(t, "/v1/status", body).Status(context.Background(), "campaign-1")
	if e != nil || s.Ready() || s.Failure == nil || s.Failure.Reason != "WALL_TIME_LIMIT" {
		t.Fatal(s, e)
	}
	delete(body, "closed")
	if _, e := fakeClient(t, "/v1/status", body).Status(context.Background(), "campaign-1"); e == nil {
		t.Fatal("absent closed flag accepted")
	}
}

func TestClosureHasOneShapeAndPreservesOriginalAttribution(t *testing.T) {
	q := query()
	q.Operation = ""
	p, e := PrepareClose(q)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	owner := Owner{Principal: "instance-1", CampaignID: "campaign-1", WorkerInstanceID: "original-worker", RunRevision: 1, BoundAt: now.Add(-time.Minute), ClosedAt: &now, ClosureReason: "controller_closed_execution"}
	for _, phase := range []string{"live", "stopped"} {
		t.Run(phase, func(t *testing.T) {
			r, e := fakeClient(t, "/v1/operations", owner).Execute(context.Background(), p)
			if e != nil {
				t.Fatal(e)
			}
			got, e := DecodeClosure(r, "campaign-1")
			if e != nil || got.ClosedAt == nil || got.WorkerInstanceID != "original-worker" {
				t.Fatal(got, e)
			}
		})
	}
	for _, bad := range []any{map[string]any{"owner": owner}, map[string]any{"campaign_id": "campaign-1"}} {
		if _, e := DecodeClosure(Response{Status: 200, Body: encoded(t, bad)}, "campaign-1"); e == nil {
			t.Fatal("invalid closure accepted")
		}
	}
}

func TestProductionTransportPolicy(t *testing.T) {
	c := New()
	defer c.Close()
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy != nil || !tr.DisableCompression || !tr.DisableKeepAlives || tr.MaxResponseHeaderBytes != 16<<10 {
		t.Fatal("transport policy mismatch")
	}
	if _, e := tr.DialContext(context.Background(), "tcp", "example.invalid:8080"); e == nil {
		t.Fatal("nonlocal destination accepted")
	}
	if e := c.http.CheckRedirect(nil, nil); e != http.ErrUseLastResponse {
		t.Fatal(e)
	}
}

func TestRealHTTPPreservesBodyAndDoesNotReplayLostReply(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		var envelope struct {
			Request OperationRequest `json:"request"`
			Body    json.RawMessage  `json:"body"`
		}
		if err != nil || json.Unmarshal(raw, &envelope) != nil || rawDigest(envelope.Body) != envelope.Request.BodyDigest {
			t.Error("native body digest mismatch", err)
		}
		if r.Method != "POST" || r.URL.Path != "/v1/operations" || r.Host != "127.0.0.1:8080" {
			t.Error("unexpected HTTP destination")
		}
		if calls.Load() == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Response{Status: 204, Body: json.RawMessage("null"), SessionRevision: 5})
			return
		}
		// Model an accepted native effect followed by connection loss. The client
		// must report uncertainty and must not submit another request automatically.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	c := New()
	defer c.Close()
	transport := c.http.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "127.0.0.1:8080" {
			t.Error("unexpected dial destination", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp4", srv.Listener.Addr().String())
	}
	q := query()
	q.Operation = "injection.delete"
	p, err := PrepareOperation(q, []byte("{ \"injection_id\": \"<exact>&\" }"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Execute(context.Background(), p)
	if err != nil || r.Status != 204 || r.SessionRevision != 5 {
		t.Fatal(r, err)
	}
	_, err = c.Execute(context.Background(), p)
	var failure *CallError
	if !errors.As(err, &failure) || !failure.Uncertain || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
}
