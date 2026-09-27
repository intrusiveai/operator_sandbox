//go:build linux || darwin

package preparation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

type referenceSet struct {
	entries, bindings, omissions []map[string]any
	contents                     map[string][]byte
}

// Bundle shape/semantics have already passed ParseBundle. Passive references are
// distinct from committed native execution artifacts and carry no delivery receipt.
func prepareReferences(p *contracts.Protocol, bundle []byte, contents map[string][]byte) (*referenceSet, error) {
	value, err := contracts.Decode(bundle, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrPreparation
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrPreparation
	}
	items, ok := object["artifacts"].([]any)
	if !ok || len(contents) > 4000 {
		return nil, ErrPreparation
	}
	out := &referenceSet{entries: []map[string]any{}, bindings: []map[string]any{}, omissions: []map[string]any{}, contents: map[string][]byte{}}
	entries := map[string]map[string]any{}
	total := 0
	for _, item := range items {
		a := item.(map[string]any)
		artifactID := a["artifact_id"].(string)
		if reason, omitted := a["omission_reason"]; omitted {
			out.omissions = append(out.omissions, map[string]any{"artifact_id": artifactID, "reason": reason})
			continue
		}
		digest := a["digest"].(string)
		raw, exists := contents[digest]
		size, _ := a["size_bytes"].(json.Number).Float64()
		if !exists || len(raw) > 1<<20 || len(raw) != int(size) || contracts.RawDigest(raw) != digest {
			return nil, ErrPreparation
		}
		id := "reference-" + strings.TrimPrefix(digest, "sha256:")
		entry := map[string]any{"entry_id": id, "root_kind": "input", "role": "reference", "path": "artifacts/sha256-" + strings.TrimPrefix(digest, "sha256:"), "media_type": a["media_type"], "size_bytes": len(raw), "digest": digest}
		if schema, ok := a["schema_id"].(string); ok {
			if _, err := p.Catalog().Validate(schema, raw, 1<<20); err != nil {
				return nil, ErrPreparation
			}
			entry["schema_id"] = schema
		}
		if old, exists := entries[digest]; exists {
			// One immutable path cannot have conflicting media/schema interpretations.
			if !reflect.DeepEqual(old, entry) {
				return nil, ErrPreparation
			}
		} else {
			total += len(raw)
			if total > 64<<20 {
				return nil, ErrPreparation
			}
			entries[digest] = entry
			out.entries = append(out.entries, entry)
			out.contents["input/"+entry["path"].(string)] = bytes.Clone(raw)
		}
		out.bindings = append(out.bindings, map[string]any{"artifact_id": artifactID, "entry_id": id})
	}
	if len(entries) != len(contents) {
		return nil, ErrPreparation
	}
	sort.Slice(out.entries, func(i, j int) bool { return out.entries[i]["path"].(string) < out.entries[j]["path"].(string) })
	for _, list := range [][]map[string]any{out.bindings, out.omissions} {
		sort.Slice(list, func(i, j int) bool { return list[i]["artifact_id"].(string) < list[j]["artifact_id"].(string) })
	}
	return out, nil
}

// ReferenceContents provides only inventoried passive data for immutable staging.
func (t *Target) ReferenceContents() map[string][]byte {
	out := map[string][]byte{}
	for name, raw := range t.references.contents {
		out[name] = bytes.Clone(raw)
	}
	return out
}
