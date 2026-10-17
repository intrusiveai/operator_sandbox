//go:build linux || darwin

package hostrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/startup"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

// PreparationDiagnostic contains only host-selected stage names and allowlisted
// descriptions. Never populate it from an error string, URL or response body.
// It belongs to the host start record, not the shared host/harness contract.
type PreparationDiagnostic struct {
	Stage         string `json:"stage"`
	Message       string `json:"message"`
	CleanupFailed bool   `json:"cleanup_failed,omitempty"`
}

func (d *PreparationDiagnostic) Valid() bool {
	if d == nil {
		return true
	}
	return len(d.Stage) > 0 && len(d.Stage) <= 96 && len(d.Message) > 0 && len(d.Message) <= 512 &&
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
	return &preparationError{PreparationDiagnostic{Stage: stage, Message: preparationMessage(err)}, err}
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
	// Prefer the originating error when a lower-level operation has joined errors.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				return preparationMessage(cause)
			}
		}
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
	var remote *interceptor.RemoteError
	if errors.As(err, &remote) && remote.Response.Status >= 100 && remote.Response.Status <= 599 {
		return fmt.Sprintf("Interceptor returned HTTP %d; the request was not accepted as successful", remote.Response.Status)
	}
	var call *interceptor.CallError
	if errors.As(err, &call) {
		message := "Interceptor request failed"
		switch call.Kind {
		case "invalid_request":
			message = "Interceptor request failed local validation"
		case "canceled_before_dispatch":
			message = "Interceptor request canceled before dispatch"
		case "transport_unavailable":
			message = "Interceptor transport failed; no valid response was received from the local API"
		case "invalid_response":
			message = "Interceptor returned a response that failed contract validation"
		}
		if call.Uncertain {
			message += "; the target-side outcome is uncertain"
		}
		return message
	}
	// These sentinels have fixed, host-authored descriptions. Do not return err.Error():
	// it may wrap one of these with private paths, credentials or server content.
	for _, safe := range []error{
		imagerelease.ErrRecord, imagerelease.ErrCompatibility, imagerelease.ErrEmbedded,
		dockercontrol.ErrCLIUnavailable, dockercontrol.ErrExecutablePath, dockercontrol.ErrExecutableUnavailable,
		dockercontrol.ErrImage, dockercontrol.ErrImageIdentity, dockercontrol.ErrPlatform, dockercontrol.ErrImageSelector, dockercontrol.ErrReleaseFiles,
		hostconfig.ErrInvalid, hostconfig.ErrPrivate, hostconfig.ErrImage, hostconfig.ErrPath, hostconfig.ErrSpool, hostconfig.ErrEvidence,
		contractstore.ErrPath, contractstore.ErrInstallation, contractstore.ErrManifest, contractstore.ErrPin, contractstore.ErrIntegrity,
		submission.ErrSubmission, targetprofile.ErrProfile, modelprovider.ErrProfile, modelprovider.ErrProvider,
		skills.ErrSkill, staging.ErrInputs, staging.ErrFilesystem, startup.ErrUnresolved, preparation.ErrPreparation,
		campaign.ErrInvalid, campaign.ErrCorrupt, campaign.ErrClosed, campaign.ErrActive, campaign.ErrQuota, campaign.ErrStorage, ErrSession,
	} {
		if errors.Is(err, safe) {
			return safe.Error()
		}
	}
	return "operation failed; no additional safe error detail is available"
}
