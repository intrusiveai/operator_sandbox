//go:build linux || darwin

package workerjob

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

type fakeRun struct {
	receipt       hostrun.Receipt
	runs, cancels int
	runError      error
	check         func()
}

func (f *fakeRun) Receipt() hostrun.Receipt { return f.receipt }
func (f *fakeRun) Run(ctx context.Context) (hostworker.Result, error) {
	f.runs++
	if err := ctx.Err(); err != nil {
		return hostworker.Result{}, err
	}
	if f.check != nil {
		f.check()
	}
	return hostworker.Result{ContainerCleanup: "removed"}, f.runError
}
func (f *fakeRun) Cancel() error { f.cancels++; return nil }

type fakeInputs struct {
	fingerprint string
	run         *fakeRun
	opens       int
	openError   error
}

func (f *fakeInputs) Fingerprint() string { return f.fingerprint }
func (f *fakeInputs) Open(ctx context.Context, version string) (preparedRun, error) {
	f.opens++
	if version != "0.1.0" {
		return nil, errors.New("wrong installed version")
	}
	return f.run, f.openError
}
func jobFixture(t *testing.T) (startrequest.Snapshot, *fakeInputs) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	r := startrequest.Request{APIVersion: startrequest.Version, ConfigurationFile: "/installed/config.yaml", StateRoot: root, DockerEndpoint: "unix:///saved/socket", InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: hostrun.NewSelection("/submitted/run")}
	saved, err := startrequest.Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeInputs{fingerprint: r.InputsFingerprint, run: &fakeRun{receipt: hostrun.Receipt{APIVersion: "operator.dev/campaign-start/v1alpha1", CampaignID: r.Selection.CampaignID, LaunchID: r.Selection.LaunchID, StartRequestID: r.Selection.StartRequestID, ManifestDigest: contracts.RawDigest([]byte("manifest")), Status: "accepted"}}}
	return saved, f
}
func TestWorkerJobClaimsChecksAcceptsAndFinishesOnce(t *testing.T) {
	saved, inputs := jobFixture(t)
	r := saved.Request
	inputs.run.check = func() {
		s, err := startrequest.Read(r.StateRoot, r.Selection.StartRequestID)
		if err != nil || s.Phase() != "accepted" {
			t.Error("execution before acceptance", s, err)
		}
	}
	loads := 0
	load := func(ctx context.Context, got startrequest.Request) (verifiedInputs, error) {
		loads++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > PreparationTimeout || got.InputsFingerprint != r.InputsFingerprint {
			t.Fatal("wrong preparation bounds")
		}
		return inputs, nil
	}
	result, err := run(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest, "0.1.0", load)
	if err != nil || result.Phase != "finished" || result.CompletionRecording != "recorded" || result.Accepted == nil || inputs.run.runs != 1 || inputs.run.cancels != 0 {
		t.Fatal(result, err, inputs.run)
	}
	if _, err := run(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest, "0.1.0", load); !errors.Is(err, startrequest.ErrClaimed) {
		t.Fatal("job executed twice", err)
	}
	if loads != 1 || inputs.opens != 1 || inputs.run.runs != 1 {
		t.Fatal("replayed work")
	}
	snapshot, err := startrequest.Read(r.StateRoot, r.Selection.StartRequestID)
	if err != nil || snapshot.Phase() != "finished" {
		t.Fatal(snapshot, err)
	}
}
func TestWorkerJobFailureDoesNotRetryOrExecuteChangedInputs(t *testing.T) {
	for _, mode := range []string{"load failed", "changed inputs", "open failed", "invalid receipt", "execution failed"} {
		t.Run(mode, func(t *testing.T) {
			saved, inputs := jobFixture(t)
			r := saved.Request
			switch mode {
			case "changed inputs":
				inputs.fingerprint = contracts.RawDigest([]byte("changed"))
			case "open failed":
				inputs.openError = errors.New("offline dependency failed")
			case "invalid receipt":
				inputs.run.receipt.CampaignID = "different"
			case "execution failed":
				inputs.run.runError = errors.New("private provider error never recorded")
			}
			load := func(context.Context, startrequest.Request) (verifiedInputs, error) {
				if mode == "load failed" {
					return nil, errors.New("private path never recorded")
				}
				return inputs, nil
			}
			result, err := run(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest, "0.1.0", load)
			if err == nil || result.Phase != "failed" || result.CompletionRecording != "recorded" {
				t.Fatal(result, err)
			}
			if inputs.run.runs != map[bool]int{true: 1, false: 0}[mode == "execution failed"] || inputs.run.cancels != map[bool]int{true: 1, false: 0}[mode == "invalid receipt"] {
				t.Fatal(inputs.run)
			}
			if mode == "changed inputs" && inputs.opens != 0 {
				t.Fatal("online preparation on changed inputs")
			}
			snapshot, err := startrequest.Read(r.StateRoot, r.Selection.StartRequestID)
			if err != nil || snapshot.Phase() != "failed" {
				t.Fatal(snapshot, err)
			}
			if _, err := run(context.Background(), r.StateRoot, r.Selection.StartRequestID, saved.Digest, "0.1.0", load); !errors.Is(err, startrequest.ErrClaimed) {
				t.Fatal("failed job reopened", err)
			}
		})
	}
}
