//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
)

func TestEvidenceCommandPathsResultsAndSafeDiagnostics(t *testing.T) {
	root := t.TempDir()
	defaults := hostconfig.Paths{ConfigFile: filepath.Join(root, "missing.yaml"), StateRoot: root}
	for _, kind := range []string{"complete", "partial", "failure", "bad arguments", "explicit missing config", "relative root", "empty flag"} {
		t.Run(kind, func(t *testing.T) {
			args := []string{"--campaign", "campaign-1", "--state-root", root, "--docker-bin", "/fixed/docker"}
			switch kind {
			case "bad arguments":
				args = append(args, "--resume")
			case "explicit missing config":
				args = append(args, "--config", defaults.ConfigFile)
			case "relative root":
				args = []string{"--campaign", "campaign-1", "--state-root", "relative"}
			case "empty flag":
				args = append(args, "--docker-bin", "")
			}
			calls := 0
			collect := func(ctx context.Context, gotRoot, id, executable string) (nativerecovery.EvidenceReport, error) {
				calls++
				if gotRoot != root || id != "campaign-1" || executable != "/fixed/docker" {
					t.Fatal(gotRoot, id, executable)
				}
				result := nativerecovery.EvidenceReport{APIVersion: "operator.dev/evidence-collection/v1alpha1", CampaignID: id, ManifestDigest: "sha256:test", Outcomes: []campaign.EvidenceOutcome{{SessionID: "session-1", State: "complete", Recorded: true}}}
				if kind == "partial" {
					result.Outcomes[0].State = "partial"
				}
				if kind == "failure" {
					return result, errors.New("private source /secret/token")
				}
				return result, nil
			}
			var out, diag bytes.Buffer
			code := collectCampaignEvidenceWith(context.Background(), args, &out, &diag, defaults, collect)
			want := 2
			if kind == "complete" {
				want = 0
			} else if kind == "partial" || kind == "failure" {
				want = 1
			}
			if code != want || (calls == 0) != (want == 2) || strings.Contains(diag.String(), "/secret/token") {
				t.Fatal(code, calls, diag.String())
			}
			if calls > 0 && !strings.Contains(out.String(), `"outcomes"`) {
				t.Fatal(out.String())
			}
		})
	}
	var out, diag bytes.Buffer
	if code := runWithDefaults(context.Background(), []string{"campaign", "evidence", "collect", "--unknown"}, &out, &diag, defaults); code != 2 || !strings.Contains(diag.String(), "unknown") {
		t.Fatal(code, diag.String())
	}
}
