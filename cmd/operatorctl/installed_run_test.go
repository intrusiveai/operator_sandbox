//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
)

func TestInstalledInputsFreezeOfflineStartFingerprint(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	source := t.TempDir()
	put := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile("../../schemas/fixtures/capability-chain/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	native := fixture("interceptor-export.json")
	put(filepath.Join(source, submission.ArtifactName(contracts.RawDigest(native))+".json"), native)
	capability, bundle, output := filepath.Join(source, "capabilities.json"), filepath.Join(source, "bundle.json"), filepath.Join(source, "run")
	put(capability, fixture("public-capabilities.json"))
	put(bundle, fixture("submitted-bundle.json"))
	modelPath, targetPath, credentialPath := filepath.Join(source, "model.json"), filepath.Join(source, "target.json"), filepath.Join(source, "credentials.yaml")
	target := []byte(`{"api_version":"operator.dev/target-profile/v1alpha1","id":"local","target_id":"delivery-example","adapter":"interceptor/v1","allow_target_stop":false,"scopes":{"operation_ids":["invoke"],"caller_principal_ids":[],"routes":[],"allow_retained_injections":false},"feedback":{"ceiling":"black-box","allowed_kinds":["target_output"],"max_attempt_bytes":1048576},"operation_timeout_ms":30000}`)
	model := []byte(`{"api_version":"operator.dev/model-provider-profile/v1alpha1","id":"model","provider":"openai-chat","codec_id":"openai-chat-text-tools-v1","model":"test-model","endpoint":"https://private.example/chat/completions","authentication":"secret-store","credential_id":"key","maximum_prompt_tokens":10000,"maximum_completion_tokens":1024,"maximum_response_bytes":1048576,"codec_options":{"instruction_role":"developer","response_models":["test-model"]}}`)
	credential := []byte(`profiles:
  - api_version: operator.dev/secret-store-profile/v1alpha1
    kind: SecretStoreProfile
    id: store
    backend_kind: gcp-secret-manager
    allowed_locator_prefixes: [gcp://projects/test/secrets/]
credentials:
  - api_version: operator.dev/host-credential-ref/v1alpha1
    kind: HostCredentialRef
    credential_id: key
    store_profile_id: store
    locator: {backend_kind: gcp-secret-manager, project: test, secret_name: provider-key, version: latest}
    value: {format: utf8}
`)
	put(modelPath, model)
	put(targetPath, target)
	put(credentialPath, credential)
	config := fmt.Sprintf("engine: {image: local-harness}\ncontract: {directory: %q, version: %q, digest: %q}\ntarget: {profile_file: %q}\nmodel: {profile_file: %q}\ncredentials: {file: %q}\ndocker: {executable: /nonexistent/operator-test-docker}\n", dir, pin.Version, pin.Digest, targetPath, modelPath, credentialPath)
	writeConfig(t, paths.ConfigFile, config)
	var stdout, stderr bytes.Buffer
	if code := runWithDefaults(context.Background(), []string{"submit", "--bundle", bundle, "--capabilities", capability, "--output", output}, &stdout, &stderr, paths); code != 0 {
		t.Fatal(code, stderr.String())
	}
	selected := hostrun.NewSelection(output)
	appendPath := filepath.Join(source, "prompt.txt")
	put(appendPath, []byte("Retain distinctions between receipts and assertions.\n"))
	selected.PromptMode = "extension"
	selected.AppendFiles = []string{appendPath}
	load := func() (*hostrun.InstalledInputs, error) {
		return hostrun.LoadInputs(context.Background(), paths.ConfigFile, paths, selected)
	}
	first, err := load()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := contractstore.Load(context.Background(), dir, pin)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := skills.Select(context.Background(), installed.Protocol(), "/unused/keys", "/unused/store", "sha256:"+strings.Repeat("a", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	setPath := filepath.Join(source, "set.json")
	put(setPath, empty.Manifest())
	selected.SkillSetFile = setPath
	withSet, err := load()
	if err != nil || withSet.Fingerprint() == first.Fingerprint() {
		t.Fatal("set not fingerprinted", err)
	}
	put(setPath, append(empty.Manifest(), ' '))
	changedSet, err := load()
	if err != nil || changedSet.Fingerprint() == withSet.Fingerprint() {
		t.Fatal("changed set not fingerprinted", err)
	}
	put(setPath, []byte(`{}`))
	if _, err := load(); err == nil {
		t.Fatal("invalid set accepted offline")
	}
	selected.SkillSetFile = ""
	second, err := load()
	if err != nil || first.Fingerprint() != second.Fingerprint() {
		t.Fatal("unstable fingerprint", err)
	}
	alternate := paths
	alternate.StateRoot = filepath.Join(source, "different-state-root")
	changedDefaults, err := hostrun.LoadInputs(context.Background(), paths.ConfigFile, alternate, selected)
	if err != nil || changedDefaults.Fingerprint() == first.Fingerprint() {
		t.Fatal("effective installation defaults not bound", err)
	}
	frozenSelection := first.Selection()
	frozenSelection.AppendFiles[0] = "/changed"
	if first.Selection().AppendFiles[0] != appendPath || first.Installation().StateRoot != paths.StateRoot {
		t.Fatal("mutable installed selection")
	}
	for _, tc := range []struct {
		name, path    string
		before, after []byte
	}{
		{"model", modelPath, model, bytes.Replace(model, []byte("10000"), []byte("10001"), 1)},
		{"target", targetPath, target, bytes.Replace(target, []byte("30000"), []byte("29999"), 1)},
		{"locator", credentialPath, credential, bytes.Replace(credential, []byte("provider-key"), []byte("new-key"), 1)},
		{"prompt", appendPath, []byte("Retain distinctions between receipts and assertions.\n"), []byte("Changed instruction.\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			put(tc.path, tc.after)
			defer put(tc.path, tc.before)
			changed, err := load()
			if err != nil || changed.Fingerprint() == first.Fingerprint() {
				t.Fatal("change not bound", err)
			}
		})
	}
	if _, err := os.Stat(paths.StateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("offline loading created runtime state", err)
	}
	// The held object retains captured input identity after source mutation.
	original := first.Fingerprint()
	put(appendPath, []byte("source changed after load"))
	if first.Fingerprint() != original {
		t.Fatal("frozen input changed")
	}
	if _, err := first.Open(context.Background(), "0.1.0"); err == nil {
		t.Fatal("missing Docker executable accepted")
	}
	if _, err := first.Open(context.Background(), "0.1.0"); !errors.Is(err, hostrun.ErrSession) {
		t.Fatal("online preparation repeated", err)
	}
	put(targetPath, bytes.Replace(target, []byte("delivery-example"), []byte("other-target"), 1))
	if _, err := load(); err == nil {
		t.Fatal("wrong offline target accepted")
	}
	put(targetPath, target)
	put(credentialPath, bytes.Replace(credential, []byte("credential_id: key"), []byte("credential_id: another"), 1))
	if _, err := load(); err == nil {
		t.Fatal("unconfigured credential accepted")
	}
	put(credentialPath, credential)
	put(filepath.Join(output, "input/scenario-bundle.json"), append(fixture("submitted-bundle.json"), ' '))
	if _, err := load(); err == nil {
		t.Fatal("changed submission accepted")
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "private.example") || strings.Contains(string(raw), "provider-key") {
		t.Fatal("private configuration exposed")
	}
}
