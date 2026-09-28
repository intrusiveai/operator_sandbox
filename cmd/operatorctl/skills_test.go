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

	"github.com/intrusiveai/operator_sandbox/internal/skills"
)

func TestSkillCommands(t *testing.T) {
	dir, pin := installedContract(t)
	paths := configPaths(t)
	writeConfig(t, paths.ConfigFile, fmt.Sprintf("engine:\n  image: test\ncontract:\n  directory: %q\n  version: %q\n  digest: %q\n", dir, pin.Version, pin.Digest))
	if err := os.Mkdir(paths.StateRoot, 0700); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: example\ndescription: Test instructions.\n---\nUse the supplied context.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(want int, args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		code := runWithDefaults(context.Background(), args, &out, &stderr, paths)
		if code != want || (want != 0 && out.Len() != 0) {
			t.Fatalf("code=%d want=%d out=%s err=%s", code, want, &out, &stderr)
		}
		return out.Bytes()
	}
	call(1, "skill", "build", "--project", "demo", "--source", source)
	call(0, "skill", "keygen")
	call(1, "skill", "keygen")
	raw := call(0, "skill", "build", "--project", "demo", "--source", source)
	var receipt skills.Receipt
	if json.Unmarshal(raw, &receipt) != nil || receipt.SkillID != "demo:example" {
		t.Fatal(string(raw))
	}
	call(0, "skill", "check", "--skill", receipt.Digest)
	call(0, "skill", "build", "--project", "demo", "--source", source)
	bundle := filepath.Join(paths.StateRoot, "skills", strings.TrimPrefix(receipt.Digest, "sha256:"))
	call(0, "skill", "import", "--source", bundle)
	setFile := filepath.Join(source, "frozen.json")
	call(0, "skill", "set", "--skill", receipt.Digest, "--loader-digest", "sha256:"+strings.Repeat("a", 64), "--output", setFile)
	call(1, "skill", "set", "--skill", receipt.Digest, "--loader-digest", "sha256:"+strings.Repeat("a", 64), "--output", setFile)
	// Keep the instruction source inventory unchanged for subsequent rebuild.
	if err := os.Remove(setFile); err != nil {
		t.Fatal(err)
	}
	call(0, "skill", "remove", "--skill", receipt.Digest)
	call(0, "skill", "remove", "--skill", receipt.Digest)
	call(1, "skill", "check", "--skill", receipt.Digest)
	call(0, "skill", "build", "--project", "demo", "--source", source)
	call(0, "skill", "check", "--skill", receipt.Digest)
	call(1, "skill", "check", "--skill", "../../escape")
	call(2, "skill", "build", "--source", source)
	call(2, "skill", "keygen", "--unknown")
	if err := os.WriteFile(filepath.Join(bundle, "files", "SKILL.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	call(1, "skill", "check", "--skill", receipt.Digest)
}
