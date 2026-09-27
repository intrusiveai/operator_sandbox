//go:build linux || darwin

// Package contractpublish builds a reproducible content-only contract snapshot
// from a trusted source checkout. Publication never executes source payloads.
package contractpublish

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

var ErrSource = errors.New("contract publication requires a complete matching source checkout and a new output directory")

// Build selects only documented source, schema, fixture and lockfile paths.
// Runtime installation MUST obtain the returned pin independently of the payload.
func Build(ctx context.Context, source, output, version string) (contractstore.Report, error) {
	var empty contractstore.Report
	if !filepath.IsAbs(source) || !filepath.IsAbs(output) || filepath.Clean(source) != source || filepath.Clean(output) != output {
		return empty, ErrSource
	}
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() {
		return empty, ErrSource
	}
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		return empty, err
	}
	files := map[string][]byte{}
	total := 0
	add := func(name string, raw []byte) error {
		if _, ok := files[name]; ok || len(files) >= 4096 || len(raw) > contracts.PackageFileLimit || total+len(raw) > contracts.PackageContentLimit {
			return ErrSource
		}
		files[name] = bytes.Clone(raw)
		total += len(raw)
		return nil
	}
	// Preserve a runnable source layout under source/. Both languages' tests use
	// the same source/schemas/fixtures paths. The installed runtime catalog also
	// has the required bare names at the package root.
	for _, directory := range []string{"schemas", "contracts"} {
		err = filepath.WalkDir(filepath.Join(source, directory), func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, e := filepath.Rel(source, name)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "__pycache__" {
					return filepath.SkipDir
				}
				if directory == "contracts" && rel != "contracts" && rel != "contracts/python" && rel != "contracts/python/operator_contracts" && rel != "contracts/python/tests" {
					return filepath.SkipDir
				}
				return nil
			}
			if !selected(rel) {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return ErrSource
			}
			raw, e := staging.Capture(ctx, name, contracts.PackageFileLimit)
			if e != nil {
				return e
			}
			if e = add("source/"+rel, raw); e != nil {
				return e
			}
			if path.Dir(rel) == "schemas" && (strings.HasSuffix(rel, ".schema.json") || entry.Name() == "catalog.json" || entry.Name() == "operations.json") {
				// The publisher binary and checkout must agree on schema bytes.
				// Runtime qualification of source implementations remains separate.
				embedded, e := schemas.Files.ReadFile(entry.Name())
				if e != nil || !bytes.Equal(raw, embedded) {
					return ErrSource
				}
				return add(entry.Name(), raw)
			}
			return nil
		})
		if err != nil {
			return empty, err
		}
	}
	for _, name := range []string{"schemas/embed.go", "contracts/protocol.go", "contracts/python/operator_contracts/protocol.py", "contracts/python/requirements.lock", "contracts/python/pyproject.toml", "schemas/fixtures/ordinary-protocol.json"} {
		if len(files["source/"+name]) == 0 {
			return empty, ErrSource
		}
	}
	// Contract-only Go dependencies are deliberately independent of host cloud
	// SDKs. Keep their versions identical to the source checkout's pinned versions.
	mod, err := staging.Capture(ctx, filepath.Join(source, "go.mod"), 1<<20)
	if err != nil {
		return empty, err
	}
	sum, err := staging.Capture(ctx, filepath.Join(source, "go.sum"), 1<<20)
	if err != nil {
		return empty, err
	}
	modules := []string{"github.com/santhosh-tekuri/jsonschema/v6", "golang.org/x/text"}
	versions := map[string]string{}
	for _, line := range strings.Split(string(mod), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(fields) >= 2 {
			for _, module := range modules {
				if fields[0] == module {
					versions[module] = fields[1]
				}
			}
		}
	}
	var goMod, goSum strings.Builder
	goMod.WriteString("module github.com/intrusiveai/operator_sandbox\n\ngo 1.26.0\n\nrequire (\n")
	for _, module := range modules {
		version := versions[module]
		if version == "" {
			return empty, ErrSource
		}
		goMod.WriteString("\t" + module + " " + version + "\n")
		count := 0
		for _, line := range strings.Split(string(sum), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == module && (fields[1] == version || fields[1] == version+"/go.mod") {
				goSum.WriteString(line + "\n")
				count++
			}
		}
		if count != 2 {
			return empty, ErrSource
		}
	}
	goMod.WriteString(")\n")
	if err := add("source/go.mod", []byte(goMod.String())); err != nil {
		return empty, err
	}
	if err := add("source/go.sum", []byte(goSum.String())); err != nil {
		return empty, err
	}
	profiles := map[string]string{"jcs-v1": "source/schemas/CANONICAL_IDENTITY_CONTRACT.md", "manifest-paths-v1": "source/schemas/STARTUP_MANIFEST_CONTRACT.md", "harness-loop-v1": "source/schemas/HARNESS_LOOP_ACCOUNTING_CONTRACT.md"}
	manifest, pin, err := p.BuildPackageManifest(version, files, profiles)
	if err != nil {
		return empty, err
	}
	if _, err = p.LoadVerifiedProtocol(manifest, files, pin); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return empty, err
	}
	r, err := os.OpenRoot(output)
	if err != nil {
		return empty, err
	}
	defer r.Close()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	dirs := map[string]bool{".": true}
	for _, name := range names {
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			dir := strings.Join(parts[:i], "/")
			if !dirs[dir] {
				if err := r.Mkdir(dir, 0755); err != nil {
					return empty, err
				}
				dirs[dir] = true
			}
		}
		if err := write(ctx, r, name, files[name]); err != nil {
			return empty, err
		}
	}
	ordered := []string{}
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ordered)))
	for _, dir := range ordered {
		if err := syncDir(r, dir); err != nil {
			return empty, err
		}
	}
	// Complete inventory metadata is the final commit marker.
	if err := write(ctx, r, "package.json", manifest); err != nil {
		return empty, err
	}
	if err := syncDir(r, "."); err != nil {
		return empty, err
	}
	parent, err := os.Open(filepath.Dir(output))
	if err != nil {
		return empty, err
	}
	err = errors.Join(parent.Sync(), parent.Close())
	if err != nil {
		return empty, err
	}
	loaded, err := contractstore.Load(ctx, output, pin)
	if err != nil {
		return empty, err
	}
	return loaded.Report(), nil
}
func selected(name string) bool {
	if strings.HasPrefix(name, "schemas/") {
		if path.Dir(name) == "schemas" {
			return strings.HasSuffix(name, ".schema.json") || strings.HasSuffix(name, ".md") || name == "schemas/catalog.json" || name == "schemas/operations.json" || name == "schemas/embed.go"
		}
		return strings.HasPrefix(name, "schemas/fixtures/") && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".txt"))
	}
	if path.Dir(name) == "contracts" {
		return strings.HasSuffix(name, ".go") || name == "contracts/README.md"
	}
	if path.Dir(name) == "contracts/python" {
		return name == "contracts/python/pyproject.toml" || name == "contracts/python/requirements.lock"
	}
	return (path.Dir(name) == "contracts/python/operator_contracts" || path.Dir(name) == "contracts/python/tests") && strings.HasSuffix(name, ".py")
}
func write(ctx context.Context, r *os.Root, name string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func syncDir(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
