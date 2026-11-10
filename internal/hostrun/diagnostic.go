//go:build linux || darwin

package hostrun

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
)

// PreparationDiagnostic retains its historical name and also carries optional
// model execution or conclusion rejection details. Stage/Message are host-selected
// summaries; Conclusion contains sanitized host validation facts and Model can
// include bounded provider fields sanitized by modelprovider. Never populate this
// object directly from an unrestricted error string, URL or response body.
// It belongs to the host start record, not the shared host/harness contract.
type PreparationDiagnostic struct {
	FailureTrigger campaign.FailureTrigger        `json:"failure_trigger,omitempty,omitzero"`
	Cleanup        campaign.CleanupDiagnostic     `json:"cleanup,omitempty,omitzero"`
	Interceptor    interceptor.CompatibilityError `json:"interceptor,omitempty,omitzero"`
	HarnessExit    campaign.HarnessExit           `json:"harness_exit,omitempty,omitzero"`
	Workflow       campaign.WorkflowDiagnostic    `json:"workflow,omitempty,omitzero"`
	Conclusion     campaign.ConclusionDiagnostic  `json:"conclusion,omitempty,omitzero"`
	Stage          string                         `json:"stage"`
	Message        string                         `json:"message"`
	CleanupFailed  bool                           `json:"cleanup_failed,omitempty"`
	Model          modelprovider.Diagnostic       `json:"model,omitempty,omitzero"`
}

func (d *PreparationDiagnostic) Valid() bool {
	if d == nil {
		return true
	}
	if d.FailureTrigger != (campaign.FailureTrigger{}) && !d.FailureTrigger.Valid() {
		return false
	}
	if !d.Cleanup.Valid() {
		return false
	}
	if d.Interceptor != (interceptor.CompatibilityError{}) && !d.Interceptor.Valid() {
		return false
	}
	if d.HarnessExit != (campaign.HarnessExit{}) && !d.HarnessExit.Valid() {
		return false
	}
	if d.Conclusion != (campaign.ConclusionDiagnostic{}) && !d.Conclusion.Valid() {
		return false
	}
	if d.Workflow != (campaign.WorkflowDiagnostic{}) && !d.Workflow.Valid() {
		return false
	}
	if d.Model != (modelprovider.Diagnostic{}) && !d.Model.Valid() {
		return false
	}
	messageLimit := 1024
	if d.Workflow != (campaign.WorkflowDiagnostic{}) || d.Conclusion != (campaign.ConclusionDiagnostic{}) {
		messageLimit = 12288
	}
	return len(d.Stage) > 0 && len(d.Stage) <= 96 && len(d.Message) > 0 && len(d.Message) <= messageLimit &&
		strings.IndexFunc(d.Stage+d.Message, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

type preparationError struct {
	diagnostic PreparationDiagnostic
	cause      error
}

func (e *preparationError) Error() string { return e.diagnostic.Stage + ": " + e.diagnostic.Message }
func (e *preparationError) Unwrap() error { return e.cause }

// PreparationFailure preserves the original cause for errors.Is/As while exposing
// only a safe description. Stage must be a literal host operation name.
func PreparationFailure(stage string, err error) error {
	if err == nil {
		return nil
	}
	var prior *preparationError
	if errors.As(err, &prior) {
		return err
	}
	if stage == "local Harness image and compatibility validation" {
		switch {
		case errors.Is(err, imagerelease.ErrRecord):
			stage = "embedded Harness compatibility record validation"
		case errors.Is(err, imagerelease.ErrCompatibility):
			stage = "Harness compatibility validation"
		case errors.Is(err, dockercontrol.ErrImage), errors.Is(err, dockercontrol.ErrImageIdentity), errors.Is(err, dockercontrol.ErrPlatform):
			stage = "local Harness image inspection"
		}
	}
	diagnostic := PreparationDiagnostic{Stage: stage, Message: "Operator preparation (" + stage + "): " + preparationMessage(err)}
	var compatibility *interceptor.CompatibilityError
	if errors.As(err, &compatibility) && compatibility.Valid() {
		diagnostic.Interceptor = *compatibility
		diagnostic.Stage = "Interceptor compatibility check"
	}
	return &preparationError{diagnostic, err}
}

// PreparationDetails returns a copy, never an unrestricted underlying error.
func PreparationDetails(err error) *PreparationDiagnostic {
	var failure *preparationError
	if !errors.As(err, &failure) {
		return nil
	}
	d := failure.diagnostic
	return &d
}

func preparationCleanup(stage string, primary, cleanup error) error {
	if primary == nil {
		return PreparationFailure(stage+" cleanup", cleanup)
	}
	primary = PreparationFailure(stage, primary)
	if cleanup == nil {
		return primary
	}
	d := *PreparationDetails(primary)
	d.CleanupFailed = true
	return &preparationError{d, errors.Join(primary, cleanup)}
}

func preparationMessage(err error) string {
	if detail, ok := diagnostictext.FromError(err); ok {
		return detail
	}
	// A joined cleanup failure must never displace the primary failure, even
	// when only the secondary error has a recognized diagnostic type.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				return preparationMessage(cause)
			}
		}
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return preparationMessage(wrapped.Unwrap())
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "operation canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "operation deadline exceeded"
	case errors.Is(err, os.ErrNotExist):
		return "required file or directory does not exist"
	case errors.Is(err, os.ErrPermission):
		return "permission denied while accessing a required resource"
	case errors.Is(err, syscall.ENOSPC):
		return "insufficient disk space"
	}
	return "operation failed; no additional safe error detail is available"
}

// ExecutionDetails reports known host boundaries without ranking coexisting
// errors as causal proof. It never prints an unrestricted error chain.
func ExecutionDetails(err error) *PreparationDiagnostic {
	if errors.Is(err, campaign.ErrStorage) || errors.Is(err, campaign.ErrQuota) || errors.Is(err, syscall.ENOSPC) {
		return &PreparationDiagnostic{Stage: "Operator campaign evidence retention", Message: "Operator could not retain required campaign evidence: " + preparationMessage(err) + ". Any target effect must be determined from retained execution records; do not replay."}
	}
	if detail, ok := dockercontrol.FailureDetail(err); ok {
		return &PreparationDiagnostic{Stage: "Operator Docker operation", Message: "Operator observed a Docker operation failure: " + detail}
	}
	return nil
}
