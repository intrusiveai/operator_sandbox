package interceptor

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// contractPin is the approved Interceptor OpenAPI identity, independent of
// product releases and the Operator/Harness wire package.
//
//go:embed contract.json
var contractPin []byte

type ContractIdentity struct {
	Version string `json:"interceptor_contract_version"`
	SHA256  string `json:"interceptor_contract_sha256"`
}

var supportedContract = func() ContractIdentity {
	var pin ContractIdentity
	if err := json.Unmarshal(contractPin, &pin); err != nil || !ValidContractVersion(pin.Version) || !ValidContractSHA256(pin.SHA256) {
		panic("invalid compiled Interceptor contract identity")
	}
	return pin
}()
var SupportedContractVersion = supportedContract.Version
var SupportedContractSHA256 = supportedContract.SHA256

func ValidContractSHA256(v string) bool {
	return len(v) == 64 && contractHashPattern.MatchString(v)
}

var contractHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var contractVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func ValidContractVersion(v string) bool {
	return len(v) <= 128 && contractVersionPattern.MatchString(v)
}

// CompatibilityError contains only selected facts from read-only discovery.
// It never wraps transport errors or retains unvalidated response content.
type CompatibilityError struct {
	Failure        *diagnostictext.Failure `json:"failure,omitempty"`
	Reason         string                  `json:"reason"`
	Expected       string                  `json:"expected"`
	Actual         string                  `json:"actual,omitempty"`
	HTTPStatus     int                     `json:"http_status,omitempty"`
	ExpectedSHA256 string                  `json:"expected_sha256,omitempty"`
	ActualSHA256   string                  `json:"actual_sha256,omitempty"`
}

func (e *CompatibilityError) DiagnosticMessage() string { return e.Error() }

func (e *CompatibilityError) Error() string {
	if e.Failure != nil {
		return e.Failure.DiagnosticMessage() + "; no campaign target operation was dispatched by this check"
	}
	if e.Reason == "hash_mismatch" || e.Reason == "version_mismatch" && e.ExpectedSHA256 != "" {
		return fmt.Sprintf("Operator requires Interceptor contract %s (SHA-256 %s); running Interceptor reports %s (SHA-256 %s). Install a compatible release; no campaign target operation was dispatched by this check", e.Expected, e.ExpectedSHA256, e.Actual, e.ActualSHA256)
	}
	if e.Reason == "version_mismatch" {
		return fmt.Sprintf("Operator requires Interceptor contract %s; running Interceptor reports %s. Use releases with matching integration contracts", e.Expected, e.Actual)
	}
	message := map[string]string{
		"endpoint_unavailable": "Interceptor compatibility endpoint is unavailable; install a release providing /v1/contract-version",
		"invalid_hash":         "Interceptor returned a missing or malformed contract SHA-256 hash; install a compatible release",
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

// ContractIdentity queries the running API without attaching, caching a binding,
// or trusting discovery's session_revision as a campaign session revision.
func (c *Client) ContractIdentity(ctx context.Context) (ContractIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.post(ctx, "/v1/contract-version", []byte(`{}`))
	failure := &CompatibilityError{Expected: SupportedContractVersion, ExpectedSHA256: SupportedContractSHA256, HTTPStatus: r.Status}
	if err != nil {
		failure.Failure = diagnostictext.FailureFromError(err)
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
		return ContractIdentity{}, failure
	}
	if r.Status != 200 {
		detail := r.Failure("contract-version", "")
		failure.Failure = &detail
		failure.Reason = "http_rejection"
		if r.Status == 404 || r.Status == 405 {
			failure.Reason = "endpoint_unavailable"
		}
		return ContractIdentity{}, failure
	}
	var body ContractIdentity
	if decodeClosed(r.Body, &body, []string{"interceptor_contract_version"}, []string{"interceptor_contract_sha256"}) != nil || !ValidContractVersion(body.Version) {
		failure.Reason = "invalid_version"
		return ContractIdentity{}, failure
	}
	if !ValidContractSHA256(body.SHA256) {
		failure.Reason = "invalid_hash"
		return ContractIdentity{}, failure
	}
	return body, nil
}

func (c *Client) CheckContract(ctx context.Context) error {
	identity, err := c.ContractIdentity(ctx)
	if err != nil {
		return err
	}
	reason := ""
	if identity.Version != SupportedContractVersion {
		reason = "version_mismatch"
	} else if identity.SHA256 != SupportedContractSHA256 {
		reason = "hash_mismatch"
	}
	if reason != "" {
		return &CompatibilityError{Reason: reason, Expected: SupportedContractVersion, Actual: identity.Version, ExpectedSHA256: SupportedContractSHA256, ActualSHA256: identity.SHA256, HTTPStatus: 200}
	}
	return nil
}

// Valid permits only bounded diagnostic facts suitable for retained start records.
func (e CompatibilityError) Valid() bool {
	if e.Failure != nil && !e.Failure.Valid() {
		return false
	}
	if !ValidContractVersion(e.Expected) || (e.HTTPStatus != 0 && (e.HTTPStatus < 100 || e.HTTPStatus > 599)) {
		return false
	}
	if e.ExpectedSHA256 != "" && !ValidContractSHA256(e.ExpectedSHA256) || e.ActualSHA256 != "" && !ValidContractSHA256(e.ActualSHA256) {
		return false
	}
	if e.Reason == "hash_mismatch" {
		return ValidContractVersion(e.Actual) && e.Actual == e.Expected && ValidContractSHA256(e.ExpectedSHA256) && ValidContractSHA256(e.ActualSHA256) && e.ActualSHA256 != e.ExpectedSHA256
	}
	if e.Reason == "version_mismatch" {
		return ValidContractVersion(e.Actual) && e.Actual != e.Expected
	}
	if e.Actual != "" || e.ActualSHA256 != "" {
		return false
	}
	switch e.Reason {
	case "endpoint_unavailable", "invalid_version", "invalid_hash", "invalid_response", "http_rejection", "transport_failure", "timeout", "canceled":
		return true
	}
	return false
}
