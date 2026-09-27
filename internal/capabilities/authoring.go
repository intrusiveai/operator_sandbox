package capabilities

// CheckAuthoring validates submission provenance and required references without
// claiming live compatibility or granting access. Optional gaps remain visible;
// Check performs the separate live target and administrator-policy admission.
func CheckAuthoring(b *Bundle, e *Export) ([]Gap, error) {
	if b == nil || e == nil {
		return nil, ErrCompatibility
	}
	t := b.value["target_requirements"].(map[string]any)
	if t["target_id"] != e.TargetID() || t["capability_source_digest"] != e.SourceDigest() {
		return nil, ErrCompatibility
	}
	if d, ok := t["capability_projection_digest"]; ok && d != e.ProjectionDigest() {
		return nil, ErrCompatibility
	}
	index := referenceIndex(e)
	missing := map[string]bool{}
	check := func(refs []string, required bool) error {
		for _, ref := range refs {
			if index[ref] != nil {
				continue
			}
			if required {
				return ErrCompatibility
			}
			missing[ref] = true
		}
		return nil
	}
	if err := check(stringsOf(t["required_capability_refs"]), true); err != nil {
		return nil, err
	}
	for _, group := range []string{"objectives", "scenarios"} {
		for _, item := range records(b.value[group]) {
			if err := check(stringsOf(item["required_capability_refs"]), item["required"].(bool)); err != nil {
				return nil, err
			}
			if group == "scenarios" {
				_ = check(stringsOf(item["guidance"].(map[string]any)["action_refs"]), false)
			}
		}
	}
	_ = check(stringsOf(b.value["evidence"].(map[string]any)["requested_classes"]), false)
	refs := []string{}
	for ref := range missing {
		refs = append(refs, ref)
	}
	gaps := []Gap{}
	for _, ref := range sorted(refs) {
		gaps = append(gaps, Gap{ref, "absent_from_authoring_export"})
	}
	return gaps, nil
}
