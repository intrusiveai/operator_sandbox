package capabilities

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

var ErrCompatibility = errors.New("scenario bundle is incompatible with the selected target")
var ErrPolicy = errors.New("invalid installed capability policy")

// Policy is a snapshot supplied by the trusted host TargetProfile/route resolver,
// never by a bundle or harness. Nil AllowedRefs/SelectableActionRefs deny all.
// SelectableActionRefs asserts at least one concrete route passes current policy;
// publication of an action family alone cannot populate it. It is not a selector
// allowlist: every concrete attempt still needs its own typed policy check.
// Excluding an evidence reference also withholds that kind from AllowedKinds.
type Policy struct {
	AllowedRefs          []string `json:"allowed_refs"`
	SelectableActionRefs []string `json:"selectable_action_refs"`
	// Empty ceiling means no additional profile narrowing. Nil kinds means no
	// additional kind narrowing; a nonnil empty slice explicitly withholds all.
	FeedbackCeiling string   `json:"feedback_ceiling,omitempty"`
	FeedbackKinds   []string `json:"feedback_kinds"`
}

type Gap struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}

// Compatibility retains immutable input/policy/binding provenance. It is not a
// campaign admission receipt: artifact staging, limits, journaling and startup
// readiness must still complete before any target effects.
type Compatibility struct {
	record                          []byte
	gaps                            []Gap
	nativeProfile, effectiveProfile string
	allowedKinds                    []string
}

func (c *Compatibility) RecordJSON() []byte       { return bytes.Clone(c.record) }
func (c *Compatibility) Gaps() []Gap              { return append([]Gap{}, c.gaps...) }
func (c *Compatibility) NativeProfile() string    { return c.nativeProfile }
func (c *Compatibility) EffectiveProfile() string { return c.effectiveProfile }
func (c *Compatibility) AllowedKinds() []string   { return append([]string{}, c.allowedKinds...) }

func Check(b *Bundle, authoring *Export, live *Live, policy Policy) (*Compatibility, error) {
	if b == nil || len(b.raw) == 0 || authoring == nil || len(authoring.raw) == 0 || live == nil || live.export == nil {
		return nil, ErrCompatibility
	}
	current := live.export
	target := b.value["target_requirements"].(map[string]any)
	if target["target_id"] != authoring.TargetID() || target["target_id"] != current.TargetID() || target["capability_source_digest"] != authoring.SourceDigest() {
		return nil, ErrCompatibility
	}
	mode := "compatible"
	if pin, ok := target["capability_projection_digest"]; ok {
		if pin != authoring.ProjectionDigest() || pin != current.ProjectionDigest() {
			return nil, ErrCompatibility
		}
		mode = "exact"
	}
	old, index := referenceIndex(authoring), referenceIndex(current)
	// Freeze sorted host decisions. Reject stale/unknown decisions instead of
	// treating a typo as permission for a new capability family.
	policy.AllowedRefs = sorted(policy.AllowedRefs)
	policy.SelectableActionRefs = sorted(policy.SelectableActionRefs)
	for _, ref := range policy.AllowedRefs {
		if index[ref] == nil {
			return nil, ErrPolicy
		}
	}
	for _, ref := range policy.SelectableActionRefs {
		if !strings.HasPrefix(ref, "action:") || index[ref] == nil || !contains(policy.AllowedRefs, ref) {
			return nil, ErrPolicy
		}
	}
	if policy.FeedbackCeiling != "" && profileKinds[policy.FeedbackCeiling] == nil {
		return nil, ErrPolicy
	}
	if !subset(policy.FeedbackKinds, profileKinds["oracle-assisted"]...) {
		return nil, ErrPolicy
	}
	if policy.FeedbackKinds != nil {
		policy.FeedbackKinds = sorted(policy.FeedbackKinds)
	}
	effective := b.value["feedback"].(map[string]any)["requested_profile"].(string)
	for _, p := range []string{live.profile, policy.FeedbackCeiling} {
		if p != "" && profileRank(p) < profileRank(effective) {
			effective = p
		}
	}
	allowedKinds := []string{}
	for _, kind := range profileKinds[effective] {
		if contains(policy.AllowedRefs, "evidence:"+kind) && (policy.FeedbackKinds == nil || contains(policy.FeedbackKinds, kind)) {
			allowedKinds = append(allowedKinds, kind)
		}
	}
	gaps := map[string]string{}
	disabled := map[string][]string{"objectives": {}, "scenarios": {}}
	check := func(ref string, mandatory bool) (bool, error) {
		item := index[ref]
		reason := ""
		switch {
		case old[ref] == nil:
			reason = "absent_from_authoring_export"
		case item == nil:
			reason = "absent_from_live_export"
		case !contains(policy.AllowedRefs, ref):
			reason = "host_policy"
		case strings.HasPrefix(ref, "operation:"):
			if item["delivery_status"] != "described" || item["delivery"] == nil {
				reason = "missing_input_contract"
			} else if !contains(current.native.CrossVMOperations, "application.invoke") || !contains(current.native.CrossVMOperations, "attempt.register") {
				reason = "native_operation_unavailable"
			}
		case strings.HasPrefix(ref, "action:"):
			if !contains(policy.SelectableActionRefs, ref) {
				reason = "no_selectable_route"
			}
		case strings.HasPrefix(ref, "evidence:"):
			if !contains(allowedKinds, item["feedback_kind"].(string)) {
				reason = "effective_feedback_policy"
			}
		}
		if reason == "" {
			return true, nil
		}
		if mandatory {
			return false, fmt.Errorf("%w: %s (%s)", ErrCompatibility, ref, reason)
		}
		gaps[ref] = reason
		return false, nil
	}
	for _, ref := range stringsOf(target["required_capability_refs"]) {
		if _, err := check(ref, true); err != nil {
			return nil, err
		}
	}
	for group, id := range map[string]string{"objectives": "objective_id", "scenarios": "scenario_id"} {
		for _, item := range records(b.value[group]) {
			for _, ref := range stringsOf(item["required_capability_refs"]) {
				ok, err := check(ref, item["required"].(bool))
				if err != nil {
					return nil, err
				}
				if !ok {
					disabled[group] = append(disabled[group], item[id].(string))
				}
			}
		}
	}
	for _, scenario := range records(b.value["scenarios"]) {
		for _, ref := range stringsOf(scenario["guidance"].(map[string]any)["action_refs"]) {
			if _, err := check(ref, false); err != nil {
				return nil, err
			}
		}
	}
	for _, ref := range stringsOf(b.value["evidence"].(map[string]any)["requested_classes"]) {
		if _, err := check(ref, false); err != nil {
			return nil, err
		}
	}
	refs := []string{}
	for ref := range gaps {
		refs = append(refs, ref)
	}
	result := &Compatibility{nativeProfile: live.profile, effectiveProfile: effective, allowedKinds: allowedKinds, gaps: []Gap{}}
	for _, ref := range sorted(refs) {
		result.gaps = append(result.gaps, Gap{ref, gaps[ref]})
	}
	// This host-only record is frozen with campaign inputs. Its hashes refer to
	// exact retained bytes; the authored bundle itself is never rewritten.
	policyRaw, err := json.Marshal(policy)
	if err != nil {
		return nil, ErrPolicy
	}
	policyDigest, err := contracts.CanonicalDigest(policyRaw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, ErrPolicy
	}
	record := map[string]any{
		"api_version": "operator.dev/capability-admission/v1alpha1", "mode": mode, "bundle_digest": contracts.RawDigest(b.raw),
		"target_id": current.TargetID(), "campaign_id": live.campaign, "instance_id": live.instance, "binding": live.binding,
		"authoring": exportPin(authoring), "live": exportPin(current), "policy": policy, "policy_digest": policyDigest,
		"native_profile": live.profile, "effective_profile": effective, "allowed_kinds": allowedKinds, "gaps": result.gaps,
		"disabled_objective_ids": sorted(disabled["objectives"]), "disabled_scenario_ids": sorted(disabled["scenarios"]),
	}
	result.record, err = json.Marshal(record)
	if err != nil {
		return nil, ErrCompatibility
	}
	return result, nil
}
func referenceIndex(e *Export) map[string]map[string]any {
	index := map[string]map[string]any{}
	for _, group := range []string{"operations", "actions", "services", "file_namespaces", "evidence_classes"} {
		for _, item := range e.projection[group].([]any) {
			v := item.(map[string]any)
			index[v["ref"].(string)] = v
		}
	}
	for _, ref := range e.projection["features"].([]string) {
		index[ref] = map[string]any{}
	}
	return index
}
func profileRank(profile string) int {
	switch profile {
	case "black-box":
		return 0
	case "diagnostic":
		return 1
	case "oracle-assisted":
		return 2
	}
	return -1
}
func exportPin(e *Export) map[string]string {
	return map[string]string{"source_digest": e.SourceDigest(), "native_raw_digest": contracts.RawDigest(e.raw), "public_raw_digest": contracts.RawDigest(e.public), "projection_digest": e.ProjectionDigest()}
}
