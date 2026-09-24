//go:build linux || darwin

package termination

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/dockercontrol"
)

type killFunc func(context.Context, campaign.DockerBinding) dockercontrol.Outcome

func (f killFunc) Terminate(ctx context.Context, b campaign.DockerBinding) dockercontrol.Outcome {
	return f(ctx, b)
}

func testBinding() campaign.DockerBinding {
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64),
		RunManifestDigest: contracts.RawDigest([]byte("manifest")), Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1",
		DockerContainerID: strings.Repeat("b", 64), ImageDigest: contracts.RawDigest([]byte("image"))}
	b.Labels = (campaign.RunManifest{CampaignID: b.CampaignID, LaunchID: b.LaunchID, ContainerID: b.ContainerID}).DockerLabels()
	return b
}

func service(t *testing.T, kill killFunc) *Service {
	t.Helper()
	s := New(kill)
	s.readBinding = func(string, string) (campaign.DockerBinding, error) { return testBinding(), nil }
	s.saveRecord = func(string, campaign.DockerBinding, campaign.StopRecord) error { return nil }
	return s
}

func stopped() dockercontrol.Outcome {
	return dockercontrol.Outcome{Confirmed: true, KillAttempted: true, State: "exited", Code: "confirmed_stopped"}
}

func TestRecordingCannotDelayDocker(t *testing.T) {
	for _, mode := range []string{"blocked", "failed", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			killed := make(chan struct{})
			s := service(t, func(ctx context.Context, b campaign.DockerBinding) dockercontrol.Outcome {
				close(killed)
				return stopped()
			})
			release := make(chan struct{})
			defer close(release)
			s.saveRecord = func(string, campaign.DockerBinding, campaign.StopRecord) error {
				if mode == "blocked" {
					<-release
				}
				if mode == "failed" {
					return errors.New("disk full")
				}
				return nil
			}
			done := make(chan Receipt, 1)
			go func() {
				done <- s.Terminate(context.Background(), "/unused", "campaign-1", NewRequestID(), "user-request")
			}()
			select {
			case <-killed:
			case <-time.After(time.Second):
				t.Fatal("kill waited for recording")
			}
			select {
			case r := <-done:
				if !r.Outcome.Confirmed {
					t.Fatal(r)
				}
				want := map[string]string{"blocked": "unconfirmed", "failed": "failed", "healthy": "recorded"}[mode]
				if r.IntentRecording != want || r.ResultRecording != want || r.Successful() != (mode == "healthy") {
					t.Fatal(r)
				}
			case <-time.After(time.Second):
				t.Fatal("recording blocked receipt")
			}
		})
	}
}

func TestBindingFailureNeverSelectsAnotherContainer(t *testing.T) {
	var calls atomic.Int32
	s := service(t, func(context.Context, campaign.DockerBinding) dockercontrol.Outcome { calls.Add(1); return stopped() })
	s.readBinding = func(string, string) (campaign.DockerBinding, error) {
		return campaign.DockerBinding{}, errors.New("damaged identity")
	}
	r := s.Terminate(context.Background(), "/unused", "campaign-1", NewRequestID(), "user-request")
	if r.Outcome.Confirmed || calls.Load() != 0 || r.IntentRecording != "not_attempted" {
		t.Fatal(r)
	}
	release := make(chan struct{})
	defer close(release)
	s.readBinding = func(string, string) (campaign.DockerBinding, error) { <-release; return testBinding(), nil }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r = s.Terminate(ctx, "/unused", "campaign-1", NewRequestID(), "user-request")
	if r.Outcome.Confirmed || calls.Load() != 0 || r.Outcome.Code != "deadline_or_cancellation" {
		t.Fatal(r)
	}
}

func TestWatcherUsesFrozenBindingAndFreshStopContext(t *testing.T) {
	for _, viaContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "fence", true: "service cancellation"}[viaContext], func(t *testing.T) {
			var calls atomic.Int32
			s := service(t, func(ctx context.Context, b campaign.DockerBinding) dockercontrol.Outcome {
				if ctx.Err() != nil || b.DockerContainerID != testBinding().DockerContainerID {
					t.Error("lost independent binding/context")
				}
				calls.Add(1)
				return stopped()
			})
			s.readBinding = func(string, string) (campaign.DockerBinding, error) {
				t.Error("live stop reread disk")
				return campaign.DockerBinding{}, errors.New("unavailable")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fence := campaign.NewFence()
			done := make(chan Receipt, 1)
			go func() { done <- s.Watch(ctx, fence, "/unused", testBinding()) }()
			if viaContext {
				cancel()
			} else {
				fence.Stop(errors.New("journal failure"))
			}
			select {
			case r := <-done:
				if !r.Successful() || calls.Load() != 1 || fence.Err() == nil {
					t.Fatal(r, calls.Load())
				}
			case <-time.After(time.Second):
				t.Fatal("watch did not stop")
			}
		})
	}
}

func TestResultCannotClaimPersistenceSuccessBeforeWrites(t *testing.T) {
	s := service(t, func(context.Context, campaign.DockerBinding) dockercontrol.Outcome { return stopped() })
	var writes atomic.Int32
	s.saveRecord = func(_ string, b campaign.DockerBinding, r campaign.StopRecord) error {
		if writes.Add(1) == 1 && r.Outcome != nil {
			t.Error("result preceded intent")
		}
		if r.Outcome != nil {
			return errors.New("result sync failed")
		}
		return nil
	}
	r := s.Terminate(context.Background(), "/unused", "campaign-1", NewRequestID(), "user-request")
	if !r.Outcome.Confirmed || r.IntentRecording != "recorded" || r.ResultRecording != "failed" || r.Successful() {
		t.Fatal(r)
	}
}
