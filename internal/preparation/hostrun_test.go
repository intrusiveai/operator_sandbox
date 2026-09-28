package preparation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostlifetime"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	startupgate "github.com/intrusiveai/operator_sandbox/internal/startup"
)

type composedDocker struct{ workerDocker }

func (d *composedDocker) ResolveCreate(context.Context, campaign.CreateIntent) (campaign.DockerBinding, error) {
	return campaign.DockerBinding{}, dockercontrol.ErrLaunch
}

func (d *composedDocker) CheckInactive(context.Context, campaign.DockerBinding) dockercontrol.Inactivity {
	return dockercontrol.Inactivity{Confirmed: true, State: "absent"}
}
func (d *composedDocker) Create(ctx context.Context, _ *dockercontrol.LaunchPlan) (campaign.DockerBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.creates++
	observed, err := campaign.Observe(ctx, d.root, "campaign-1", nil)
	if err != nil {
		return campaign.DockerBinding{}, err
	}
	m := observed.Manifest
	d.binding = campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: observed.ManifestDigest, Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	return d.binding, nil
}
func composedConfig(t *testing.T) (hostrun.Config, *composedDocker) {
	t.Helper()
	if goruntime.GOOS != "darwin" {
		t.Skip("composed guest fixture uses macOS spool; Linux FIFO coverage is separate")
	}
	in := fixture(t)
	target, err := preparation.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	launch := launchConfig(t, target, "openai-chat-text-tools-v1")
	root := t.TempDir()
	os.Chmod(root, 0700)
	d := &composedDocker{workerDocker: workerDocker{root: root, spool: filepath.Join(root, "campaigns/campaign-1/runtime/transport"), ready: make(chan struct{}), stop: make(chan struct{}), guestDone: make(chan error, 1)}}
	gate, recovery, err := startupgate.Acquire(context.Background(), root, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gate.Close() })
	pin, _ := in.Protocol.PackageIdentity()
	power := &workerPower{}
	c := hostrun.Config{StateRoot: root, Target: target, Launch: launch, Requirements: imagerelease.Requirements{OperatorVersion: "0.1.0", Contract: pin, HostPlatform: goruntime.GOOS + "/" + goruntime.GOARCH, RuntimeProfile: "operator-container/v1"}, Peer: &peer{input: in, revision: 5}, Docker: d, Provider: &provider{}, StartRequestID: strings.Repeat("a", 32), Gate: gate, Recovery: recovery, Lifetime: hostlifetime.NewWithBackend(power, func(error) {})}
	return c, d
}
func TestComposedSessionRetainsInputsBeforeRun(t *testing.T) {
	c, d := composedConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := hostrun.Prepare(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Cancel() })
	if d.creates != 0 || s.Receipt().Status != "accepted" {
		t.Fatal("created before Run")
	}
	kinds := map[string]int{}
	if _, err := campaign.Observe(ctx, c.StateRoot, "campaign-1", func(e campaign.Event) error { kinds[e.Kind]++; return nil }); err != nil {
		t.Fatal(err)
	}
	if kinds["campaign.launch-inputs-retained"] != 1 || kinds["campaign.preparation-adopted"] != 1 || kinds["campaign.start-accepted"] != 1 {
		t.Fatal(kinds)
	}
	if _, err := campaign.AcquireHostLease(c.StateRoot); !errors.Is(err, campaign.ErrActive) {
		t.Fatal("lost worker lease", err)
	}
	done := make(chan struct{})
	var result hostworker.Result
	var runErr error
	go func() { result, runErr = s.Run(ctx); close(done) }()
	select {
	case <-d.ready:
		close(d.stop)
	case <-done:
		t.Fatalf("stopped before handshake: result=%+v error=%v", result, runErr)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if runErr == nil || result.ContainerCleanup != "removed" || result.TransportCleanup != "removed" || result.InputCleanup != "removed" || d.creates != 1 || d.starts != 1 {
		t.Fatal(result, runErr)
	}
	if _, err := os.Stat(d.spool); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("spool retained", err)
	}
	if _, err := s.Run(ctx); !errors.Is(err, hostrun.ErrSession) {
		t.Fatal("session reused", err)
	}
	report, err := campaign.Inspect(c.StateRoot, "campaign-1", nil)
	if err != nil || !report.JournalIntact {
		t.Fatal(report, err)
	}
	lease, err := campaign.AcquireHostLease(c.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
}
func TestComposedSessionCancelAndPrelaunchFailureAreTerminal(t *testing.T) {
	for _, mode := range []string{"cancel prepared", "cancel before run", "invalid configuration"} {
		t.Run(mode, func(t *testing.T) {
			c, d := composedConfig(t)
			if mode == "invalid configuration" {
				c.EvidenceMaxBytes = -1
			}
			s, err := hostrun.Prepare(context.Background(), c)
			if mode == "invalid configuration" {
				if err == nil {
					t.Fatal("accepted invalid capacity")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cancel prepared" {
					if err := s.Cancel(); err != nil {
						t.Fatal(err)
					}
				} else {
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					result, err := s.Run(ctx)
					if err == nil || result.ContainerCleanup != "not-created" {
						t.Fatal(result, err)
					}
				}
				terminal := 0
				report, err := campaign.Inspect(c.StateRoot, "campaign-1", func(e campaign.Event) error {
					if e.Kind == "launch.terminal" {
						terminal++
					}
					return nil
				})
				if err != nil || !report.JournalIntact || terminal != 1 {
					t.Fatal("missing terminal record", terminal, err)
				}
				if _, err := os.Stat(d.spool); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("spool retained", err)
				}
			}
			if d.creates != 0 {
				t.Fatal("unexpected Docker create")
			}
			lease, err := campaign.AcquireHostLease(c.StateRoot)
			if err != nil {
				t.Fatal("lease retained", err)
			}
			lease.Close()
		})
	}
}
