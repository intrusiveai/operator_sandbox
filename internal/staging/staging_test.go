//go:build linux || darwin

package staging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/schemas"
)

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func digest(t *testing.T, b []byte, limit int) string {
	t.Helper()
	d, e := contracts.CanonicalDigest(b, limit)
	if e != nil {
		t.Fatal(e)
	}
	return d
}

func protocol(t *testing.T) *contracts.Protocol {
	t.Helper()
	p, e := contracts.LoadProtocol(schemas.Files)
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	names, e := fs.ReadDir(schemas.Files, ".")
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range names {
		files[n.Name()], e = schemas.Files.ReadFile(n.Name())
		if e != nil {
			t.Fatal(e)
		}
	}
	profiles := map[string]string{"jcs-v1": "semantics/jcs", "manifest-paths-v1": "semantics/paths", "harness-loop-v1": "semantics/loop"}
	for _, name := range profiles {
		files[name] = []byte("Fixture only, not a release.")
	}
	m, pin, e := p.BuildPackageManifest("0.0.0", files, profiles)
	if e != nil {
		t.Fatal(e)
	}
	p, e = p.LoadVerifiedProtocol(m, files, pin)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

// These small documents exercise staging integrity only. They intentionally do
// not represent a campaign that has passed semantic/release/target admission.
func inputs(t *testing.T, skills map[string][]byte) (Manifests, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{"input/run-context.json": []byte(`{}`), "input/scenario-bundle.json": []byte(`{}`), "input/system-prompt.txt": []byte("test guidance\n")}
	entries := []any{}
	for _, item := range []struct{ name, role, media, schema string }{
		{"run-context.json", "engine-context", "application/json", contracts.EngineContextSchema},
		{"scenario-bundle.json", "scenario-bundle", "application/json", "urn:operator:schema:scenario-bundle:v1alpha1"},
		{"system-prompt.txt", "system-prompt", "text/plain", ""},
	} {
		b := files["input/"+item.name]
		e := map[string]any{"entry_id": item.role, "root_kind": "input", "path": item.name, "role": item.role, "media_type": item.media, "size_bytes": len(b), "digest": contracts.RawDigest(b)}
		if item.schema != "" {
			e["schema_id"] = item.schema
		}
		entries = append(entries, e)
	}
	m := Manifests{InputTree: encode(t, map[string]any{"api_version": "operator.dev/input-tree-manifest/v1alpha1", "entries": entries})}
	selected := []any{}
	if skills != nil {
		names := []string{}
		for n := range skills {
			names = append(names, n)
		}
		sort.Strings(names)
		inventory := []any{}
		for _, n := range names {
			raw := skills[n]
			inventory = append(inventory, map[string]any{"path": n, "media_type": "text/markdown", "size_bytes": len(raw), "digest": contracts.RawDigest(raw)})
			files["customer-skills/custom-1/"+n] = raw
		}
		skill := encode(t, map[string]any{"api_version": "operator.dev/skill-manifest/v1alpha1", "skill_id": "custom-1", "name": "Custom instructions", "description": "Passive guidance.", "entrypoint": "SKILL.md", "files": inventory})
		m.Skills = [][]byte{skill}
		selected = append(selected, map[string]any{"skill_id": "custom-1", "bundle_digest": contracts.RawDigest([]byte("fixture")), "manifest": map[string]any{"slot": 0, "schema_id": contracts.SkillManifestSchema, "size_bytes": len(skill), "digest": contracts.RawDigest(skill), "object_digest": digest(t, skill, contracts.SkillManifestLimit)}})
	}
	set := map[string]any{"api_version": "operator.dev/skill-set-manifest/v1alpha1", "loader_schema": "operator.dev/instruction-skill-loader/v1alpha1", "loader_digest": contracts.RawDigest([]byte("fixture loader")), "skills": selected}
	set["loading_digest"] = digest(t, encode(t, set), contracts.ControlLimit)
	m.SkillSet = encode(t, set)
	return m, files
}

func parent(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if e := os.Chmod(d, 0700); e != nil {
		t.Fatal(e)
	}
	return d
}

func TestMaterializeReadOnlyIndependentTrees(t *testing.T) {
	p := protocol(t)
	for _, withSkills := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty skills", true: "selected skills"}[withSkills], func(t *testing.T) {
			var skills map[string][]byte
			if withSkills {
				skills = map[string][]byte{"SKILL.md": []byte("# Guidance\n"), "refs/note.md": []byte("passive reference")}
			}
			m, files := inputs(t, skills)
			stage, e := Create(context.Background(), p, parent(t), m, files)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if e := stage.Discard(); e != nil {
					t.Error(e)
				}
			})
			if e := stage.Verify(context.Background()); e != nil {
				t.Fatal(e)
			}
			for name, raw := range files {
				name = filepath.Join(stage.Directory(), filepath.FromSlash(name))
				got, e := os.ReadFile(name)
				if e != nil || !bytes.Equal(raw, got) {
					t.Fatal(name, e)
				}
				i, e := os.Stat(name)
				if e != nil || i.Mode().Perm() != 0444 {
					t.Fatal(i, e)
				}
			}
			for _, d := range stage.dirs {
				i, e := os.Stat(filepath.Join(stage.Directory(), d))
				if e != nil || i.Mode().Perm() != 0555 {
					t.Fatal(i, e)
				}
			}
			if withSkills {
				if _, e := os.Stat(filepath.Join(stage.Directory(), "manifests/skills/0000.json")); e != nil {
					t.Fatal(e)
				}
			}
			report := stage.Receipt()
			if report.Contract.Version != "0.0.0" || report.FileCount != len(stage.files)-1 || report.ContentBytes <= 0 {
				t.Fatal(report)
			}
			files["input/system-prompt.txt"][0] = '!'
			m.InputTree[0] = '!'
			m.SkillSet[0] = '!'
			if e := stage.Verify(context.Background()); e != nil {
				t.Fatal("caller mutation changed staged files", e)
			}
		})
	}
}

func TestRejectInputsBeforeWriting(t *testing.T) {
	p := protocol(t)
	for _, name := range []string{"extra", "missing", "tamper", "loading digest", "skill object digest", "unverified protocol", "traversal"} {
		t.Run(name, func(t *testing.T) {
			m, files := inputs(t, map[string][]byte{"SKILL.md": []byte("# Guidance\n")})
			use := p
			switch name {
			case "extra":
				files["input/extra"] = nil
			case "missing":
				delete(files, "input/system-prompt.txt")
			case "tamper":
				files["input/system-prompt.txt"][0] = '!'
			case "loading digest", "skill object digest":
				var set map[string]any
				if e := json.Unmarshal(m.SkillSet, &set); e != nil {
					t.Fatal(e)
				}
				if name == "loading digest" {
					set["loading_digest"] = contracts.RawDigest(nil)
				} else {
					set["skills"].([]any)[0].(map[string]any)["manifest"].(map[string]any)["object_digest"] = contracts.RawDigest(nil)
					delete(set, "loading_digest")
					set["loading_digest"] = digest(t, encode(t, set), contracts.ControlLimit)
				}
				m.SkillSet = encode(t, set)
			case "unverified protocol":
				var e error
				use, e = contracts.LoadProtocol(schemas.Files)
				if e != nil {
					t.Fatal(e)
				}
			case "traversal":
				m.InputTree = bytes.ReplaceAll(m.InputTree, []byte("system-prompt.txt"), []byte("../system-prompt.txt"))
			}
			d := parent(t)
			if _, e := Create(context.Background(), use, d, m, files); !errors.Is(e, ErrInputs) {
				t.Fatal(e)
			}
			entries, e := os.ReadDir(d)
			if e != nil || len(entries) != 0 {
				t.Fatal(entries, e)
			}
		})
	}
}

func TestFailedMaterializationRemovesPartialTree(t *testing.T) {
	m, files := inputs(t, map[string][]byte{"SKILL.md": []byte("# Guidance\n"), strings.Repeat("x", 300) + ".md": []byte("cannot fit host filesystem name")})
	d := parent(t)
	if stage, e := Create(context.Background(), protocol(t), d, m, files); e == nil || stage != nil {
		t.Fatal(stage, e)
	}
	entries, e := os.ReadDir(d)
	if e != nil || len(entries) != 0 {
		t.Fatal("partial tree leaked", entries, e)
	}
}

func TestVerifyDetectsDiskChanges(t *testing.T) {
	p := protocol(t)
	for _, kind := range []string{"bytes", "mode", "extra", "missing", "symlink", "hardlink", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			m, files := inputs(t, nil)
			stage, e := Create(context.Background(), p, parent(t), m, files)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if e := stage.Discard(); e != nil {
					t.Error(e)
				}
			})
			d := filepath.Join(stage.Directory(), "input")
			file := filepath.Join(d, "system-prompt.txt")
			if e = os.Chmod(d, 0755); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "bytes":
				e = os.Chmod(file, 0644)
				if e == nil {
					e = os.WriteFile(file, []byte("evil guidance\n"), 0444)
				}
				if e == nil {
					e = os.Chmod(file, 0444)
				}
			case "mode":
				e = os.Chmod(file, 0644)
			case "extra":
				e = os.WriteFile(filepath.Join(d, "extra"), nil, 0444)
			case "missing":
				e = os.Remove(file)
			case "symlink":
				e = os.Remove(file)
				if e == nil {
					e = os.Symlink("run-context.json", file)
				}
			case "hardlink":
				e = os.Link(file, filepath.Join(t.TempDir(), "linked"))
			case "receipt":
				e = os.WriteFile(filepath.Join(stage.Directory(), "staging-receipt.json"), []byte("{}"), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Chmod(d, 0555); e != nil {
				t.Fatal(e)
			}
			if e = stage.Verify(context.Background()); e == nil {
				t.Fatal("modified tree verified")
			}
		})
	}
}

func TestCaptureRejectsLinksSpecialsAndOversize(t *testing.T) {
	d := parent(t)
	name := filepath.Join(d, "source")
	if e := os.WriteFile(name, []byte("bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := Capture(context.Background(), name, 5)
	if e != nil || string(raw) != "bytes" {
		t.Fatal(e)
	}
	if _, e = Capture(context.Background(), name, 4); e == nil {
		t.Fatal("oversize accepted")
	}
	link := filepath.Join(d, "link")
	if e = os.Symlink(name, link); e != nil {
		t.Fatal(e)
	}
	if _, e = Capture(context.Background(), link, 5); e == nil {
		t.Fatal("symlink accepted")
	}
	if e = os.Link(name, filepath.Join(d, "hardlink")); e != nil {
		t.Fatal(e)
	}
	if _, e = Capture(context.Background(), name, 5); e == nil {
		t.Fatal("hardlink accepted")
	}
	fifo := filepath.Join(d, "fifo")
	if e = syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Capture(context.Background(), fifo, 5); e == nil {
		t.Fatal("fifo accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Capture(ctx, name, 5); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestPrivateParentAndCanceledPreparation(t *testing.T) {
	p := protocol(t)
	m, files := inputs(t, nil)
	d := parent(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Create(ctx, p, d, m, files); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := os.Chmod(d, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := Create(context.Background(), p, d, m, files); !errors.Is(e, ErrFilesystem) {
		t.Fatal(e)
	}
	if e := os.Chmod(d, 0700); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(parent(t), "link")
	if e := os.Symlink(d, link); e != nil {
		t.Fatal(e)
	}
	if _, e := Create(context.Background(), p, link, m, files); !errors.Is(e, ErrFilesystem) {
		t.Fatal(e)
	}
}
