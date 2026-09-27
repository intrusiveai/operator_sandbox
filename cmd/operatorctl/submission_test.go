//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
)

func TestSubmissionRoundTripAndTampering(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, fmt.Sprintf("engine:\n  image: test\ncontract:\n  directory: %q\n  version: %q\n  digest: %q\n", dir, pin.Version, pin.Digest))
	source := t.TempDir()
	read := func(name string) []byte {
		t.Helper()
		b, e := os.ReadFile("../../schemas/fixtures/capability-chain/" + name)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	put := func(name string, raw []byte) {
		t.Helper()
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	native := read("interceptor-export.json")
	put(filepath.Join(source, submission.ArtifactName(contracts.RawDigest(native))+".json"), native)
	capability := filepath.Join(source, "capabilities.json")
	put(capability, read("public-capabilities.json"))
	bundle := read("submitted-bundle.json")
	bundlePath := filepath.Join(source, "bundle.json")
	put(bundlePath, bundle)
	output := filepath.Join(source, "run")
	call := func(want int, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		code := runWithDefaults(context.Background(), args, &out, &stderr, paths)
		if code != want || (want != 0 && out.Len() != 0) {
			t.Fatalf("code=%d want=%d stdout=%s stderr=%s", code, want, out.String(), stderr.String())
		}
		return out.Bytes()
	}
	args := []string{"submit", "--bundle", bundlePath, "--capabilities", capability, "--output", output}
	raw := call(0, args...)
	var r submission.Receipt
	if json.Unmarshal(raw, &r) != nil || r.Status != "validated-offline" || r.Contract != pin || r.BundleDigest != contracts.RawDigest(bundle) {
		t.Fatal(string(raw))
	}
	call(0, "validate", "--run", output)
	call(1, args...) // Never overwrite a submitted run.
	if _, err := os.Stat(paths.StateRoot); !os.IsNotExist(err) {
		t.Fatal("offline command created authoritative campaign state", err)
	}
	saved := filepath.Join(output, "input/scenario-bundle.json")
	put(saved, append(bundle, ' '))
	call(1, "validate", "--run", output) // Even semantically identical changed bytes fail.
	put(saved, bundle)
	call(0, "validate", "--run", output)
	if e := os.Remove(saved); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(bundlePath, saved); e != nil {
		t.Fatal(e)
	}
	call(1, "validate", "--run", output)
}

func TestSubmissionArtifactsAndInvalidInputs(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, fmt.Sprintf("engine:\n  image: test\ncontract:\n  directory: %q\n  version: %q\n  digest: %q\n", dir, pin.Version, pin.Digest))
	for _, kind := range []string{"valid", "raw-json", "missing", "corrupt", "unlisted", "symlink", "hardlink", "directory", "optional-omission", "wrong-source", "required-ref", "optional-ref", "no-contract"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			put := func(name string, raw []byte) {
				t.Helper()
				if e := os.WriteFile(filepath.Join(source, name), raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			fixture := func(name string) []byte {
				t.Helper()
				b, e := os.ReadFile("../../schemas/fixtures/capability-chain/" + name)
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			native := fixture("interceptor-export.json")
			put(submission.ArtifactName(contracts.RawDigest(native))+".json", native)
			put("capabilities.json", fixture("public-capabilities.json"))
			var b map[string]any
			if e := json.Unmarshal(fixture("submitted-bundle.json"), &b); e != nil {
				t.Fatal(e)
			}
			content := []byte("reference text")
			media := "text/plain"
			if kind == "raw-json" {
				content = []byte("{ \"value\": 1 }\n")
				media = "application/json"
			}
			digest := contracts.RawDigest(content)
			a := map[string]any{"artifact_id": "reference", "digest": digest, "size_bytes": len(content), "media_type": media, "purpose": "context", "visibility": "operator-engine", "required": false}
			b["artifacts"] = []any{a}
			if e := os.Mkdir(filepath.Join(source, "artifacts"), 0700); e != nil {
				t.Fatal(e)
			}
			artifact := "artifacts/" + submission.ArtifactName(digest)
			put(artifact, content)
			want := 1
			switch kind {
			case "valid", "raw-json":
				want = 0
			case "missing":
				os.Remove(filepath.Join(source, artifact))
			case "corrupt":
				put(artifact, []byte("corrupt"))
			case "unlisted":
				put("artifacts/extra", []byte("x"))
			case "symlink", "hardlink", "directory":
				os.Remove(filepath.Join(source, artifact))
				put("reference", content)
				var e error
				switch kind {
				case "symlink":
					e = os.Symlink(filepath.Join(source, "reference"), filepath.Join(source, artifact))
				case "hardlink":
					e = os.Link(filepath.Join(source, "reference"), filepath.Join(source, artifact))
				case "directory":
					e = os.Mkdir(filepath.Join(source, artifact), 0700)
				}
				if e != nil {
					t.Fatal(e)
				}
			case "optional-omission":
				a["omission_reason"] = "not provided"
				os.Remove(filepath.Join(source, artifact))
				want = 0
			case "wrong-source":
				b["target_requirements"].(map[string]any)["capability_source_digest"] = "sha256:" + strings.Repeat("0", 64)
			case "required-ref":
				b["target_requirements"].(map[string]any)["required_capability_refs"] = []string{"operation:absent"}
			case "optional-ref":
				s := b["scenarios"].([]any)[0].(map[string]any)
				s["required"] = false
				s["required_capability_refs"] = []string{"operation:absent"}
				want = 0
			case "no-contract":
				writeConfig(t, paths.ConfigFile, "engine:\n  image: test\n")
			}
			raw, e := json.Marshal(b)
			if e != nil {
				t.Fatal(e)
			}
			put("bundle.json", raw)
			output := filepath.Join(source, "run")
			var out, stderr bytes.Buffer
			code := runWithDefaults(context.Background(), []string{"submit", "--bundle", filepath.Join(source, "bundle.json"), "--capabilities", filepath.Join(source, "capabilities.json"), "--artifacts", filepath.Join(source, "artifacts"), "--output", output}, &out, &stderr, paths)
			if code != want {
				t.Fatal(code, want, out.String(), stderr.String())
			}
			if want != 0 {
				if _, e := os.Stat(output); !os.IsNotExist(e) {
					t.Fatal("invalid input published run", e)
				}
				return
			}
			out.Reset()
			stderr.Reset()
			if code := runWithDefaults(context.Background(), []string{"validate", "--run", output}, &out, &stderr, paths); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
}
