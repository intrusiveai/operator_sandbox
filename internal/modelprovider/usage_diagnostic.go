//go:build linux || darwin

package modelprovider

import (
	"fmt"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

const usageRejectionMessage = "Operator rejected validated model usage against a local token allowance after the provider returned successfully."

// UsageAllowance contains validated, normalized provider counts and the actual
// local allowances used for that call. It does not settle or refund a reservation.
// The zero value means these facts were not retained (including older records).
type UsageAllowance struct {
	InputTokens               int64 `json:"input_tokens"`
	OutputTokens              int64 `json:"output_tokens"`
	TotalTokens               int64 `json:"total_tokens"`
	MaximumPromptTokens       int64 `json:"maximum_prompt_tokens"`
	RequestedCompletionTokens int64 `json:"requested_completion_tokens"`
	ReservedTokens            int64 `json:"reserved_tokens"`
	PromptAllowanceExceeded   bool  `json:"prompt_allowance_exceeded"`
	TotalReservationExceeded  bool  `json:"total_reservation_exceeded"`
}

func (u UsageAllowance) valid() bool {
	for _, n := range []int64{u.InputTokens, u.OutputTokens, u.TotalTokens, u.MaximumPromptTokens, u.RequestedCompletionTokens, u.ReservedTokens} {
		if n < 0 || n > contracts.MaxSafeInteger {
			return false
		}
	}
	return u.MaximumPromptTokens > 0 && u.RequestedCompletionTokens > 0 && u.ReservedTokens > 0 &&
		u.InputTokens <= contracts.MaxSafeInteger-u.OutputTokens && u.TotalTokens == u.InputTokens+u.OutputTokens &&
		u.PromptAllowanceExceeded == (u.InputTokens > u.MaximumPromptTokens) &&
		u.TotalReservationExceeded == (u.TotalTokens > u.ReservedTokens) &&
		(u.PromptAllowanceExceeded || u.TotalReservationExceeded)
}

// WithUsageRejection must receive metrics from successful contract validation.
// Unknown/malformed metrics cannot be presented as validated zero usage.
func (d Diagnostic) WithUsageRejection(m contracts.ModelMetrics, prompt, completion, reserved int64) Diagnostic {
	d.UsageAllowance = UsageAllowance{}
	if !m.Known {
		return d.WithFailure("response_validation", "usage_unknown")
	}
	u := UsageAllowance{m.InputTokens, m.OutputTokens, m.TotalTokens, prompt, completion, reserved,
		m.InputTokens > prompt, m.TotalTokens > reserved}
	if !u.valid() {
		return d.WithFailure("response_validation", "usage_invalid")
	}
	d.UsageAllowance = u
	return d.WithFailure("response_validation", "usage_exceeds_reservation")
}

func (d Diagnostic) UsageDetailsText() string {
	if d.UsageAllowance == (UsageAllowance{}) || !d.Valid() {
		return ""
	}
	u := d.UsageAllowance
	failed := []string{}
	if u.PromptAllowanceExceeded {
		failed = append(failed, "input tokens exceeded the configured prompt allowance")
	}
	if u.TotalReservationExceeded {
		failed = append(failed, "total tokens exceeded the reserved total")
	}
	return fmt.Sprintf("Local allowance exceeded: %s.\nValidated input tokens: %d; configured prompt allowance: %d.\nValidated output tokens: %d; requested completion allowance: %d.\nValidated total tokens: %d; reserved total: %d.",
		strings.Join(failed, "; "), u.InputTokens, u.MaximumPromptTokens, u.OutputTokens, u.RequestedCompletionTokens, u.TotalTokens, u.ReservedTokens)
}
