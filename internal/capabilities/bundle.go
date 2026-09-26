package capabilities

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

var ErrBundle = errors.New("invalid scenario bundle semantics")

// Bundle is structurally and semantically checked, but not yet compatible with a
// target. Artifact descriptors are checked here; actual bytes require staging.
type Bundle struct {
	raw   []byte
	value map[string]any
}

func (b *Bundle) JSON() []byte { return bytes.Clone(b.raw) }

func ParseBundle(catalog *contracts.Catalog, raw []byte) (*Bundle, error) {
	if catalog == nil {
		return nil, contracts.ErrCatalog
	}
	value, err := catalog.Validate(contracts.ScenarioBundleSchema, raw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	b := value.(map[string]any)
	for key, value := range b {
		if !boundedText(value, key == "context") {
			return nil, ErrBundle
		}
	}
	indexes := map[string]map[string]map[string]any{}
	for group, id := range map[string]string{"objectives": "objective_id", "scenarios": "scenario_id", "artifacts": "artifact_id"} {
		index := map[string]map[string]any{}
		for _, item := range records(b[group]) {
			key := item[id].(string)
			if _, ok := index[key]; ok {
				return nil, ErrBundle
			}
			index[key] = item
		}
		indexes[group] = index
	}
	coverage := stringsOf(b["coverage"].(map[string]any)["objective_refs"])
	if len(coverage) != len(indexes["objectives"]) {
		return nil, ErrBundle
	}
	for _, id := range coverage {
		if indexes["objectives"][id] == nil {
			return nil, ErrBundle
		}
	}
	for _, group := range []string{"objectives", "scenarios"} {
		for _, item := range records(b[group]) {
			for _, id := range stringsOf(item["artifact_refs"]) {
				a := indexes["artifacts"][id]
				if a == nil {
					return nil, ErrBundle
				}
				if item["required"] == true && a["omission_reason"] != nil {
					return nil, ErrBundle
				}
			}
			if group == "scenarios" {
				for _, id := range stringsOf(item["objective_refs"]) {
					if indexes["objectives"][id] == nil {
						return nil, ErrBundle
					}
				}
			}
		}
	}
	// Reused bytes may have distinct IDs, but their concrete content claims agree.
	digests := map[string]string{}
	for _, a := range records(b["artifacts"]) {
		digest := a["digest"].(string)
		claims := map[string]any{}
		for _, key := range []string{"size_bytes", "media_type", "visibility", "schema_id"} {
			if value, ok := a[key]; ok {
				claims[key] = value
			}
		}
		raw, err := json.Marshal(claims)
		if err != nil {
			return nil, ErrBundle
		}
		claimDigest, err := contracts.CanonicalDigest(raw, contracts.OrdinaryLimit)
		if err != nil {
			return nil, ErrBundle
		}
		if old, ok := digests[digest]; ok && old != claimDigest {
			return nil, ErrBundle
		}
		digests[digest] = claimDigest
	}
	return &Bundle{raw: bytes.Clone(raw), value: b}, nil
}

// Schema string lengths count code points. Narrative limits count UTF-8 bytes;
// only the top-level context gets the larger ceiling.
func boundedText(value any, context bool) bool {
	switch v := value.(type) {
	case string:
		limit := 4096
		if context {
			limit = 16384
		}
		return len(v) <= limit
	case []any:
		for _, child := range v {
			if !boundedText(child, false) {
				return false
			}
		}
	case map[string]any:
		for _, child := range v {
			if !boundedText(child, false) {
				return false
			}
		}
	}
	return true
}
