//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/purge"
)

func TestPurgeCommandSelectorsAndAbsentCampaign(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	for _, args := range [][]string{{"purge"}, {"purge", "--all", "--campaign", "id"}, {"purge", "--campaign", "../outside"}, {"purge", "--all", "--state-root", "relative"}, {"purge", "--all", "extra"}, {"purge", "--campaign", ""}} {
		var out, err bytes.Buffer
		if code := runWithDefaults(context.Background(), args, &out, &err, hostconfig.Paths{StateRoot: root}); code != 2 {
			t.Fatal(args, code, err.String())
		}
	}
	var out, err bytes.Buffer
	code := runWithDefaults(context.Background(), []string{"purge", "--campaign", "absent", "--state-root", root}, &out, &err, hostconfig.Paths{})
	if code != 0 || !strings.Contains(out.String(), `"status":"already_absent"`) || !strings.Contains(out.String(), purge.Consequence) {
		t.Fatal(code, out.String(), err.String())
	}
}
func TestPurgeCommandReportsPartialFailureAndConfigurationSelection(t *testing.T) {
	root := t.TempDir()
	var out, diagnostic bytes.Buffer
	run := func(_ context.Context, path string, selection purge.Selection, bin string) (purge.Result, error) {
		if path != root || !selection.All || bin != "/fixed/docker" {
			t.Fatal(path, selection, bin)
		}
		return purge.Result{APIVersion: "operator.dev/purge-result/v1alpha1", Status: "partial", Code: "deletion_incomplete", Consequence: purge.Consequence}, errors.New("private host detail")
	}
	code := purgeCampaignWith(context.Background(), []string{"--all", "--state-root", root, "--docker-bin", "/fixed/docker"}, &out, &diagnostic, hostconfig.Paths{}, run)
	if code != 1 || !strings.Contains(out.String(), `"status":"partial"`) || strings.Contains(diagnostic.String(), "private host detail") {
		t.Fatal(code, out.String(), diagnostic.String())
	}
}
