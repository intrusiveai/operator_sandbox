//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

func runFixture(t *testing.T) (hostconfig.Paths, []string) {
	t.Helper()
	dir, pin := installedContract(t)
	paths := configPaths(t)
	if err := os.Mkdir(paths.StateRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, paths.ConfigFile, fmt.Sprintf("engine: {image: test}\ncontract: {directory: %q, version: %q, digest: %q}\n", dir, pin.Version, pin.Digest))
	env := t.TempDir()
	if err := os.WriteFile(filepath.Join(env, "target-profile.json"), []byte(adminTarget), 0600); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"public-capabilities.json", "capabilities.json"}, {"interceptor-export.json", ""}, {"submitted-bundle.json", "bundle.json"}} {
		raw, err := os.ReadFile("../../schemas/fixtures/capability-chain/" + pair[0])
		if err != nil {
			t.Fatal(err)
		}
		name := pair[1]
		if name == "" {
			name = "sha256-" + contracts.RawDigest(raw)[7:] + ".json"
		}
		if err := os.WriteFile(filepath.Join(env, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return paths, []string{"--bundle", filepath.Join(env, "bundle.json"), "--environment", env, "--output", filepath.Join(env, "run")}
}
func TestCombinedRunReusesStartAndPreservesUncertainReceipt(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			paths, args := runFixture(t)
			args = append(args, "--service")
			calls := 0
			deps := startDependencies{freeze: func(_ context.Context, file string, p hostconfig.Paths, s hostrun.Selection) (startrequest.Request, error) {
				return startrequest.Request{APIVersion: startrequest.Version, ConfigurationFile: file, StateRoot: p.StateRoot, DockerEndpoint: p.DockerEndpoint, InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: s}, nil
			}, submit: func(ctx context.Context, root, id, digest string) error {
				calls++
				if uncertain {
					return errors.New("lost manager reply")
				}
				owner, err := startrequest.ClaimOnce(ctx, root, id, digest)
				if err != nil {
					return err
				}
				defer owner.Close()
				saved, err := startrequest.Read(root, id)
				if err != nil {
					return err
				}
				err = owner.Accept(ctx, hostrun.Receipt{APIVersion: "operator.dev/campaign-start/v1alpha1", CampaignID: saved.Request.Selection.CampaignID, LaunchID: saved.Request.Selection.LaunchID, StartRequestID: id, ManifestDigest: contracts.RawDigest([]byte("manifest")), Status: "accepted"})
				if err != nil {
					return err
				}
				m := savedManifest(t)
				m.CampaignID = saved.Request.Selection.CampaignID
				m.LaunchID = saved.Request.Selection.LaunchID
				w, e := campaign.Create(root, m)
				if e != nil {
					return e
				}
				_, e = w.Append(campaign.Entry{RunRevision: m.InitialRevision, Kind: "launch.terminal", Metadata: json.RawMessage(`{}`)})
				e = errors.Join(e, w.Close())
				if e != nil {
					return e
				}
				return owner.Finish(ctx, "finished", "completed")
			}}
			start := func(ctx context.Context, action string, args []string, out, diagnostic io.Writer, p hostconfig.Paths) int {
				return startCampaignWith(ctx, action, args, out, diagnostic, p, deps)
			}
			var out, diagnostic bytes.Buffer
			code := combinedRunWith(context.Background(), append(args, "--wait"), &out, &diagnostic, paths, start)
			var receipt runReceipt
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatal(err, out.String())
			}
			want := 0
			if uncertain {
				want = 1
			}
			if code != want || receipt.Start == nil || receipt.Start.StartRequestID == "" || receipt.Submission == nil || calls != 1 {
				t.Fatal(code, receipt, diagnostic.String())
			}
			if uncertain {
				// Returning failure must not withdraw the durable request.
				owner, err := startrequest.ClaimOnce(context.Background(), paths.StateRoot, receipt.Start.StartRequestID, receipt.Start.RequestDigest)
				if err != nil {
					t.Fatal(err)
				}
				owner.Close()
			} else {
				if receipt.Observation == nil || receipt.Status != "execution_closed" {
					t.Fatal(receipt)
				}
				out.Reset()
				diagnostic.Reset()
				if code := combinedRunWith(context.Background(), args, &out, &diagnostic, paths, start); code != 0 || calls != 1 {
					t.Fatal(code, calls, diagnostic.String())
				}
				if err := os.WriteFile(args[1], []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				out.Reset()
				if code := combinedRunWith(context.Background(), args, &out, &diagnostic, paths, start); code != 1 || calls != 1 {
					t.Fatal("changed submission executed", code, calls)
				}
			}
		})
	}
}

func TestDoctorRedactsPrivateConfigurationAndDoesNotCreateState(t *testing.T) {
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, "engine: {image: test}\ncredentials: {file: /private/secret-locator/config}\ndocker: {executable: /private/secret-locator/docker}\n")
	var out, diagnostic bytes.Buffer
	code := runWithDefaults(context.Background(), []string{"doctor", "--offline"}, &out, &diagnostic, paths)
	if code != 1 || bytes.Contains(out.Bytes(), []byte("secret-locator")) || diagnostic.Len() != 0 {
		t.Fatal(code, out.String(), diagnostic.String())
	}
	var result struct{ Checks []diagnosticCheck }
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Checks) < 6 {
		t.Fatal(out.String())
	}
	for _, check := range result.Checks {
		if check.Name == "target_profile" && (check.Status != "not_checked" || check.Action != "select_explicit_submission_target") {
			t.Fatal("explicit submission profile wrongly blocked", check)
		}
	}
	if _, err := os.Stat(paths.StateRoot); !os.IsNotExist(err) {
		t.Fatal("doctor created state", err)
	}
}

func TestInspectUsesObserverAndDoesNotResume(t *testing.T) {
	root, writer := observerFixture(t)
	defer writer.Close()
	var out, diagnostic bytes.Buffer
	if code := runWithDefaults(context.Background(), []string{"inspect", "--campaign", writer.Manifest().CampaignID, "--state-root", root}, &out, &diagnostic, hostconfig.Paths{}); code != 0 {
		t.Fatal(code, diagnostic.String())
	}
	var receipt observationReceipt
	if json.Unmarshal(out.Bytes(), &receipt) != nil || !receipt.Verified || receipt.ExecutionState != "unknown" {
		t.Fatal(out.String())
	}
}

func TestCombinedRunRejectsIncompleteOutputBeforeStart(t *testing.T) {
	paths, args := runFixture(t)
	if err := os.Mkdir(args[5], 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	start := func(context.Context, string, []string, io.Writer, io.Writer, hostconfig.Paths) int { calls++; return 0 }
	var out, diagnostic bytes.Buffer
	if code := combinedRunWith(context.Background(), args, &out, &diagnostic, paths, start); code != 1 || calls != 0 {
		t.Fatal(code, calls)
	}
	var receipt runReceipt
	if json.Unmarshal(out.Bytes(), &receipt) != nil || receipt.Status != "submission_failed" || receipt.Start != nil {
		t.Fatal(out.String())
	}
}
