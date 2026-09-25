// Package capabilities verifies Interceptor provenance and produces the public
// execution description. An Export describes capabilities, never authorization.
package capabilities

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

var (
	ErrNative     = errors.New("invalid or unsupported native capability manifest")
	ErrProjection = errors.New("public capability export does not match verified native source")
)

// Export can only be populated by verification. Its buffers and decoded values
// are private so callers cannot alter provenance after successful verification.
type Export struct {
	native           nativeManifest
	raw, public      []byte
	projection       map[string]any
	projectionDigest string
}

func (e *Export) NativeJSON() []byte       { return bytes.Clone(e.raw) }
func (e *Export) PublicJSON() []byte       { return bytes.Clone(e.public) }
func (e *Export) SourceDigest() string     { return e.native.Digest }
func (e *Export) ProjectionDigest() string { return e.projectionDigest }
func (e *Export) TargetID() string         { return e.projection["target_id"].(string) }
func (e *Export) CompanionName() string {
	return "sha256-" + strings.TrimPrefix(contracts.RawDigest(e.raw), "sha256:") + ".json"
}

// FromNative verifies native serialization and semantics before projecting only
// allowlisted fields. targetID comes from installed configuration, not the bundle.
// catalog must be the installed, trusted offline contract catalog.
func FromNative(catalog *contracts.Catalog, raw []byte, targetID string) (*Export, error) {
	if catalog == nil {
		return nil, contracts.ErrCatalog
	}
	n, source, err := verifyNative(raw)
	if err != nil {
		return nil, err
	}
	p := map[string]any{"target_id": targetID, "adapter": "interceptor/v1", "feedback_profiles": sorted(n.FeedbackProfiles)}
	for _, key := range []string{"operations", "actions", "services", "file_namespaces", "evidence_classes"} {
		p[key] = []any{}
	}
	p["features"] = []string{}
	add := func(key string, item map[string]any) { p[key] = append(p[key].([]any), item) }
	for _, op := range records(source["operations"]) {
		item := map[string]any{"ref": "operation:" + op["id"].(string), "operation_id": op["id"], "delivery_status": "missing-input-contract"}
		if op["delivery"] != nil {
			item["delivery_status"] = "described"
			item["delivery"] = op["delivery"]
		}
		copyFields(item, op, "maximum_input_bytes")
		add("operations", item)
	}
	if contains(n.CrossVMOperations, "injection.arm") && contains(n.CrossVMOperations, "injection.delete") {
		for _, profile := range n.InjectionProfiles {
			add("actions", map[string]any{"ref": "action:injection:" + profile.Surface, "action_type": "interceptor.injection/v1alpha1", "surface": profile.Surface, "scopes": sorted(profile.Scopes), "placements": sorted(profile.Placements), "selector_fields": sorted(profile.SelectorFields), "carriers": sorted(profile.Carriers)})
		}
	}
	for _, svc := range records(source["services"]) {
		item := map[string]any{"ref": "service:" + svc["id"].(string), "service_id": svc["id"], "transports": sorted(stringsOf(svc["transports"])), "injection_surfaces": sorted(stringsOf(svc["injection_surfaces"]))}
		copyFields(item, svc, "kind", "role", "implementation", "response_mode", "mcp", "endpoints", "creatable_collections")
		for key, id := range map[string]string{"endpoints": "id", "creatable_collections": "collection"} {
			if value, ok := item[key]; ok {
				items := records(value)
				sort.Slice(items, func(i, j int) bool { return items[i][id].(string) < items[j][id].(string) })
				item[key] = items
			}
		}
		add("services", item)
	}
	for _, ns := range records(source["file_namespaces"]) {
		item := map[string]any{"ref": "file_namespace:" + ns["id"].(string), "namespace_id": ns["id"]}
		copyFields(item, ns, "allow_create", "max_files", "max_file_bytes")
		add("file_namespaces", item)
	}
	if contains(n.CrossVMOperations, "observation.read") && contains(n.CrossVMOperations, "observation.content.read") && n.Feedback.ViewVersion == "interceptor.dev/observation-view/v1alpha2" {
		for _, kind := range profileKinds["oracle-assisted"] {
			profiles := []string{}
			for _, profile := range sorted(n.FeedbackProfiles) {
				if contains(profileKinds[profile], kind) {
					profiles = append(profiles, profile)
				}
			}
			if contains(n.Feedback.Kinds, kind) && len(profiles) > 0 {
				add("evidence_classes", map[string]any{"ref": "evidence:" + kind, "feedback_kind": kind, "profiles": profiles})
			}
		}
	}
	if n.SnapshotCapable && contains(n.CrossVMOperations, "snapshot.create") {
		p["features"] = []string{"feature:snapshots"}
	}
	for _, key := range []string{"operations", "actions", "services", "file_namespaces", "evidence_classes"} {
		items := p[key].([]any)
		sort.Slice(items, func(i, j int) bool {
			return items[i].(map[string]any)["ref"].(string) < items[j].(map[string]any)["ref"].(string)
		})
	}
	projectionBytes, err := json.Marshal(p)
	if err != nil {
		return nil, ErrProjection
	}
	digest, err := contracts.CanonicalDigest(projectionBytes, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"schema_version": "operator.dev/target-capability-manifest/v1alpha1", "source": map[string]any{"adapter": "interceptor/v1", "schema_version": n.APIVersion, "capability_source_digest": n.Digest, "raw_digest": contracts.RawDigest(raw)}, "capabilities": p, "capability_projection_digest": digest}
	public, err := json.Marshal(result)
	if err != nil {
		return nil, ErrProjection
	}
	if _, err = catalog.Validate(contracts.TargetCapabilityManifestSchema, public, contracts.OrdinaryLimit); err != nil {
		return nil, err
	}
	return &Export{native: n, raw: bytes.Clone(raw), public: public, projection: p, projectionDigest: digest}, nil
}

// Import requires the exact native companion, not just the declared source hash.
// The entire public document must equal its verified, deterministic projection.
func Import(catalog *contracts.Catalog, public, native []byte, targetID string) (*Export, error) {
	if catalog == nil {
		return nil, contracts.ErrCatalog
	}
	if _, err := catalog.Validate(contracts.TargetCapabilityManifestSchema, public, contracts.OrdinaryLimit); err != nil {
		return nil, err
	}
	e, err := FromNative(catalog, native, targetID)
	if err != nil {
		return nil, err
	}
	a, err := contracts.Canonicalize(public, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	b, err := contracts.Canonicalize(e.public, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(a, b) {
		return nil, ErrProjection
	}
	return e, nil
}

var profileKinds = map[string][]string{
	"black-box":       {"target_output"},
	"diagnostic":      {"target_output", "operation_error", "injection_delivery"},
	"oracle-assisted": {"target_output", "operation_error", "injection_delivery", "oracle_outcome"},
}

func contains(items []string, s string) bool {
	for _, v := range items {
		if v == s {
			return true
		}
	}
	return false
}
func sorted(items []string) []string {
	result := append([]string{}, items...)
	sort.Strings(result)
	out := result[:0]
	for _, v := range result {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
func records(value any) []map[string]any {
	out := []map[string]any{}
	if value != nil {
		for _, v := range value.([]any) {
			out = append(out, v.(map[string]any))
		}
	}
	return out
}
func stringsOf(value any) []string {
	out := []string{}
	if value != nil {
		for _, v := range value.([]any) {
			out = append(out, v.(string))
		}
	}
	return out
}
func copyFields(to, from map[string]any, keys ...string) {
	for _, key := range keys {
		if v, ok := from[key]; ok {
			to[key] = v
		}
	}
}
