package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

const Address = "http://127.0.0.1:8080"
const QueryTimeout = 30 * time.Second
const MaxOperationTimeout = 300 * time.Second

// CallError distinguishes local refusal from a sent request whose result is not
// trustworthy. Uncertain never permits a replacement mutation or resumed work.
type CallError struct {
	Kind       string
	Uncertain  bool
	HTTPStatus int
	Detail     string
}

func (e *CallError) Error() string { return "Interceptor call failed: " + e.Kind }

// DiagnosticMessage selects protocol/transport facts, never a transport's text.
func (e *CallError) DiagnosticMessage() string { return e.DiagnosticFailure().DiagnosticMessage() }

type Response struct {
	operation       string
	requestID       string
	raw             []byte
	Status          int             `json:"status"`
	Body            json.RawMessage `json:"body,omitempty"`
	SessionRevision uint64          `json:"session_revision"`
}

// RemoteError preserves a valid native rejection separately from transport loss.
// A native error or 202 can still report uncertain/in-progress effects: callers
// must interpret the operation/code and reconcile, never assume non-execution.
type RemoteError struct{ Response Response }

func (e *RemoteError) Error() string             { return "Interceptor returned a non-success response" }
func (e *RemoteError) DiagnosticMessage() string { return e.DiagnosticFailure().DiagnosticMessage() }
func (r Response) Code() string {
	m, e := object(r.Body)
	if e != nil {
		return ""
	}
	// Admission/lifecycle rejections carry body.code; dispatched native HTTP
	// handlers carry body.error.code. Never interpret arbitrary messages or
	// choose between conflicting representations.
	code := ""
	if raw, exists := m["code"]; exists {
		if json.Unmarshal(raw, &code) != nil || !identifier.MatchString(code) {
			return ""
		}
	}
	if raw, exists := m["error"]; exists {
		nested, err := object(raw)
		var native string
		if err != nil || json.Unmarshal(nested["code"], &native) != nil || !identifier.MatchString(native) || (code != "" && code != native) {
			return ""
		}
		code = native
	}
	return code
}

type Client struct{ http *http.Client }

// New has no endpoint/credential/route configuration. Disable proxies, redirects,
// compression and connection reuse; POST bodies cannot be automatically replayed.
func New() *Client {
	dial := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "127.0.0.1:8080" {
				return nil, diagnostictext.New("nonlocal Interceptor destination")
			}
			return dial.DialContext(ctx, "tcp4", address)
		}}
	return &Client{http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

func (c *Client) post(ctx context.Context, route string, raw []byte) (Response, error) {
	return c.postBounded(ctx, route, raw, JSONLimit)
}

func (c *Client) postBounded(ctx context.Context, route string, raw []byte, responseLimit int64) (result Response, callErr error) {
	var identity struct {
		Operation string `json:"operation"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(raw, &identity)
	if identity.Operation == "" {
		identity.Operation = route
	}
	defer func() {
		result.operation = identity.Operation
		result.requestID = identity.RequestID
		callErr = diagnostictext.Context(callErr, "Interceptor", identity.Operation, identity.RequestID)
	}()
	if len(raw) > JSONLimit || responseLimit <= 0 || responseLimit > JSONLimit || c == nil || c.http == nil {
		return Response{}, &CallError{Kind: "invalid_request"}
	}
	if ctx.Err() != nil {
		return Response{}, &CallError{Kind: "canceled_before_dispatch"}
	}
	// NopCloser prevents net/http from adding GetBody (and therefore replaying it).
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Address+route, io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		return Response{}, &CallError{Kind: "invalid_request"}
	}
	req.ContentLength = int64(len(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	response, err := c.http.Do(req)
	if err != nil {
		kind := "transport_unavailable"
		if ctx.Err() == context.DeadlineExceeded {
			kind = "deadline_exceeded"
		} else if ctx.Err() != nil {
			kind = "canceled"
		}
		return Response{}, &CallError{Kind: kind, Uncertain: true}
	}
	defer response.Body.Close()
	return readJSONResponse(ctx, response, responseLimit)
}

func readJSONResponse(ctx context.Context, response *http.Response, responseLimit int64) (Response, error) {
	fail := func(detail string) (Response, error) {
		return Response{}, &CallError{Kind: "invalid_response", Uncertain: true, HTTPStatus: response.StatusCode, Detail: detail}
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return fail("Response Content-Type must be application/json.")
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fail("Response Content-Encoding must be absent or identity.")
	}
	if response.ContentLength > responseLimit {
		return fail("Declared response size exceeds the configured byte limit.")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if ctx.Err() != nil {
		kind := "canceled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind = "deadline_exceeded"
		}
		return Response{}, &CallError{Kind: kind, Uncertain: true, HTTPStatus: response.StatusCode, Detail: "Response reading stopped when the request context ended."}
	}
	if err != nil {
		return fail("Response body reading was interrupted; the complete response was not received.")
	}
	if int64(len(raw)) > responseLimit {
		return fail("Received response size exceeds the configured byte limit.")
	}
	result, err := decodeResponse(raw)
	if err != nil {
		return fail("Response JSON or native envelope is invalid.")
	}
	expected := result.Status
	if expected == 204 {
		expected = 200
	}
	if response.StatusCode != expected {
		return fail("HTTP status does not match the native envelope status.")
	}
	return result, nil
}

// decodeResponse also validates responses nested in durable operation records.
// These have no separate HTTP status, but retain the native framing rules.
func decodeResponse(raw []byte) (Response, error) {
	var result Response
	if decodeClosed(raw, &result, []string{"status", "session_revision"}, []string{"body"}) != nil || result.Status < 200 || result.Status >= 600 || result.Status >= 300 && result.Status < 400 {
		return Response{}, invalidResponse()
	}
	// A 204 native operation may omit body; every other response must contain an object.
	if result.Status != 204 {
		if _, err := object(result.Body); err != nil {
			return Response{}, invalidResponse()
		}
	} else if len(result.Body) != 0 && !bytes.Equal(result.Body, []byte("null")) {
		if _, err := object(result.Body); err != nil {
			return Response{}, invalidResponse()
		}
	}
	result.raw = bytes.Clone(raw)
	return result, nil
}

// Execute never retries, renews a deadline or changes saved request bytes. A lost
// reply requires operation.status reconciliation under the host terminal policy.
func (c *Client) Execute(ctx context.Context, p PreparedOperation) (Response, error) {
	if len(p.envelope) == 0 {
		return Response{}, &CallError{Kind: "invalid_request"}
	}
	deadline := p.request.Deadline
	if cap := time.Now().Add(MaxOperationTimeout); deadline.After(cap) {
		deadline = cap
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return c.post(ctx, "/v1/operations", p.envelope)
}

func success(r Response, err error) error {
	if err != nil {
		return err
	}
	if r.Status != 200 {
		return &RemoteError{r}
	}
	return nil
}

func invalidResponse() error { return &CallError{Kind: "invalid_response", Uncertain: true} }
