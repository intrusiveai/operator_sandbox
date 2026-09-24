// Package interceptor implements the host-only, trusted-loopback native client.
// Native identities and JSON rules are separate from the harness wire contract.
package interceptor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const LifecycleVersion = "interceptor.dev/local-lifecycle/v1alpha1"
const OperationVersion = "interceptor.dev/operation-request/v1alpha2"
const JSONLimit = 5 << 20

var ErrRequest = errors.New("invalid or oversized Interceptor request")
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type OperationRequest struct {
	APIVersion              string    `json:"api_version"`
	RequestID               string    `json:"request_id"`
	OperationID             string    `json:"operation_id"`
	Operation               string    `json:"operation"`
	SessionID               string    `json:"session_id"`
	CampaignID              string    `json:"campaign_id"`
	WorkerInstanceID        string    `json:"worker_instance_id"`
	RunRevision             uint64    `json:"run_revision"`
	ExpectedSessionRevision uint64    `json:"expected_session_revision,omitempty"`
	AttemptID               string    `json:"attempt_id,omitempty"`
	AttemptContextDigest    string    `json:"attempt_context_digest,omitempty"`
	BodyDigest              string    `json:"body_digest"`
	Deadline                time.Time `json:"deadline"`
}

// PreparedOperation freezes the exact envelope to persist before dispatch. It
// grants no admission authority; the broker must enforce policy and journaling.
type PreparedOperation struct {
	request  OperationRequest
	envelope []byte
}

func (p PreparedOperation) Request() OperationRequest { return p.request }
func (p PreparedOperation) Bytes() []byte             { return bytes.Clone(p.envelope) }

func rawDigest(raw []byte) string {
	s := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(s[:])
}

var operations = map[string]bool{
	"session.owner": true, "session.status": true, "operation.status": true, "capabilities.read": true,
	"artifact.register": true, "artifact.read": true, "attempt.register": true, "injection.arm": true, "injection.delete": true,
	"application.invoke": true, "observation.read": true, "observation.content.read": true, "snapshot.create": true,
}

// PrepareOperation computes the raw native body digest. Outer whitespace is
// rejected because JSON RawMessage decoding does not retain value delimiters.
// Interior whitespace and HTML-sensitive characters are preserved byte-for-byte.
func PrepareOperation(q OperationRequest, body []byte) (PreparedOperation, error) {
	if len(body) > JSONLimit || !bytes.Equal(bytes.TrimSpace(body), body) {
		return PreparedOperation{}, ErrRequest
	}
	if _, err := object(body); err != nil {
		return PreparedOperation{}, ErrRequest
	}
	if q.APIVersion == "" {
		q.APIVersion = OperationVersion
	}
	if q.BodyDigest == "" {
		q.BodyDigest = rawDigest(body)
	}
	if q.APIVersion != OperationVersion || !operations[q.Operation] || q.Deadline.IsZero() || q.RunRevision == 0 || q.BodyDigest != rawDigest(body) {
		return PreparedOperation{}, ErrRequest
	}
	for _, id := range []string{q.RequestID, q.OperationID, q.SessionID, q.CampaignID, q.WorkerInstanceID} {
		if !identifier.MatchString(id) {
			return PreparedOperation{}, ErrRequest
		}
	}
	if strings.HasPrefix(q.RequestID, "local-") || strings.HasPrefix(q.OperationID, "local-") || (q.AttemptID != "" && !identifier.MatchString(q.AttemptID)) || (q.AttemptContextDigest != "" && !digest.MatchString(q.AttemptContextDigest)) {
		return PreparedOperation{}, ErrRequest
	}
	if q.Operation == "session.owner" {
		var owner struct {
			Action     string `json:"action"`
			SessionID  string `json:"session_id"`
			CampaignID string `json:"campaign_id"`
		}
		if decodeClosed(body, &owner, []string{"action", "session_id", "campaign_id"}, nil) != nil || owner.Action != "close_execution" || owner.SessionID != q.SessionID || owner.CampaignID != q.CampaignID {
			return PreparedOperation{}, ErrRequest
		}
	}
	raw, err := encodeOperation(q, body)
	if err != nil || len(raw) > JSONLimit {
		return PreparedOperation{}, ErrRequest
	}
	// The envelope adds a nesting level to body; enforce the native bound on
	// the complete frame, not just on the embedded object.
	if _, err := object(raw); err != nil {
		return PreparedOperation{}, ErrRequest
	}
	return PreparedOperation{q, raw}, nil
}

func encodeOperation(q OperationRequest, body []byte) ([]byte, error) {
	header, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	raw := append([]byte(`{"request":`), header...)
	raw = append(raw, []byte(`,"body":`)...)
	raw = append(raw, body...)
	return append(raw, '}'), nil
}

// object mirrors native strict framing: object root, UTF-8, duplicate detection,
// depth at most 64 and no trailing values. Native uint64s are not jcs-v1 numbers.
func object(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > JSONLimit || !utf8.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, ErrRequest
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return ErrRequest
		}
		token, err := d.Token()
		if err != nil {
			return ErrRequest
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return ErrRequest
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return ErrRequest
				}
				seen[s] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrRequest
		}
		if _, err := d.Token(); err != nil {
			return ErrRequest
		}
		return nil
	}
	if walk(0) != nil {
		return nil, ErrRequest
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrRequest
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil, ErrRequest
	}
	return m, nil
}

func decodeClosed(raw []byte, target any, required, optional []string) error {
	m, err := object(raw)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, k := range required {
		v, ok := m[k]
		if !ok || bytes.Equal(v, []byte("null")) {
			return ErrRequest
		}
		allowed[k] = true
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return ErrRequest
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return ErrRequest
	}
	return nil
}
