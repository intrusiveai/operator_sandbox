//go:build linux || darwin

package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func protocol(t *testing.T) *contracts.Protocol {
	t.Helper()
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	names, err := fs.ReadDir(schemas.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		files[name.Name()], err = schemas.Files.ReadFile(name.Name())
		if err != nil {
			t.Fatal(err)
		}
	}
	profiles := map[string]string{"jcs-v1": "semantics/jcs", "manifest-paths-v1": "semantics/paths", "harness-loop-v1": "semantics/loop"}
	for _, name := range profiles {
		files[name] = []byte("Fixture only, not a release.")
	}
	m, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.LoadVerifiedProtocol(m, files, pin)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func put(t *testing.T, name string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func skillText(name string) []byte {
	return []byte("---\nname: " + name + "\ndescription: Evaluate an example scenario.\n---\nUse the supplied references.\n")
}

func TestBuildInstallSelectAndImport(t *testing.T) {
	ctx := context.Background()
	p := protocol(t)
	base := t.TempDir()
	store, source := filepath.Join(base, "store"), filepath.Join(base, "source")
	put(t, filepath.Join(source, "SKILL.md"), bytes.ReplaceAll(skillText("example"), []byte("\n"), []byte("\r\n")))
	put(t, filepath.Join(source, "references", "example.json"), []byte("{ \"value\": 1 }\n"))
	b, err := Build(ctx, p, "project", source)
	if err != nil {
		t.Fatal(err)
	}
	if b.manifest.SkillID != "project:example" || b.Receipt().FileCount != 2 {
		t.Fatal(b.Receipt())
	}
	if bytes.Contains(b.files["SKILL.md"], []byte("\r")) {
		t.Fatal("CRLF not normalized")
	}
	for range 2 {
		if err := b.Install(ctx, p, store); err != nil {
			t.Fatal(err)
		}
	}
	loader := contracts.RawDigest([]byte("test loader"))
	s, err := Select(ctx, p, store, loader, []string{b.digest})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(s.Manifest(), &m) != nil || m["loader_digest"] != loader {
		t.Fatal("loader binding")
	}
	content := s.Contents()
	content["customer-skills/project:example/SKILL.md"][0] = 'x'
	if s.Contents()["customer-skills/project:example/SKILL.md"][0] != '-' {
		t.Fatal("mutable selection")
	}
	if _, err := Select(ctx, p, store, loader, []string{b.digest, b.digest}); err == nil {
		t.Fatal("duplicate selection")
	}
	put(t, filepath.Join(source, "references", "example.json"), []byte("{\"value\":2}"))
	second, err := Build(ctx, p, "project", source)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Install(ctx, p, store); err != nil {
		t.Fatal(err)
	}
	if _, err := Select(ctx, p, store, loader, []string{b.digest, second.digest}); err == nil {
		t.Fatal("two revisions of one skill selected")
	}
	// Empty selection neither discovers installed skills nor requires an existing store.
	empty, err := Select(ctx, p, "/missing", loader, nil)
	if err != nil || len(empty.Manifests()) != 0 {
		t.Fatal(err)
	}
	// Explicit import into a different installation needs only validated bytes.
	imported, err := Read(ctx, p, filepath.Join(store, b.digest[7:]))
	if err != nil {
		t.Fatal(err)
	}
	otherStore := filepath.Join(t.TempDir(), "skills")
	if err := imported.Install(ctx, p, otherStore); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInstalled(ctx, p, otherStore, b.digest); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store, strings.TrimPrefix(b.digest, "sha256:"))
	put(t, filepath.Join(dir, "files", "references", "example.json"), []byte("{\"value\":1}\n"))
	if _, err := LoadInstalled(ctx, p, store, b.digest); err == nil {
		t.Fatal("changed file bytes accepted")
	}
}

func TestRejectsOtherExtendedAttributes(t *testing.T) {
	p := protocol(t)
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "source")
	file := filepath.Join(source, "SKILL.md")
	put(t, file, skillText("test"))
	if _, err := Build(ctx, p, "project", source); err != nil {
		t.Fatal("ordinary OS metadata rejected", err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	err = unix.Fsetxattr(int(f.Fd()), "user.operator_test", []byte("metadata"), 0)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(ctx, p, "project", source); err == nil {
		t.Fatal("unapproved extended attribute accepted")
	}
}

func TestInvalidSourceAndNormalization(t *testing.T) {
	p := protocol(t)
	for _, kind := range []string{"unknown-frontmatter", "empty-body", "binary", "script", "registration", "duplicate-json", "yaml-alias", "yaml-tag", "yaml-duplicate", "case-collision", "prefix-collision", "unicode-collision", "escape", "symlink", "directory-link", "hardlink", "executable", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			put(t, filepath.Join(source, "SKILL.md"), skillText("test"))
			switch kind {
			case "unknown-frontmatter":
				put(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: x\ndescription: text\ncommand: run\n---\nbody"))
			case "empty-body":
				put(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: x\ndescription: text\n---\n"))
			case "binary":
				put(t, filepath.Join(source, "data.txt"), []byte{0xff})
			case "script":
				put(t, filepath.Join(source, "run.py"), []byte("print('hello')"))
			case "registration":
				put(t, filepath.Join(source, "commands", "test.md"), []byte("test"))
			case "duplicate-json":
				put(t, filepath.Join(source, "data.json"), []byte(`{"a":1,"a":2}`))
			case "yaml-alias":
				put(t, filepath.Join(source, "data.yaml"), []byte("a: &x value\nb: *x"))
			case "yaml-tag":
				put(t, filepath.Join(source, "data.yaml"), []byte("a: !run value"))
			case "yaml-duplicate":
				put(t, filepath.Join(source, "data.yaml"), []byte("a: x\na: y"))
			case "case-collision", "prefix-collision", "unicode-collision", "escape":
				// Test on maps to cover filesystems that cannot create both names.
				files := map[string][]byte{"SKILL.md": skillText("test")}
				switch kind {
				case "case-collision":
					files["A.md"], files["a.md"] = []byte("a"), []byte("a")
				case "prefix-collision":
					files["A/x.md"], files["a/y.md"] = []byte("a"), []byte("a")
				case "unicode-collision":
					files["é.md"], files["e\u0301.md"] = []byte("a"), []byte("a")
				case "escape":
					files["../escape.md"] = []byte("a")
				}
				if _, err := validateContent(p, "project", files); err == nil {
					t.Fatal("invalid content accepted")
				}
				return
			case "symlink":
				if err := os.Symlink("SKILL.md", filepath.Join(source, "alias.md")); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.Symlink(t.TempDir(), filepath.Join(source, "references")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(source, "SKILL.md"), filepath.Join(source, "alias.md")); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Chmod(filepath.Join(source, "SKILL.md"), 0700); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				put(t, filepath.Join(source, "large.txt"), bytes.Repeat([]byte("a"), 1<<20+1))
			}
			if _, err := Build(context.Background(), p, "project", source); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestInventoryCannotBeRenamedRepairedOrExtended(t *testing.T) {
	p := protocol(t)
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "source")
	put(t, filepath.Join(source, "SKILL.md"), skillText("test"))
	b, err := Build(ctx, p, "project", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"wrong-path-digest", "extra-file", "crlf-repair", "changed-content", "missing-content", "changed-manifest", "extra-envelope-file", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			store := filepath.Join(t.TempDir(), "store")
			if err := b.Install(ctx, p, store); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(store, strings.TrimPrefix(b.digest, "sha256:"))
			digest := b.digest
			switch kind {
			case "wrong-path-digest":
				digest = contracts.RawDigest([]byte("different"))
				if err := os.Rename(dir, filepath.Join(store, strings.TrimPrefix(digest, "sha256:"))); err != nil {
					t.Fatal(err)
				}
			case "extra-file":
				put(t, filepath.Join(dir, "files", "extra.md"), []byte("extra"))
			case "crlf-repair":
				put(t, filepath.Join(dir, "files", "SKILL.md"), bytes.ReplaceAll(skillText("test"), []byte("\n"), []byte("\r\n")))
			case "changed-manifest":
				put(t, filepath.Join(dir, "manifest.json"), append(b.raw, ' '))
			case "changed-content":
				put(t, filepath.Join(dir, "files", "SKILL.md"), bytes.ReplaceAll(skillText("test"), []byte("supplied"), []byte("modified")))
			case "missing-content":
				if err := os.Remove(filepath.Join(dir, "files", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			case "extra-envelope-file":
				put(t, filepath.Join(dir, "extra.json"), []byte(`{}`))
			case "incomplete":
				if err := os.Remove(filepath.Join(dir, "manifest.json")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := LoadInstalled(ctx, p, store, digest); err == nil {
				t.Fatal("invalid installed skill accepted")
			}
			if kind == "incomplete" {
				if err := b.Install(ctx, p, store); err == nil {
					t.Fatal("retry silently repaired incomplete publication")
				}
			}
		})
	}
}

func TestFrozenSetBindsLoaderAndExactBundleDescriptors(t *testing.T) {
	ctx := context.Background()
	p := protocol(t)
	base := t.TempDir()
	store, source := filepath.Join(base, "store"), filepath.Join(base, "source")
	put(t, filepath.Join(source, "SKILL.md"), skillText("example"))
	b, err := Build(ctx, p, "project", source)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Install(ctx, p, store); err != nil {
		t.Fatal(err)
	}
	loader := "sha256:" + strings.Repeat("a", 64)
	set, err := Select(ctx, p, store, loader, []string{b.digest})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := Frozen(ctx, p, store, set.Manifest())
	if err != nil || frozen.LoaderDigest() != loader {
		t.Fatal(err)
	}
	for _, field := range []string{"loading_digest", "loader_digest", "skill_id", "size_bytes"} {
		var m map[string]any
		_ = json.Unmarshal(set.Manifest(), &m)
		switch field {
		case "loading_digest", "loader_digest":
			m[field] = "sha256:" + strings.Repeat("b", 64)
		case "skill_id":
			m["skills"].([]any)[0].(map[string]any)[field] = "other"
		case "size_bytes":
			m["skills"].([]any)[0].(map[string]any)["manifest"].(map[string]any)[field] = 1
		}
		raw, _ := json.Marshal(m)
		if _, err := Frozen(ctx, p, store, raw); err == nil {
			t.Fatal("accepted changed", field)
		}
	}
	empty, err := Select(ctx, p, "/missing/store", loader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Frozen(ctx, p, "/missing/store", empty.Manifest()); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveReinstallAndFrozenCopy(t *testing.T) {
	ctx := context.Background()
	p := protocol(t)
	base := t.TempDir()
	store, source := filepath.Join(base, "store"), filepath.Join(base, "source")
	put(t, filepath.Join(source, "SKILL.md"), skillText("example"))
	b, err := Build(ctx, p, "project", source)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Install(ctx, p, store); err != nil {
		t.Fatal(err)
	}
	selected, err := Select(ctx, p, store, "sha256:"+strings.Repeat("a", 64), []string{b.digest})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := storeLease(store, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Remove(ctx, store, b.digest); err == nil {
		t.Fatal("removed during capture")
	}
	lease.Close()
	if removed, err := Remove(ctx, store, b.digest); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if _, err := LoadInstalled(ctx, p, store, b.digest); err == nil {
		t.Fatal("selected absent bundle")
	}
	if len(selected.Contents()) != 1 {
		t.Fatal("removal altered frozen campaign inputs")
	}
	if removed, err := Remove(ctx, store, b.digest); err != nil || removed {
		t.Fatal("absent", removed, err)
	}
	if err = b.Install(ctx, p, store); err != nil {
		t.Fatal("cannot add back", err)
	}
	if _, err := Frozen(ctx, p, store, selected.Manifest()); err != nil {
		t.Fatal("re-added bundle blocked", err)
	}
	// Partial/corrupt stored copies can be explicitly removed without validating
	// their now-incomplete manifest or inventory, then installed afresh.
	if err := os.Remove(filepath.Join(store, b.digest[7:], "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if removed, err := Remove(ctx, store, b.digest); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if err = b.Install(ctx, p, store); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	sentinel := filepath.Join(external, "keep")
	put(t, sentinel, []byte("keep"))
	if err := os.Symlink(external, filepath.Join(store, b.digest[7:], "files", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(ctx, store, b.digest); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatal("followed link", err)
	}
}
