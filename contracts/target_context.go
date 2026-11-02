package contracts

// CompactTargetContext is a presentation of verified public data. The complete
// manifest remains authoritative. Returned reference bytes use existing immutable
// input/reference delivery; they do not introduce dispatch authority.
func (p *Protocol) CompactTargetContext(raw []byte) ([]byte, map[string][]byte, error) {
	value, err := p.catalog.Validate(TargetCapabilityManifestSchema, raw, OrdinaryLimit)
	if err != nil {
		return nil, nil, err
	}
	target := value.(map[string]any)
	caps := target["capabilities"].(map[string]any)
	files := map[string][]byte{}
	detail := func(v any) string {
		b, e := canonicalValue(v, OrdinaryLimit)
		if e != nil {
			err = e
			return ""
		}
		id := "target-" + RawDigest(b)[7:]
		files[id] = b
		return id
	}
	copyFields := func(v map[string]any, keys ...string) map[string]any {
		out := map[string]any{}
		for _, key := range keys {
			if item, ok := v[key]; ok {
				out[key] = item
			}
		}
		return out
	}
	result := copyFields(target, "schema_version", "source", "capability_projection_digest")
	out := copyFields(caps, "target_id", "adapter", "features", "feedback_profiles", "evidence_classes", "actions")
	for _, group := range []string{"operations", "services", "file_namespaces"} {
		items := []any{}
		for _, v := range caps[group].([]any) {
			item := v.(map[string]any)
			short := copyFields(item, "ref", "operation_id", "service_id", "namespace_id", "kind", "role", "transports", "injection_surfaces", "delivery_status", "maximum_input_bytes")
			short["details_reference"] = detail(item)
			if group == "operations" {
				if d, ok := item["delivery"].(map[string]any); ok {
					short["delivery"] = copyFields(d, "description", "input")
				}
			}
			if group == "services" {
				for _, sub := range []string{"endpoints", "database_tables", "creatable_collections"} {
					if entries, ok := item[sub].([]any); ok {
						index := []any{}
						for _, entry := range entries {
							obj := entry.(map[string]any)
							mini := copyFields(obj, "id", "kind", "collection")
							mini["details_reference"] = detail(map[string]any{"service_id": item["service_id"], "resource_kind": sub, "resource": obj})
							index = append(index, mini)
						}
						short[sub] = index
					}
				}
			}
			items = append(items, short)
		}
		out[group] = items
	}
	result["capabilities"] = out
	result["details_deferred"] = true
	b, e := canonicalValue(result, OrdinaryLimit)
	if err != nil {
		return nil, nil, err
	}
	return b, files, e
}
