//go:build linux || darwin

package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// Selection is an immutable, explicitly selected set. The caller supplies the
// loader digest from the admitted image's fixed loader artifact, never a skill.
type Selection struct {
	raw     []byte
	bundles []*Bundle
}

func (s *Selection) Manifest() []byte { return bytes.Clone(s.raw) }
func (s *Selection) Manifests() [][]byte {
	r := make([][]byte, 0, len(s.bundles))
	for _, b := range s.bundles {
		r = append(r, bytes.Clone(b.raw))
	}
	return r
}
func (s *Selection) Contents() map[string][]byte {
	r := map[string][]byte{}
	for _, b := range s.bundles {
		for name, raw := range b.files {
			r["customer-skills/"+b.manifest.SkillID+"/"+name] = bytes.Clone(raw)
		}
	}
	return r
}
func Select(ctx context.Context, p *contracts.Protocol, store, loaderDigest string, digests []string) (*Selection, error) {
	if p == nil || !digestPattern.MatchString(loaderDigest) || len(digests) > 16 {
		return nil, ErrSkill
	}
	if _, ok := p.PackageIdentity(); !ok {
		return nil, ErrSkill
	}
	if len(digests) > 0 {
		lease, err := storeLease(store, false)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
	}
	s := &Selection{bundles: []*Bundle{}}
	seen := map[string]bool{}
	total := 0
	for _, digest := range digests {
		if !digestPattern.MatchString(digest) || seen[digest] {
			return nil, ErrSkill
		}
		seen[digest] = true
		b, err := loadInstalled(ctx, p, store, digest)
		if err != nil {
			return nil, err
		}
		if b.digest != digest {
			return nil, ErrSkill
		}
		total += b.Receipt().ContentBytes
		if total > 64<<20 {
			return nil, ErrSkill
		}
		s.bundles = append(s.bundles, b)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(s.bundles, func(i, j int) bool { return s.bundles[i].manifest.SkillID < s.bundles[j].manifest.SkillID })
	entries := []any{}
	for i, b := range s.bundles {
		entries = append(entries, map[string]any{"skill_id": b.manifest.SkillID, "bundle_digest": b.digest, "manifest": map[string]any{"slot": i, "schema_id": contracts.SkillManifestSchema, "size_bytes": len(b.raw), "digest": b.digest, "object_digest": b.digest}})
	}
	m := map[string]any{"api_version": "operator.dev/skill-set-manifest/v1alpha1", "loader_schema": "operator.dev/instruction-skill-loader/v1alpha1", "loader_digest": loaderDigest, "skills": entries}
	raw, err := canonical(m, contracts.ControlLimit)
	if err != nil {
		return nil, err
	}
	m["loading_digest"] = contracts.RawDigest(raw)
	s.raw, err = canonical(m, contracts.ControlLimit)
	if err != nil {
		return nil, err
	}
	if _, err := p.ValidateSkillSet(s.raw); err != nil {
		return nil, ErrSkill
	}
	return s, nil
}

// Frozen verifies a complete supplied SkillSetManifest against the current
// installation's validated bundles.
func Frozen(ctx context.Context, p *contracts.Protocol, store string, raw []byte) (*Selection, error) {
	if p == nil {
		return nil, ErrSkill
	}
	m, err := p.ValidateSkillSet(raw)
	if err != nil {
		return nil, ErrSkill
	}
	digests := []string{}
	for _, item := range m["skills"].([]any) {
		digests = append(digests, item.(map[string]any)["bundle_digest"].(string))
	}
	selected, err := Select(ctx, p, store, m["loader_digest"].(string), digests)
	if err != nil {
		return nil, err
	}
	canonicalRaw, err := contracts.Canonicalize(raw, contracts.ControlLimit)
	if err != nil || !bytes.Equal(canonicalRaw, selected.Manifest()) {
		return nil, ErrSkill
	}
	return selected, nil
}

func (s *Selection) LoaderDigest() string {
	var m struct {
		Loader string `json:"loader_digest"`
	}
	_ = json.Unmarshal(s.raw, &m)
	return m.Loader
}
