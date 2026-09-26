//go:build linux || darwin

// Package termination coordinates independent Docker stop and best-effort emergency
// recording. Journal or transport locks are never on the path to Docker kill.
package termination

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

const recordingWait = 250 * time.Millisecond

type Killer interface {
	Terminate(context.Context, campaign.DockerBinding) dockercontrol.Outcome
}

type Service struct {
	docker      Killer
	readBinding func(string, string) (campaign.DockerBinding, error)
	saveRecord  func(string, campaign.DockerBinding, campaign.StopRecord) error
}

func New(docker Killer) *Service {
	return &Service{docker, campaign.ReadDockerBinding, campaign.SaveStopRecord}
}

func NewRequestID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

type Receipt struct {
	APIVersion        string                `json:"api_version"`
	RequestID         string                `json:"request_id"`
	CampaignID        string                `json:"campaign_id"`
	LaunchID          string                `json:"launch_id"`
	DockerContainerID string                `json:"docker_container_id"`
	Outcome           dockercontrol.Outcome `json:"outcome"`
	IntentRecording   string                `json:"intent_recording"`
	ResultRecording   string                `json:"result_recording"`
}

func (r Receipt) Successful() bool {
	return r.Outcome.Confirmed && r.IntentRecording == "recorded" && r.ResultRecording == "recorded"
}

// Terminate bounds identity reads and Docker confirmation by a single deadline.
// Filesystem work cannot be canceled portably; buffered result channels let a late
// recording finish without retaining a caller or delaying the kill. Unknown writes
// stay explicitly unconfirmed. The CLI reports both Docker and persistence outcomes.
func (s *Service) Terminate(ctx context.Context, stateRoot, campaignID, requestID, reason string) Receipt {
	r := Receipt{APIVersion: "operator.dev/termination-receipt/v1alpha1", RequestID: requestID, CampaignID: campaignID,
		Outcome: dockercontrol.Outcome{State: "unknown", Code: "invalid_binding"}, IntentRecording: "not_attempted", ResultRecording: "not_attempted"}
	if s == nil || s.docker == nil || !campaign.ValidStopRequest(requestID, reason) {
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, dockercontrol.ConfirmationTimeout)
	defer cancel()
	type bindingResult struct {
		b   campaign.DockerBinding
		err error
	}
	read := make(chan bindingResult, 1)
	go func() { b, err := s.readBinding(stateRoot, campaignID); read <- bindingResult{b, err} }()
	var b campaign.DockerBinding
	select {
	case <-ctx.Done():
		r.Outcome.Code = "deadline_or_cancellation"
		return r
	case got := <-read:
		if got.err != nil || got.b.Validate() != nil || got.b.CampaignID != campaignID {
			return r
		}
		b = got.b
	}
	r.LaunchID, r.DockerContainerID = b.LaunchID, b.DockerContainerID
	intent := make(chan error, 1)
	result := make(chan error, 1)
	// The two records serialize with each other, not with Docker or the worker.
	// A stuck intent does not claim a result was recorded.
	outcomes := make(chan dockercontrol.Outcome, 1)
	go func() {
		intent <- s.saveRecord(stateRoot, b, campaign.StopObservation(b, requestID, reason, nil))
		outcome := <-outcomes
		result <- s.saveRecord(stateRoot, b, campaign.StopObservation(b, requestID, reason, &outcome))
	}()
	r.Outcome = s.docker.Terminate(ctx, b)
	outcomes <- r.Outcome
	r.IntentRecording, r.ResultRecording = "unconfirmed", "unconfirmed"
	wait := time.NewTimer(recordingWait)
	defer wait.Stop()
	for intent != nil || result != nil {
		select {
		case err := <-intent:
			r.IntentRecording = recordingStatus(err)
			intent = nil
		case err := <-result:
			r.ResultRecording = recordingStatus(err)
			result = nil
		case <-wait.C:
			return r
		}
	}
	return r
}

func recordingStatus(err error) string {
	if err == nil {
		return "recorded"
	}
	return "failed"
}

// Watch is armed by the future launcher before harness admission. It reacts once
// to the shared terminal fence or service cancellation. Docker gets a fresh bounded
// context even when the worker's context has already been canceled. It never joins
// the worker, transport, journal writer or a graceful guest handshake first.
func (s *Service) Watch(ctx context.Context, fence *campaign.Fence, stateRoot string, binding campaign.DockerBinding) Receipt {
	if s == nil || s.docker == nil || fence == nil || binding.Validate() != nil {
		return Receipt{Outcome: dockercontrol.Outcome{State: "unknown", Code: "invalid_binding"}}
	}
	// The launcher supplies the already persisted/verified binding. Freeze labels
	// before waiting, so a later filesystem stall cannot obstruct the live kill.
	b := binding
	b.Labels = make(map[string]string, len(binding.Labels))
	for k, v := range binding.Labels {
		b.Labels[k] = v
	}
	select {
	case <-ctx.Done():
		fence.Stop(errors.New("host service interrupted"))
	case <-fence.Done():
	}
	live := *s
	live.readBinding = func(string, string) (campaign.DockerBinding, error) { return b, b.Validate() }
	return live.Terminate(context.Background(), stateRoot, b.CampaignID, NewRequestID(), "host-terminal-fence")
}
