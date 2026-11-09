//go:build linux || darwin

package campaign

import (
	"fmt"
	"slices"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// TargetFailure contains host-selected native response or boundary failure facts.
// Detail is untrusted target text, bounded and filtered for credential patterns.
// ResponseDigest refers to the original, private native step response.
type TargetFailure struct {
	NativeOperation string `json:"native_operation"`
	Operation       string `json:"target_operation"`
	AttemptID       string `json:"attempt_id"`
	TurnID          string `json:"turn_id,omitempty"`
	StepID          string `json:"step_id"`
	ResponseDigest  string `json:"response_digest"`
	HTTPStatus      int    `json:"http_status"`
	Code            string `json:"native_code,omitempty"`
	Category        string `json:"category"`
	Detail          string `json:"detail,omitempty"`
	Omission        string `json:"detail_omission,omitempty"`
}

func (f *TargetFailure) SetDetail(raw string) {
	f.Detail, f.Omission = diagnostictext.Safe(raw, 384)
	if raw == "" {
		f.Omission = "not_provided"
	}
	if f.Detail != raw && f.Omission == "" {
		f.Omission = "redacted"
	}
}
func (f TargetFailure) Valid() bool {
	for _, s := range []string{f.NativeOperation, f.Operation, f.AttemptID, f.StepID} {
		if value, why := diagnosticID(s); s == "" || value != s || why != "" {
			return false
		}
	}
	for _, s := range []string{f.TurnID, f.Code} {
		if s != "" {
			if value, why := diagnosticID(s); value != s || why != "" {
				return false
			}
		}
	}
	if slices.Contains([]string{"preflight_failure", "communication_failure", "native_response_invalid", "unknown_boundary_failure"}, f.Category) {
		return (f.HTTPStatus == 0 && f.ResponseDigest == "" || f.Category == "native_response_invalid" && f.HTTPStatus >= 200 && f.HTTPStatus <= 599 && validDigest(f.ResponseDigest)) && f.Detail == "" && f.Code == "" && f.Omission == "not_provided"
	}
	text, why := diagnostictext.Safe(f.Detail, 384)
	return text == f.Detail && why == "" && validDigest(f.ResponseDigest) && f.HTTPStatus >= 200 && f.HTTPStatus <= 599 && slices.Contains([]string{"target_turn_timeout", "target_turn_failed", "native_rejection"}, f.Category) && slices.Contains([]string{"", "not_provided", "redacted", "credential_redacted", "unsafe_control", "truncated"}, f.Omission)
}
func (f TargetFailure) Message() string {
	text := fmt.Sprintf("Interceptor reported a target application turn failure for %s during %s; a target-model failure is not established.", f.Operation, f.NativeOperation)
	switch f.Category {
	case "communication_failure":
		return fmt.Sprintf("Operator lost communication with Interceptor during %s for target operation %s; that native operation's outcome is unknown. Execution is closed and will not be replayed.", f.NativeOperation, f.Operation)
	case "preflight_failure":
		return fmt.Sprintf("Operator could not complete Interceptor preflight for %s (target operation %s). This native step was not dispatched; failure origin unknown.", f.NativeOperation, f.Operation)
	case "native_response_invalid":
		return fmt.Sprintf("Operator rejected Interceptor's response during %s for target operation %s; the response could not establish the target outcome. Execution is closed and will not be replayed.", f.NativeOperation, f.Operation)
	case "unknown_boundary_failure":
		return fmt.Sprintf("Operator could not confirm Interceptor's response during %s for target operation %s; failure origin unknown. Execution is closed and will not be replayed.", f.NativeOperation, f.Operation)
	}
	if f.Category == "native_rejection" {
		text = fmt.Sprintf("Interceptor rejected %s for target operation %s (HTTP %d).", f.NativeOperation, f.Operation, f.HTTPStatus)
	}
	if f.Category == "target_turn_timeout" {
		text = fmt.Sprintf("The target application turn exceeded its deadline while running %s.", f.Operation)
	}
	if f.Detail != "" {
		text += fmt.Sprintf(" Interceptor reported %q.", f.Detail)
	} else {
		text += " Native error description unavailable."
	}
	if f.Code != "" {
		text += " Native code: " + f.Code + "."
	}
	if f.Omission != "" {
		text += " Description omission: " + f.Omission + "."
	}
	return text + " Campaign execution is closed; the operation will not be replayed."
}
