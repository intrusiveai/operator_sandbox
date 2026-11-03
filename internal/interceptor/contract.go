package interceptor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// SupportedContractVersion is the integration implemented by this executable,
// independent of both product releases and the Operator/Harness wire package.
const SupportedContractVersion = "0.1.0"

var contractVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func ValidContractVersion(v string) bool {
	return len(v) <= 128 && contractVersionPattern.MatchString(v)
}

// CompatibilityError contains only selected facts from read-only discovery.
// It never wraps transport errors or retains unvalidated response content.
type CompatibilityError struct {
	Reason     string `json:"reason"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func (e *CompatibilityError) Error() string {
	if e.Reason == "version_mismatch" {
		return fmt.Sprintf("Operator requires Interceptor contract %s; running Interceptor reports %s. Use releases with matching integration contracts", e.Expected, e.Actual)
	}
	message := map[string]string{
		"endpoint_unavailable": "Interceptor compatibility endpoint is unavailable; install a release providing /v1/contract-version",
		"invalid_version":      "Interceptor returned a missing or malformed contract version; install a compatible release",
		"invalid_response":     "Interceptor compatibility response failed validation",
		"http_rejection":       "Interceptor rejected the compatibility query",
		"transport_failure":    "cannot reach the local Interceptor compatibility endpoint; check that Interceptor is running",
		"timeout":              "Interceptor compatibility query timed out",
		"canceled":             "Interceptor compatibility query was canceled",
	}[e.Reason]
	if message == "" {
		message = "Interceptor compatibility check failed"
	}
	if e.HTTPStatus != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.HTTPStatus)
	}
	return message + "; no campaign target operation was dispatched by this check"
}

// ContractVersion queries the running API without attaching, caching a binding,
// or trusting discovery's session_revision as a campaign session revision.
func (c *Client) ContractVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.post(ctx, "/v1/contract-version", []byte(`{}`))
	failure := &CompatibilityError{Expected: SupportedContractVersion, HTTPStatus: r.Status}
	if err != nil {
		failure.Reason = "transport_failure"
		var call *CallError
		if errors.As(err, &call) {
			failure.HTTPStatus = call.HTTPStatus
			if call.Kind == "invalid_response" {
				failure.Reason = "invalid_response"
			}
		}
		if failure.HTTPStatus == 404 || failure.HTTPStatus == 405 {
			failure.Reason = "endpoint_unavailable"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failure.Reason = "timeout"
		} else if ctx.Err() != nil {
			failure.Reason = "canceled"
		}
		return "", failure
	}
	if r.Status != 200 {
		failure.Reason = "http_rejection"
		if r.Status == 404 || r.Status == 405 {
			failure.Reason = "endpoint_unavailable"
		}
		return "", failure
	}
	var body struct {
		Version string `json:"interceptor_contract_version"`
	}
	if decodeClosed(r.Body, &body, []string{"interceptor_contract_version"}, nil) != nil || !ValidContractVersion(body.Version) {
		failure.Reason = "invalid_version"
		return "", failure
	}
	return body.Version, nil
}

func (c *Client) CheckContract(ctx context.Context) error {
	version, err := c.ContractVersion(ctx)
	if err != nil {
		return err
	}
	if version != SupportedContractVersion {
		return &CompatibilityError{Reason: "version_mismatch", Expected: SupportedContractVersion, Actual: version, HTTPStatus: 200}
	}
	return nil
}
