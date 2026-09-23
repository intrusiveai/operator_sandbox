package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/unicode/rangetable"
)

const InputTreeManifestLimit = 8 << 20
const SkillManifestLimit = 2 << 20
const ManifestSetLimit = 40 << 20

var pathRepertoire = rangetable.Assigned("15.0.0")

func normalizedPath(path string) bool {
	if len(path) > 1024 || strings.ContainsAny(path, "\\") || !norm.NFC.IsNormalString(path) {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, r := range path {
		if r < 32 || r == 127 || !unicode.Is(pathRepertoire, r) {
			return false
		}
	}
	return utf8.ValidString(path)
}
func folded(path string) string { return norm.NFC.String(cases.Fold().String(path)) }

// inventoryPaths rejects spelling collisions in files AND their directory
// prefixes, including a path that is both a file and a parent directory.
func inventoryPaths(entries []any) bool {
	previous := ""
	prefixes := map[string]string{}
	files := map[string]bool{}
	for _, value := range entries {
		path := value.(map[string]any)["path"].(string)
		if !normalizedPath(path) || path <= previous {
			return false
		}
		previous = path
		parts := strings.Split(path, "/")
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			key := folded(prefix)
			old, exists := prefixes[key]
			if exists && (old != prefix || files[key] || i == len(parts)-1) {
				return false
			}
			prefixes[key] = prefix
			if i == len(parts)-1 {
				files[key] = true
			}
		}
	}
	return true
}

func (p *Protocol) ValidateInputTree(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(InputTreeManifestSchema, raw, InputTreeManifestLimit)
	if err != nil {
		return nil, err
	}
	m := v.(map[string]any)
	entries := m["entries"].([]any)
	if !inventoryPaths(entries) {
		return nil, ErrProtocol
	}
	ids := map[string]bool{}
	roles := map[string]int{}
	total := int64(0)
	for _, item := range entries {
		e := item.(map[string]any)
		id := e["entry_id"].(string)
		if ids[id] {
			return nil, ErrProtocol
		}
		ids[id] = true
		role := e["role"].(string)
		roles[role]++
		total += number(e["size_bytes"])
		if role == "reference" && e["path"] != "artifacts/sha256-"+strings.TrimPrefix(e["digest"].(string), "sha256:") {
			return nil, ErrProtocol
		}
	}
	if total > 64<<20 || roles["engine-context"] != 1 || roles["scenario-bundle"] != 1 || roles["system-prompt"] != 1 {
		return nil, ErrProtocol
	}
	return m, nil
}
func (p *Protocol) ValidateSkillManifest(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(SkillManifestSchema, raw, SkillManifestLimit)
	if err != nil {
		return nil, err
	}
	m := v.(map[string]any)
	files := m["files"].([]any)
	total := int64(0)
	entrypoint := false
	if !inventoryPaths(files) {
		return nil, ErrProtocol
	}
	for _, item := range files {
		file := item.(map[string]any)
		total += number(file["size_bytes"])
		if file["path"] == "SKILL.md" {
			entrypoint = file["media_type"] == "text/markdown" && number(file["size_bytes"]) > 0
		}
	}
	if !entrypoint || total > 8<<20 {
		return nil, ErrProtocol
	}
	return m, nil
}
func (p *Protocol) ValidateSkillSet(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(SkillSetManifestSchema, raw, ControlLimit)
	if err != nil {
		return nil, err
	}
	m := v.(map[string]any)
	if err := checkSkillSet(m); err != nil {
		return nil, err
	}
	return m, nil
}

func checkSkillSet(m map[string]any) error {
	previous := ""
	seen := map[string]bool{}
	for i, item := range m["skills"].([]any) {
		skill := item.(map[string]any)
		id := skill["skill_id"].(string)
		key := folded(id)
		if id <= previous || seen[key] || number(skill["manifest"].(map[string]any)["slot"]) != int64(i) {
			return ErrProtocol
		}
		previous = id
		seen[key] = true
	}
	return nil
}

// ValidateManifestSet checks inventories and exact per-skill raw descriptors.
// It does not open staged files or verify canonical object/loading digests.
func (p *Protocol) ValidateManifestSet(tree, set []byte, skills [][]byte) error {
	if _, err := p.ValidateInputTree(tree); err != nil {
		return err
	}
	manifest, err := p.ValidateSkillSet(set)
	if err != nil {
		return err
	}
	entries := manifest["skills"].([]any)
	if len(entries) != len(skills) {
		return ErrProtocol
	}
	encoded := len(tree) + len(set)
	content := int64(0)
	for i, raw := range skills {
		encoded += len(raw)
		if encoded > ManifestSetLimit {
			return ErrLimit
		}
		skill, err := p.ValidateSkillManifest(raw)
		if err != nil {
			return err
		}
		entry := entries[i].(map[string]any)
		descriptor := entry["manifest"].(map[string]any)
		hash := sha256.Sum256(raw)
		if !same(entry["skill_id"], skill["skill_id"]) || number(descriptor["size_bytes"]) != int64(len(raw)) || descriptor["digest"] != "sha256:"+hex.EncodeToString(hash[:]) {
			return ErrProtocol
		}
		for _, value := range skill["files"].([]any) {
			content += number(value.(map[string]any)["size_bytes"])
		}
	}
	if content > 64<<20 {
		return ErrProtocol
	}
	return nil
}

// ValidateStartupInputs connects the transcript's raw descriptors to the complete
// manifest set and staged context/prompt descriptors. Reading actual input files,
// canonical object digests and runtime gate enforcement remain caller work.
func (p *Protocol) ValidateStartupInputs(messages [][]byte, tree, set []byte, skills [][]byte) error {
	if err := p.ValidateStartup(messages); err != nil {
		return err
	}
	if err := p.ValidateManifestSet(tree, set, skills); err != nil {
		return err
	}
	init, err := p.ValidateControl("host", messages[2])
	if err != nil {
		return err
	}
	body := init["body"].(map[string]any)
	for key, raw := range map[string][]byte{"input_tree": tree, "skill_set": set} {
		descriptor := body[key].(map[string]any)
		hash := sha256.Sum256(raw)
		if number(descriptor["size_bytes"]) != int64(len(raw)) || descriptor["digest"] != "sha256:"+hex.EncodeToString(hash[:]) {
			return ErrProtocol
		}
	}
	manifest, err := p.ValidateInputTree(tree)
	if err != nil {
		return err
	}
	binding := body["binding"].(map[string]any)
	for _, value := range manifest["entries"].([]any) {
		entry := value.(map[string]any)
		key := ""
		switch entry["role"] {
		case "engine-context":
			key = "engine_context_digest"
		case "system-prompt":
			key = "prompt_digest"
		}
		if key != "" && !same(entry["digest"], binding[key]) {
			return ErrProtocol
		}
	}
	return nil
}
