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

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
	"github.com/intrusiveai/operator_sandbox/schemas"
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

func TestHTTPSCapabilityExportFromPrivateProfile(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, fmt.Sprintf("engine: {image: test}\ncontract: {directory: %q, version: %q, digest: %q}\n", dir, pin.Version, pin.Digest))
	env := t.TempDir()
	profile := filepath.Join(env, "target-profile.json")
	raw, err := os.ReadFile("../../examples/https-target-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(profile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(env, "capabilities.json")
	var out, stderr bytes.Buffer
	code := runWithDefaults(context.Background(), []string{"capabilities", "export", "--target-profile", profile, "--output", public}, &out, &stderr, paths)
	if code != 0 {
		t.Fatal(code, stderr.String())
	}
	c, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		TargetID string `json:"target_id"`
	}
	_ = json.Unmarshal(raw, &settings)
	exported, err := capabilities.LoadExport(context.Background(), c, public, settings.TargetID)
	if err != nil || exported.Adapter() != "https/v1" {
		t.Fatal(exported, err)
	}
	if bytes.Contains(exported.PublicJSON(), []byte("agent.example.com")) || bytes.Contains(exported.NativeJSON(), []byte("authentication")) {
		t.Fatal("private mapping leaked")
	}
	if _, err := os.Stat(paths.StateRoot); !os.IsNotExist(err) {
		t.Fatal("offline export created campaign state", err)
	}
}
