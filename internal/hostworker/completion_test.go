//go:build linux || darwin

package hostworker

import (
	"context"
	"errors"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

func TestWorkerStopCauseSurvivesInternalCancellation(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	cancel()
	for _, cause := range []error{dockercontrol.ErrExited, dockercontrol.ErrNotRunning, errors.New("journal failure")} {
		fence := campaign.NewFence()
		fence.Stop(cause)
		if got := stopCause(life, fence); got != cause {
			t.Fatal("original stop cause lost", got, cause)
		}
	}
	if got := stopCause(life, campaign.NewFence()); got != context.Canceled {
		t.Fatal("external cancellation lost", got)
	}
}

func TestNormalStopDoesNotHideCancellationOrUnrelatedFault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if normalStopCause(ctx, true, dockercontrol.ErrExited) || normalStopCause(context.Background(), false, dockercontrol.ErrExited) || normalStopCause(context.Background(), true, errors.New("journal failure")) {
		t.Fatal("failure or cancellation became normal completion")
	}
	if !normalStopCause(context.Background(), true, dockercontrol.ErrExited) {
		t.Fatal("accepted natural exit not recognized")
	}
}

func TestCompletionRequiresAllCleanupAndNaturalExit(t *testing.T) {
	r := Result{HarnessExit: &campaign.HarnessExit{Outcome: "succeeded", ExitObserved: true}, StopAccepted: true, ContainerCleanup: "removed", TransportCleanup: "removed", InputCleanup: "removed",
		Terminal: campaignservice.TerminalResult{Closure: "confirmed", CleanupState: "complete", TargetStop: "confirmed"}}
	r.Termination.Outcome = dockercontrol.Outcome{Confirmed: true, State: "exited"}
	failed := r
	failed.StopAccepted = false
	failed.Termination.Outcome.KillAttempted = true
	if completedResult(failed) || !cleanupCompleted(failed) {
		t.Fatal("cleanup confused with successful campaign", failed)
	}
	if !completedResult(r) {
		t.Fatal("clean completion rejected")
	}
	for _, mutate := range []func(*Result){
		func(r *Result) { r.StopAccepted = false },
		func(r *Result) { r.Termination.Outcome.Confirmed = false },
		func(r *Result) { r.Termination.Outcome.KillAttempted = true },
		func(r *Result) { r.ContainerCleanup = "unconfirmed" },
		func(r *Result) { r.TransportCleanup = "retained" },
		func(r *Result) { r.InputCleanup = "retained" },
		func(r *Result) { r.Terminal.Closure = "unconfirmed" },
		func(r *Result) { r.Terminal.CleanupRemaining = 1 },
		func(r *Result) { r.Terminal.TargetStop = "unconfirmed" },
	} {
		copy := r
		mutate(&copy)
		if completedResult(copy) {
			t.Fatal("incomplete cleanup became successful completion", copy)
		}
	}
}

func TestAcceptedPeerCloseIsNotAnIndependentFailure(t *testing.T) {
	for _, err := range []error{transport.ErrPeerLost, transport.ErrClosed, dockercontrol.ErrExited, dockercontrol.ErrNotRunning} {
		if !normalStopCause(context.Background(), true, err) || normalStopCause(context.Background(), false, err) {
			t.Fatal(err)
		}
	}
	for _, err := range []error{transport.ErrProtocol, transport.ErrDeadline, dockercontrol.ErrEvents, context.DeadlineExceeded, errors.Join(transport.ErrPeerLost, errors.New("independent failure"))} {
		if normalStopCause(context.Background(), true, err) {
			t.Fatal("suppressed independent failure", err)
		}
	}
}
