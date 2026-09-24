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
)

const Address = "http://127.0.0.1:8080"
const QueryTimeout = 30 * time.Second
const MaxOperationTimeout = 300 * time.Second

// CallError distinguishes local refusal from a sent request whose result is not
// trustworthy. Uncertain never permits a replacement mutation or resumed work.
type CallError struct {
	Kind      string
	Uncertain bool
}

func (e *CallError) Error() string { return "Interceptor call failed: " + e.Kind }

type Response struct {
	Status          int             `json:"status"`
	Body            json.RawMessage `json:"body,omitempty"`
	SessionRevision uint64          `json:"session_revision"`
}

// RemoteError preserves a valid native rejection separately from transport loss.
// A native error or 202 can still report uncertain/in-progress effects: callers
// must interpret the operation/code and reconcile, never assume non-execution.
type RemoteError struct{ Response Response }

func (e *RemoteError) Error() string { return "Interceptor returned a non-success response" }
func (r Response) Code() string {
	m, e := object(r.Body)
	if e != nil {
		return ""
	}
	var code string
	if json.Unmarshal(m["code"], &code) != nil || !identifier.MatchString(code) {
		return ""
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
				return nil, errors.New("nonlocal Interceptor destination")
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
	if len(raw) > JSONLimit || c == nil || c.http == nil {
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
		return Response{}, &CallError{Kind: "transport_unavailable", Uncertain: true}
	}
	defer response.Body.Close()
	fail := func() (Response, error) { return Response{}, &CallError{Kind: "invalid_response", Uncertain: true} }
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") || response.ContentLength > JSONLimit {
		return fail()
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, JSONLimit+1))
	if err != nil || len(raw) > JSONLimit || ctx.Err() != nil {
		return fail()
	}
	result, err := decodeResponse(raw)
	if err != nil {
		return fail()
	}
	expected := result.Status
	if expected == 204 {
		expected = 200
	}
	if response.StatusCode != expected {
		return fail()
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
