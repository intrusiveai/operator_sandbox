//go:build linux || darwin

// Package workerjob executes a single durable host start request. Process/service
// supervision is outside this package; a prior worker claim never resumes here.
package workerjob

import (
	"context"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

const PreparationTimeout = 2 * time.Minute

var ErrInputsChanged = errors.New("queued campaign inputs changed before preparation")

type Result struct {
	APIVersion          string                         `json:"api_version"`
	StartRequestID      string                         `json:"start_request_id"`
	CampaignID          string                         `json:"campaign_id"`
	Phase               string                         `json:"phase"`
	Code                string                         `json:"code"`
	Diagnostic          *hostrun.PreparationDiagnostic `json:"diagnostic,omitempty"`
	CompletionRecording string                         `json:"completion_recording"`
	Accepted            *hostrun.Receipt               `json:"accepted,omitempty"`
	Execution           *hostworker.Result             `json:"-"`
	Reporting           string                         `json:"reporting"`
	Report              *reporting.Receipt             `json:"report,omitempty"`
}
type preparedRun interface {
	Receipt() hostrun.Receipt
	Run(context.Context) (hostworker.Result, error)
	Cancel() error
}
type verifiedInputs interface {
	Fingerprint() string
	Open(context.Context, string) (preparedRun, error)
}
type installedInputs struct{ *hostrun.InstalledInputs }

func (i installedInputs) Open(ctx context.Context, version string) (preparedRun, error) {
	return i.InstalledInputs.Open(ctx, version)
}

type loader func(context.Context, startrequest.Request) (verifiedInputs, error)

func Run(ctx context.Context, stateRoot, requestID, requestDigest, operatorVersion string) (Result, error) {
	result, err := run(ctx, stateRoot, requestID, requestDigest, operatorVersion, func(ctx context.Context, r startrequest.Request) (verifiedInputs, error) {
		inputs, err := hostrun.LoadInputs(ctx, r.ConfigurationFile, r.Defaults(), r.Selection)
		if err != nil {
			return nil, err
		}
		return installedInputs{inputs}, nil
	})
	finishReport(stateRoot, &result, reporting.Generate)
	return result, err
}

// Execution completion is durable before this bounded, independently retryable
// report pass. Reporting failure never changes execution success or cleanup.
func finishReport(root string, result *Result, generate func(context.Context, string, string, string) (reporting.Receipt, error)) {
	result.Reporting = "not_started"
	if result.Execution == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	receipt, err := generate(ctx, root, result.CampaignID, "")
	result.Reporting = "report_failed"
	if err == nil {
		result.Reporting = "generated"
		result.Report = &receipt
	}
}
func run(ctx context.Context, stateRoot, requestID, requestDigest, operatorVersion string, load loader) (result Result, runErr error) {
	result = Result{APIVersion: "operator.dev/worker-job/v1alpha1", StartRequestID: requestID, Phase: "unclaimed", Code: "claim_rejected", CompletionRecording: "not_attempted"}
	owner, err := startrequest.ClaimOnce(ctx, stateRoot, requestID, requestDigest)
	if err != nil {
		return result, err
	}
	defer owner.Close()
	result.Phase, result.Code = "failed", "preparation_failed"
	stage := "saved start request loading"
	defer func() {
		if result.Code == "preparation_failed" {
			runErr = hostrun.PreparationFailure(stage, runErr)
			result.Diagnostic = hostrun.PreparationDetails(runErr)
		}
		// Completion recording is independent of an observer/service cancellation;
		// it cannot grant another execution or delay the independent Docker kill.
		recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := owner.FinishWithDiagnostic(recordCtx, result.Phase, result.Code, result.Diagnostic)
		result.CompletionRecording = "recorded"
		if err != nil {
			result.CompletionRecording = "unconfirmed"
			runErr = errors.Join(runErr, err)
		}
	}()
	snapshot, err := startrequest.Read(stateRoot, requestID)
	if err != nil {
		return result, err
	}
	result.CampaignID = snapshot.Request.Selection.CampaignID
	prepareCtx, cancel := context.WithTimeout(ctx, PreparationTimeout)
	defer cancel()
	stage = "input loading"
	inputs, err := load(prepareCtx, snapshot.Request)
	if err != nil {
		return result, err
	}
	if inputs.Fingerprint() != snapshot.Request.InputsFingerprint {
		result.Code = "inputs_changed"
		return result, ErrInputsChanged
	}
	stage = "campaign preparation"
	prepared, err := inputs.Open(prepareCtx, operatorVersion)
	if err != nil {
		return result, err
	}
	accepted := prepared.Receipt()
	if err := owner.Accept(prepareCtx, accepted); err != nil {
		result.Code = "acceptance_unconfirmed"
		return result, errors.Join(err, prepared.Cancel())
	}
	result.Accepted = &accepted
	cancel()
	// The worker's campaign deadline is already frozen. Preparation's shorter
	// context must not shorten execution or be retained by a native/model client.
	execution, err := prepared.Run(ctx)
	result.Execution = &execution
	if err != nil {
		result.Code = "execution_failed"
		if execution.FailureTrigger != nil && execution.FailureTrigger.Kind == "model" && execution.ModelDiagnostic != nil && execution.ModelDiagnostic.Valid() && execution.ModelDiagnostic.OperationID == execution.FailureTrigger.OperationID {
			d := *execution.ModelDiagnostic
			result.Diagnostic = &hostrun.PreparationDiagnostic{Stage: "Attack Harness model request (" + d.Category + ")", Message: d.Summary(), Model: d, CleanupFailed: execution.ContainerCleanup == "unconfirmed" || execution.TransportCleanup == "retained" || execution.InputCleanup == "retained"}
		} else if execution.FailureTrigger != nil && execution.FailureTrigger.Kind == "workflow" && execution.WorkflowDiagnostic != nil && execution.WorkflowDiagnostic.Valid() && execution.WorkflowDiagnostic.OperationID == execution.FailureTrigger.OperationID {
			d := *execution.WorkflowDiagnostic
			result.Diagnostic = &hostrun.PreparationDiagnostic{Stage: map[string]string{"harness-local": "Harness", "operator": "Operator", "target": "target", "cleanup": "cleanup", "interceptor": "Interceptor", "unknown": "unattributed operation"}[d.Source] + " " + d.Stage, Message: d.Message(), Workflow: d, CleanupFailed: execution.ContainerCleanup == "unconfirmed" || execution.TransportCleanup == "retained" || execution.InputCleanup == "retained"}
		} else if execution.ConclusionDiagnostic != nil && execution.ConclusionDiagnostic.Valid() && !execution.StopAccepted {
			d := *execution.ConclusionDiagnostic
			result.Diagnostic = &hostrun.PreparationDiagnostic{Stage: "assessment not accepted", Message: "No accepted assessment was recorded. A prior rejection is retained separately; it does not establish the terminal cause. " + d.Message(), Conclusion: d, CleanupFailed: execution.ContainerCleanup == "unconfirmed" || execution.TransportCleanup == "retained" || execution.InputCleanup == "retained"}
		}
		if result.Diagnostic == nil && (execution.ModelDiagnostic != nil || execution.WorkflowDiagnostic != nil) {
			result.Diagnostic = &hostrun.PreparationDiagnostic{Stage: "execution", Message: "Operator observed execution failures; the initiating failure could not be established. Inspect the separately retained diagnostics."}
			if execution.ModelDiagnostic != nil && execution.ModelDiagnostic.Valid() {
				result.Diagnostic.Model = *execution.ModelDiagnostic
			}
			if execution.WorkflowDiagnostic != nil && execution.WorkflowDiagnostic.Valid() {
				result.Diagnostic.Workflow = *execution.WorkflowDiagnostic
			}
		}
		if result.Diagnostic == nil {
			result.Diagnostic = hostrun.ExecutionDetails(err)
		}
		if execution.HarnessExit != nil && execution.HarnessExit.Valid() {
			h := *execution.HarnessExit
			if result.Diagnostic == nil {
				result.Diagnostic = &hostrun.PreparationDiagnostic{Stage: "Harness exit", Message: h.Summary()}
			}
			result.Diagnostic.HarnessExit = h
			result.Diagnostic.CleanupFailed = !h.CleanupComplete
		}
		if result.Diagnostic != nil {
			result.Diagnostic.Cleanup = execution.CleanupDiagnostic()
			if execution.FailureTrigger != nil && execution.FailureTrigger.Valid() {
				result.Diagnostic.FailureTrigger = *execution.FailureTrigger
			}
		}
		return result, err
	}
	result.Phase, result.Code = "finished", "execution_finished"
	return result, nil
}
