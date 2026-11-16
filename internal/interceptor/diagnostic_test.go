package interceptor

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

func TestNativeFailureFieldsAndPrimaryRetention(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"a_future_native_failure","message":"scan mutable root /state/app: permission denied; api_key=sk-private-value","request_id":"req-1"},"private":"NEVER_COPY"}`,
		`{"code":"a_future_native_failure","message":"scan mutable root /state/app: permission denied; api_key=sk-private-value","request_id":"req-1","private":"NEVER_COPY"}`,
	} {
		r, err := ParseResponse([]byte(`{"status":409,"session_revision":26,"body":` + body + `}`))
		if err != nil {
			t.Fatal(err)
		}
		storage := errors.New("private storage error")
		var saved []byte
		_, err = Call("snapshot.create", "snapshot-1", func() (Response, error) { return r, nil }, func(r Response) error { saved = r.Bytes(); return storage }, func(r Response) (Response, error) { return r, success(r, nil) })
		f := diagnostictext.FailureFromError(err)
		var remote *RemoteError
		if f == nil || !f.Valid() || !errors.As(err, &remote) || !errors.Is(err, storage) || string(saved) != string(r.Bytes()) {
			t.Fatal(f, err)
		}
		for _, want := range []string{"snapshot.create", "snapshot-1", "a_future_native_failure", "permission denied", "req-1", rawDigest(r.Bytes())} {
			if !strings.Contains(f.DiagnosticMessage(), want) {
				t.Fatal(want, f)
			}
		}
		for _, secret := range []string{"sk-private-value", "NEVER_COPY", "private storage error"} {
			if strings.Contains(f.DiagnosticMessage(), secret) {
				t.Fatal("leak", f)
			}
		}
	}
}
func TestNativeFailuresAreNotExecutionPolicy(t *testing.T) {
	for _, status := range []int{202, 400, 403, 404, 409, 429, 500, 503} {
		r, _ := ParseResponse([]byte(strings.ReplaceAll(`{"status":STATUS,"session_revision":1,"body":{"code":"future_code","message":"native explanation"}}`, "STATUS", strconv.Itoa(status))))
		// The interpreter can accept an operation-specific response; capture must not
		// turn it into a failure, retry it, or make an unknown effect definite.
		calls := 0
		got, err := Call("operation", "id", func() (Response, error) { calls++; return r, nil }, nil, func(r Response) (Response, error) { return r, nil })
		if err != nil || calls != 1 || got.Status != status {
			t.Fatal(got, err, calls)
		}
	}
}

func TestNativeInvalidMessageIsOmittedWithoutLosingStatus(t *testing.T) {
	for _, body := range []string{`{"error":{"code":"native_failed","message":{"secret":"private"}}}`, `{"error":{"message":"\u001bprivate"}}`} {
		r, _ := ParseResponse([]byte(`{"status":409,"session_revision":1,"body":` + body + `}`))
		f := r.Failure("operation", "request")
		if !f.Valid() || f.HTTPStatus != 409 || f.Omissions == "" || strings.Contains(f.DiagnosticMessage(), "private") {
			t.Fatal(f)
		}
	}
}

func TestNativeDecoderFailureRetainsObservedResponse(t *testing.T) {
	r, err := ParseResponse([]byte(`{"status":200,"session_revision":1,"body":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Call("snapshot.create", "request-1", func() (Response, error) { return r, nil }, nil, func(r Response) (Response, error) { return r, invalidResponse() })
	f := diagnostictext.FailureFromError(err)
	if f == nil || f.HTTPStatus != 200 || f.ResponseDigest != rawDigest(r.Bytes()) || f.Operation != "snapshot.create" || f.Category != "invalid_response" {
		t.Fatal(f, err)
	}
}
