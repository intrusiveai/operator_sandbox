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
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

const PreparationTimeout = 2 * time.Minute

var ErrInputsChanged = errors.New("queued campaign inputs changed before preparation")

type Result struct {
	APIVersion          string             `json:"api_version"`
	StartRequestID      string             `json:"start_request_id"`
	CampaignID          string             `json:"campaign_id"`
	Phase               string             `json:"phase"`
	Code                string             `json:"code"`
	CompletionRecording string             `json:"completion_recording"`
	Accepted            *hostrun.Receipt   `json:"accepted,omitempty"`
	Execution           *hostworker.Result `json:"-"`
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
	return run(ctx, stateRoot, requestID, requestDigest, operatorVersion, func(ctx context.Context, r startrequest.Request) (verifiedInputs, error) {
		inputs, err := hostrun.LoadInputs(ctx, r.ConfigurationFile, r.Defaults(), r.Selection)
		if err != nil {
			return nil, err
		}
		return installedInputs{inputs}, nil
	})
}
func run(ctx context.Context, stateRoot, requestID, requestDigest, operatorVersion string, load loader) (result Result, runErr error) {
	result = Result{APIVersion: "operator.dev/worker-job/v1alpha1", StartRequestID: requestID, Phase: "unclaimed", Code: "claim_rejected", CompletionRecording: "not_attempted"}
	owner, err := startrequest.ClaimOnce(ctx, stateRoot, requestID, requestDigest)
	if err != nil {
		return result, err
	}
	defer owner.Close()
	result.Phase, result.Code = "failed", "preparation_failed"
	defer func() {
		// Completion recording is independent of an observer/service cancellation;
		// it cannot grant another execution or delay the independent Docker kill.
		recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := owner.Finish(recordCtx, result.Phase, result.Code)
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
	inputs, err := load(prepareCtx, snapshot.Request)
	if err != nil {
		return result, err
	}
	if inputs.Fingerprint() != snapshot.Request.InputsFingerprint {
		result.Code = "inputs_changed"
		return result, ErrInputsChanged
	}
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
		return result, err
	}
	result.Phase, result.Code = "finished", "execution_finished"
	return result, nil
}
