package interceptor

import (
	"encoding/json"
	"errors"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// Failure selects only native error fields, never a response body or headers.
// Both native error envelope forms are supported. Operation semantics (including
// tolerated rejections and uncertain effects) remain the caller's decision.
func (r Response) Failure(operation, request string) diagnostictext.Failure {
	f := diagnostictext.Failure{Component: "Interceptor", Operation: operation, RequestID: request, Category: "remote_response", HTTPStatus: r.Status}
	if f.Operation == "" {
		f.Operation = r.operation
	}
	if f.RequestID == "" {
		f.RequestID = r.requestID
	}
	if len(r.raw) > 0 {
		f.ResponseDigest = rawDigest(r.raw)
	}
	if r.Status >= 400 {
		f.Category = "remote_rejection"
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(r.Body, &body) != nil {
		f.Omissions = "error_fields:unavailable;"
		return f.Bounded()
	}
	if nested, ok := body["error"]; ok {
		var detail map[string]json.RawMessage
		if json.Unmarshal(nested, &detail) != nil {
			f.Omissions = "error_fields:invalid;"
			return f.Bounded()
		}
		body = detail
	}
	for _, field := range []struct {
		name   string
		target *string
	}{{"code", &f.Code}, {"message", &f.Message}, {"request_id", &f.RemoteRequestID}} {
		if raw, ok := body[field.name]; ok && json.Unmarshal(raw, field.target) != nil {
			f.Omissions += field.name + ":invalid;"
		}
	}
	return f.Bounded()
}
func (e *RemoteError) DiagnosticFailure() diagnostictext.Failure { return e.Response.Failure("", "") }
func (e *CallError) DiagnosticFailure() diagnostictext.Failure {
	detail := e.Detail
	if e.Uncertain {
		detail += " The target-side outcome is uncertain."
	}
	return (diagnostictext.Failure{Component: "Interceptor", Category: e.Kind, HTTPStatus: e.HTTPStatus, Message: detail}).Bounded()
}

// Call is the common native exchange boundary. It always interprets the received
// response before combining a secondary retention failure. Decoders can accept
// operation-specific non-2xx results without losing native data on other errors.
func Call[T any](operation, request string, invoke func() (Response, error), retain func(Response) error, decode func(Response) (T, error)) (value T, err error) {
	r, err := invoke()
	r.operation = operation
	r.requestID = request
	if err == nil {
		value, err = decode(r)
	}
	err = ResponseError(operation, request, r, err)
	if retain != nil {
		save := retain(r)
		if save != nil {
			if f := diagnostictext.FailureFromError(err); f != nil {
				f.Omissions += "response_retention:failed;"
				err = diagnostictext.WithFailure(err, *f)
			}
		}
		err = errors.Join(err, save)
	}
	return value, err
}

func ResponseError(operation, request string, r Response, err error) error {
	if err == nil {
		return nil
	}
	if prior := diagnostictext.FailureFromError(err); prior != nil {
		if prior.Operation == "" {
			prior.Operation = operation
		}
		if prior.RequestID == "" {
			prior.RequestID = request
		}
		if prior.HTTPStatus == 0 {
			prior.HTTPStatus = r.Status
		}
		if prior.ResponseDigest == "" && len(r.raw) > 0 {
			prior.ResponseDigest = rawDigest(r.raw)
		}
		return diagnostictext.WithFailure(err, *prior)
	}
	if r.Status >= 400 {
		return diagnostictext.WithFailure(err, r.Failure(operation, request))
	}
	if r.Status != 0 {
		f := r.Failure(operation, request)
		f.Category = "response_validation"
		f.Code, f.Message, f.RemoteRequestID = "", "", ""
		if detail, ok := diagnostictext.FromError(err); ok {
			f.Message = detail
		}
		return diagnostictext.WithFailure(err, f)
	}
	return diagnostictext.Context(err, "Interceptor", operation, request)
}

// StatusError preserves an explicit native failure independently of readiness.
func StatusError(status Status, err error, request string) error {
	if err != nil {
		return diagnostictext.Context(err, "Interceptor", "status", request)
	}
	f := diagnostictext.Failure{Component: "Interceptor", Operation: "status", RequestID: request, Category: "state_validation", Message: "The returned session is not ready or does not match the owned binding."}
	if status.Failure != nil {
		f.Category = "session_failure"
		f.Message = status.Failure.Reason
	}
	return diagnostictext.WithFailure(ErrRequest, f)
}

func (r Response) Validate() (Response, error) {
	verified, err := ParseResponse(r.Bytes())
	verified.operation, verified.requestID = r.operation, r.requestID
	return verified, err
}

// DecodeDeletion confirms the existing deletion/absence semantics. It never
// interprets an uncertain effect as absence and never performs another request.
func DecodeDeletion(r Response, priorRevision uint64) (Response, error) {
	verified, err := r.Validate()
	if err != nil {
		return r, err
	}
	if verified.SessionRevision >= priorRevision && (verified.Status == 204 && (len(verified.Body) == 0 || string(verified.Body) == "null") && verified.SessionRevision > priorRevision || verified.Status == 404 && verified.Code() == "not_found") {
		return verified, nil
	}
	if verified.Status >= 400 {
		return verified, &RemoteError{Response: verified}
	}
	return verified, diagnostictext.New("Injection deletion response did not confirm cleanup.")
}
