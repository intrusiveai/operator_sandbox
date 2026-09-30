//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

func startFixture(t *testing.T) (string, hostconfig.Paths, startDependencies, *int) {
	t.Helper()
	dir, root := t.TempDir(), t.TempDir()
	os.Chmod(dir, 0700)
	os.Chmod(root, 0700)
	paths := hostconfig.Paths{ConfigFile: "/installed/config.yaml", StateRoot: root, DockerEndpoint: "unix:///saved/socket"}
	calls := new(int)
	deps := startDependencies{freeze: func(ctx context.Context, file string, p hostconfig.Paths, s hostrun.Selection) (startrequest.Request, error) {
		return startrequest.Request{APIVersion: startrequest.Version, ConfigurationFile: file, StateRoot: p.StateRoot, DockerEndpoint: p.DockerEndpoint, InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: s}, nil
	}, submit: func(ctx context.Context, root, id, digest string) error {
		*calls++
		link, saved, err := startrequest.ReadLink(dir)
		if err != nil || link.RequestDigest != digest {
			t.Fatal("manager contacted before durable link", err)
		}
		owner, err := startrequest.ClaimOnce(ctx, root, id, digest)
		if err != nil {
			return err
		}
		defer owner.Close()
		return owner.Accept(ctx, hostrun.Receipt{APIVersion: "operator.dev/campaign-start/v1alpha1", CampaignID: saved.Request.Selection.CampaignID, LaunchID: saved.Request.Selection.LaunchID, StartRequestID: id, ManifestDigest: contracts.RawDigest([]byte("manifest")), Status: "accepted"})
	}}
	return dir, paths, deps, calls
}

func invokeStart(t *testing.T, ctx context.Context, action string, args []string, paths hostconfig.Paths, deps startDependencies) (int, startReceipt, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := startCampaignWith(ctx, action, args, &out, &diagnostic, paths, deps)
	var result startReceipt
	if out.Len() != 0 {
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err, out.String())
		}
	}
	return code, result, diagnostic.String()
}

func TestPrepareStartAndRepeatUseOneDurableIntent(t *testing.T) {
	dir, paths, deps, calls := startFixture(t)
	ctx := context.Background()
	args := []string{"--service", "--run", dir, "--system-prompt-append", "instructions.txt"}
	code, prepared, diag := invokeStart(t, ctx, "prepare", args, paths, deps)
	if code != 0 || prepared.Phase != "submitted" || *calls != 0 {
		t.Fatal(code, prepared, diag)
	}
	code, started, diag := invokeStart(t, ctx, "start", []string{"--service", "--run", dir}, paths, deps)
	if code != 0 || started.Accepted == nil || started.StartRequestID != prepared.StartRequestID || *calls != 1 {
		t.Fatal(code, started, diag)
	}
	code, repeated, diag := invokeStart(t, ctx, "start", []string{"--service", "--run", dir}, paths, deps)
	if code != 0 || repeated.CampaignID != started.CampaignID || *calls != 1 {
		t.Fatal(code, repeated, diag)
	}
	code, _, _ = invokeStart(t, ctx, "start", []string{"--service", "--run", dir, "--system-prompt", "changed.txt"}, paths, deps)
	if code != 1 || *calls != 1 {
		t.Fatal("changed input executed", code, *calls)
	}
	code, next, diag := invokeStart(t, ctx, "prepare", []string{"--service", "--run", dir, "--new-campaign"}, paths, deps)
	if code != 0 || next.CampaignID == started.CampaignID || *calls != 1 {
		t.Fatal(code, next, diag)
	}
	if _, err := startrequest.Read(paths.StateRoot, started.StartRequestID); err != nil {
		t.Fatal("history removed", err)
	}
}

func TestUncertainSubmissionRetainsExactLookupAndCancellationIsObserverOnly(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "uncertain"}[uncertain], func(t *testing.T) {
			dir, paths, deps, _ := startFixture(t)
			deps.submit = func(context.Context, string, string, string) error {
				if uncertain {
					return errors.New("private diagnostic")
				}
				return nil
			}
			code, result, _ := invokeStart(t, context.Background(), "start", []string{"--service", "--run", dir, "--timeout", "1ms"}, paths, deps)
			if code != 1 || result.StartRequestID == "" || result.Accepted != nil {
				t.Fatal(code, result)
			}
			link, saved, err := startrequest.ReadLink(dir)
			if err != nil || link.StartRequestID != result.StartRequestID || saved.Claim != nil {
				t.Fatal(link, saved, err)
			}
			// The real worker can still claim after the submitting CLI has returned.
			owner, err := startrequest.ClaimOnce(context.Background(), paths.StateRoot, result.StartRequestID, result.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
		})
	}
}

func TestRunObserversReportPreJournalFailureAndPinTheSavedRoot(t *testing.T) {
	dir, paths, deps, _ := startFixture(t)
	_, prepared, _ := invokeStart(t, context.Background(), "prepare", []string{"--service", "--run", dir}, paths, deps)
	owner, err := startrequest.ClaimOnce(context.Background(), paths.StateRoot, prepared.StartRequestID, prepared.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.Finish(context.Background(), "failed", "preparation_failed"); err != nil {
		t.Fatal(err)
	}
	wrong := hostconfig.Paths{ConfigFile: "/missing/config", StateRoot: "/wrong/root"}
	for _, action := range []string{"status", "logs", "wait"} {
		var out, diag bytes.Buffer
		code := observeCampaign(context.Background(), action, []string{"--run", dir}, &out, &diag, wrong)
		want := 0
		if action == "wait" {
			want = 1
		}
		var result observationReceipt
		if code != want || json.Unmarshal(out.Bytes(), &result) != nil || result.Start == nil || result.Start.Phase != "failed" || result.Verified || result.ExecutionState != "unknown" {
			t.Fatal(action, code, out.String(), diag.String())
		}
	}
}

func TestMissingReferencedRequestCannotSilentlyStartAgain(t *testing.T) {
	dir, paths, deps, calls := startFixture(t)
	_, prepared, _ := invokeStart(t, context.Background(), "prepare", []string{"--service", "--run", dir}, paths, deps)
	if err := os.Remove(filepath.Join(paths.StateRoot, "starts", prepared.StartRequestID, "request.json")); err != nil {
		t.Fatal(err)
	}
	code, _, _ := invokeStart(t, context.Background(), "start", []string{"--service", "--run", dir}, paths, deps)
	if code != 1 || *calls != 0 {
		t.Fatal("dangling reference became execution", code, *calls)
	}
}

func TestFrozenSkillSetSelectorIsExclusiveAndSaved(t *testing.T) {
	dir, paths, deps, calls := startFixture(t)
	code, _, _ := invokeStart(t, context.Background(), "prepare", []string{"--service", "--run", dir, "--skill-set", "set.json", "--skill", "sha256:" + strings.Repeat("a", 64)}, paths, deps)
	if code != 2 || *calls != 0 {
		t.Fatal(code, *calls)
	}
	code, prepared, diag := invokeStart(t, context.Background(), "prepare", []string{"--service", "--run", dir, "--skill-set", "set.json"}, paths, deps)
	if code != 0 {
		t.Fatal(code, diag)
	}
	saved, err := startrequest.Read(paths.StateRoot, prepared.StartRequestID)
	expected, _ := filepath.Abs("set.json")
	if err != nil || saved.Request.Selection.SkillSetFile != expected || len(saved.Request.Selection.SkillDigests) != 0 {
		t.Fatal(saved, err)
	}
	code, reused, diag := invokeStart(t, context.Background(), "prepare", []string{"--service", "--run", dir}, paths, deps)
	if code != 0 || reused.StartRequestID != prepared.StartRequestID {
		t.Fatal(code, diag)
	}
	code, _, _ = invokeStart(t, context.Background(), "prepare", []string{"--service", "--run", dir, "--skill-set", "different.json"}, paths, deps)
	if code != 1 {
		t.Fatal("changed prepared selection", code)
	}
}

func TestForegroundOwnsWorkerLifetimeAndNeverReplays(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "finished", true: "cancelled"}[cancelRun], func(t *testing.T) {
			dir, paths, deps, serviceCalls := startFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runs := 0
			deps.run = func(ctx context.Context, root, id, digest string) error {
				runs++
				owner, err := startrequest.ClaimOnce(ctx, root, id, digest)
				if err != nil {
					return err
				}
				defer owner.Close()
				if cancelRun {
					cancel()
					<-ctx.Done()
					return errors.Join(ctx.Err(), owner.Finish(context.Background(), "failed", "execution_failed"))
				}
				saved, err := startrequest.Read(root, id)
				if err != nil {
					return err
				}
				if err := owner.Accept(ctx, hostrun.Receipt{APIVersion: "operator.dev/campaign-start/v1alpha1", CampaignID: saved.Request.Selection.CampaignID, LaunchID: saved.Request.Selection.LaunchID, StartRequestID: id, ManifestDigest: contracts.RawDigest([]byte("manifest")), Status: "accepted"}); err != nil {
					return err
				}
				return owner.Finish(ctx, "finished", "execution_closed")
			}
			code, receipt, diag := invokeStart(t, ctx, "start", []string{"--run", dir}, paths, deps)
			want := 0
			if cancelRun {
				want = 1
			}
			if code != want || runs != 1 || *serviceCalls != 0 || receipt.Completion == nil {
				t.Fatal(code, receipt, diag, runs, *serviceCalls)
			}
			code, again, diag := invokeStart(t, context.Background(), "start", []string{"--run", dir}, paths, deps)
			if code != want || runs != 1 || again.StartRequestID != receipt.StartRequestID {
				t.Fatal(code, again, diag, runs)
			}
			code, _, _ = invokeStart(t, context.Background(), "start", []string{"--run", dir, "--service"}, paths, deps)
			if code != 1 || *serviceCalls != 0 {
				t.Fatal("changed execution mode replayed")
			}
		})
	}
}
