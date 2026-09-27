//go:build linux || darwin

// Package skills validates instruction-only data and verifies installation-local
// signatures. No skill content is evaluated, imported as code, or executed.
package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/unicode/norm"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

var ErrSkill = errors.New("invalid instruction-only skill: check metadata, inventory, limits and installed signing key")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type fileEntry struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size_bytes"`
	Digest    string `json:"digest"`
}
type manifest struct {
	APIVersion  string      `json:"api_version"`
	SkillID     string      `json:"skill_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Entrypoint  string      `json:"entrypoint"`
	Files       []fileEntry `json:"files"`
}
type Bundle struct {
	manifest       manifest
	raw, signature []byte
	files          map[string][]byte
	digest         string
}
type Receipt struct {
	APIVersion   string `json:"api_version"`
	SkillID      string `json:"skill_id"`
	Digest       string `json:"manifest_digest"`
	FileCount    int    `json:"file_count"`
	ContentBytes int    `json:"content_bytes"`
}

func (b *Bundle) Receipt() Receipt {
	total := 0
	for _, f := range b.manifest.Files {
		total += f.Size
	}
	return Receipt{"operator.dev/skill-build-receipt/v1alpha1", b.manifest.SkillID, b.digest, len(b.files), total}
}

// Build captures and validates the entire source before reading the signing key.
// The key directory is an explicit installation path, never supplied by a skill.
func Build(ctx context.Context, p *contracts.Protocol, keyDirectory, project, source string) (*Bundle, error) {
	files, err := staging.CaptureInstructionTree(ctx, source)
	if err != nil {
		return nil, err
	}
	b, err := validateContent(p, project, files)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.signature, err = sign(keyDirectory, b.digest)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func validateContent(p *contracts.Protocol, project string, source map[string][]byte) (*Bundle, error) {
	if p == nil || !idPattern.MatchString(project) || len(source) == 0 || len(source) > 1024 {
		return nil, ErrSkill
	}
	if _, ok := p.PackageIdentity(); !ok {
		return nil, ErrSkill
	}
	files := map[string][]byte{}
	total := 0
	for name, raw := range source {
		name = norm.NFC.String(name)
		if _, exists := files[name]; exists || !allowedPath(name) || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 || len(raw) > 1<<20 {
			return nil, ErrSkill
		}
		// Text is data. Normalize line endings once; never expand templates,
		// environment variables, command substitutions or Markdown directives.
		raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		if bytes.Contains(raw, []byte("\r")) {
			return nil, ErrSkill
		}
		switch path.Ext(name) {
		case ".json":
			if _, err := contracts.Decode(raw, 1<<20); err != nil {
				return nil, ErrSkill
			}
		case ".yaml", ".yml":
			if _, err := safeYAML(raw); err != nil {
				return nil, err
			}
		}
		total += len(raw)
		if total > 8<<20 {
			return nil, ErrSkill
		}
		files[name] = bytes.Clone(raw)
	}
	name, description, err := frontmatter(files["SKILL.md"])
	if err != nil {
		return nil, err
	}
	m := manifest{"operator.dev/skill-manifest/v1alpha1", project + ":" + name, name, description, "SKILL.md", []fileEntry{}}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m.Files = append(m.Files, fileEntry{name, mediaType(name), len(files[name]), contracts.RawDigest(files[name])})
	}
	raw, err := canonical(m, contracts.SkillManifestLimit)
	if err != nil {
		return nil, err
	}
	if _, err := p.ValidateSkillManifest(raw); err != nil {
		return nil, ErrSkill
	}
	return &Bundle{manifest: m, raw: raw, files: files, digest: contracts.RawDigest(raw)}, nil
}

func allowedPath(name string) bool {
	if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	parts := strings.Split(name, "/")
	switch strings.ToLower(path.Base(name)) {
	case "mcp.json", "lsp.json", "plugin.json", "package.json", "package-lock.json":
		return false
	}
	if len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return false
		}
		switch strings.ToLower(part) {
		case "scripts", "hooks", "plugins", "agents", "commands", "node_modules", "mcp", "lsp":
			return false
		}
	}
	return mediaType(name) != ""
}
func mediaType(name string) string {
	switch path.Ext(name) {
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	}
	return ""
}
func safeYAML(raw []byte) (*yaml.Node, error) {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	var doc, extra yaml.Node
	if d.Decode(&doc) != nil || len(doc.Content) != 1 || !errors.Is(d.Decode(&extra), io.EOF) {
		return nil, ErrSkill
	}
	nodes := 0
	var visit func(*yaml.Node, int) bool
	visit = func(n *yaml.Node, depth int) bool {
		nodes++
		if depth > 32 || nodes > 16384 || n.Anchor != "" || n.Alias != nil || n.Kind == yaml.AliasNode {
			return false
		}
		switch n.Tag {
		case "!!str", "!!bool", "!!int", "!!float", "!!null", "!!map", "!!seq":
		default:
			return false
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
					return false
				}
				seen[k.Value] = true
			}
		}
		for _, child := range n.Content {
			if !visit(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !visit(doc.Content[0], 0) {
		return nil, ErrSkill
	}
	return doc.Content[0], nil
}
func frontmatter(raw []byte) (string, string, error) {
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return "", "", ErrSkill
	}
	end := bytes.Index(raw[4:], []byte("\n---\n"))
	if end < 0 || end > 8192 || len(bytes.TrimSpace(raw[4+end+5:])) == 0 {
		return "", "", ErrSkill
	}
	node, err := safeYAML(raw[4 : 4+end])
	if err != nil || node.Kind != yaml.MappingNode || len(node.Content) != 4 {
		return "", "", ErrSkill
	}
	fields := map[string]string{}
	for i := 0; i < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if (k.Value != "name" && k.Value != "description") || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return "", "", ErrSkill
		}
		fields[k.Value] = v.Value
	}
	name, description := fields["name"], fields["description"]
	if !idPattern.MatchString(name) || strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 || strings.IndexFunc(description, unicode.IsControl) >= 0 {
		return "", "", ErrSkill
	}
	return name, description, nil
}
func canonical(v any, limit int) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return contracts.Canonicalize(raw, limit)
}
