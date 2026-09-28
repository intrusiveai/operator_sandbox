//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/submission"
)

const adminTarget = `{"api_version":"operator.dev/target-profile/v1alpha1","id":"local","target_id":"delivery-example","adapter":"interceptor/v1","allow_target_stop":false,"scopes":{"operation_ids":["invoke"],"caller_principal_ids":[],"routes":[],"allow_retained_injections":false},"feedback":{"ceiling":"black-box","allowed_kinds":["target_output"],"max_attempt_bytes":1048576},"operation_timeout_ms":30000}`

func TestCapabilityExportAndEnvironmentSubmission(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, fmt.Sprintf("engine: {image: test}\ncontract: {directory: %q, version: %q, digest: %q}\n", dir, pin.Version, pin.Digest))
	env := t.TempDir()
	profile := filepath.Join(env, "target-profile.json")
	if err := os.WriteFile(profile, []byte(adminTarget), 0600); err != nil {
		t.Fatal(err)
	}
	native, _ := filepath.Abs("../../schemas/fixtures/capability-chain/interceptor-export.json")
	bundle, _ := filepath.Abs("../../schemas/fixtures/capability-chain/submitted-bundle.json")
	call := func(want int, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		code := runWithDefaults(context.Background(), args, &out, &stderr, paths)
		if code != want {
			t.Fatalf("got %d want %d: %s %s", code, want, out.String(), stderr.String())
		}
		return out.Bytes()
	}
	public := filepath.Join(env, "capabilities.json")
	call(0, "capabilities", "export", "--native", native, "--target-profile", profile, "--output", public)
	call(1, "capabilities", "export", "--native", native, "--target-profile", profile, "--output", public)
	call(0, "capabilities", "export", "--environment", env, "--output", filepath.Join(env, "copy.json"))
	output := filepath.Join(env, "run")
	raw := call(0, "submit", "--bundle", bundle, "--environment", env, "--output", output)
	var receipt submission.Receipt
	if json.Unmarshal(raw, &receipt) != nil || receipt.TargetSelection == nil || receipt.TargetSelection.ProfileFile != profile {
		t.Fatal(string(raw))
	}
	call(0, "validate", "--run", output)
	call(2, "submit", "--bundle", bundle, "--environment", env, "--target-profile", profile, "--output", filepath.Join(env, "bad"))
	call(0, "submit", "--bundle", bundle, "--capabilities", public, "--target-profile", profile, "--output", filepath.Join(env, "explicit"))
	if err := os.WriteFile(profile, bytes.ReplaceAll([]byte(adminTarget), []byte("30000"), []byte("20000")), 0600); err != nil {
		t.Fatal(err)
	}
	call(1, "validate", "--run", output)
	if _, err := os.Stat(paths.StateRoot); !os.IsNotExist(err) {
		t.Fatal("offline workflow created campaign state", err)
	}
}
