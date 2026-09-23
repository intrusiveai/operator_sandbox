package contracts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

const PromptLimit = 131072
const EngineContextLimit = 64 << 20

func rawDescriptor(raw []byte) map[string]any {
	hash := sha256.Sum256(raw)
	return map[string]any{"size_bytes": json.Number(strconv.Itoa(len(raw))), "digest": "sha256:" + hex.EncodeToString(hash[:])}
}

// ValidatePrompt accepts exact nonempty UTF-8 text, without normalization.
func ValidatePrompt(raw []byte) error {
	if len(raw) == 0 || len(raw) > PromptLimit || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		return ErrProtocol
	}
	for _, r := range string(raw) {
		// Fixed Unicode White_Space, identical to the Python validator.
		if !(r >= 9 && r <= 13 || r == 32 || r == 0x85 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000) {
			return nil
		}
	}
	return ErrProtocol
}

// ComposePrompt consumes frozen sources and returns exact effective bytes plus
// safe provenance. A nil replacement means absent; no source paths are retained.
func ComposePrompt(mode string, base, replacement []byte, appends [][]byte) ([]byte, map[string]any, error) {
	if mode != "default" && mode != "replacement" && mode != "extension" || len(appends) > 16 || (mode == "replacement") != (replacement != nil) || (mode == "extension") != (len(appends) > 0) {
		return nil, nil, ErrProtocol
	}
	if err := ValidatePrompt(base); err != nil {
		return nil, nil, err
	}
	if replacement != nil {
		if err := ValidatePrompt(replacement); err != nil {
			return nil, nil, err
		}
	}
	descriptors := []any{}
	for _, raw := range appends {
		if err := ValidatePrompt(raw); err != nil {
			return nil, nil, err
		}
		descriptors = append(descriptors, rawDescriptor(raw))
	}
	selected := [][]byte{base}
	if mode == "replacement" {
		selected = [][]byte{replacement}
	} else if mode == "extension" {
		selected = append(selected, appends...)
	}
	size := 2 * (len(selected) - 1)
	for _, raw := range selected {
		size += len(raw)
	}
	if size > PromptLimit {
		return nil, nil, ErrProtocol
	}
	effective := bytes.Join(selected, []byte("\n\n"))
	provenance := map[string]any{"api_version": "operator.dev/prompt-provenance/v1alpha1", "mode": mode, "base": rawDescriptor(base), "appends": descriptors, "effective": rawDescriptor(effective)}
	if replacement != nil {
		provenance["replacement"] = rawDescriptor(replacement)
	}
	return effective, provenance, nil
}

func checkProvenance(p map[string]any) error {
	effective := p["effective"].(map[string]any)
	switch p["mode"] {
	case "default":
		if !wireEqual(effective, p["base"]) {
			return ErrProtocol
		}
	case "replacement":
		if !wireEqual(effective, p["replacement"]) {
			return ErrProtocol
		}
	case "extension":
		size := number(p["base"].(map[string]any)["size_bytes"])
		for _, d := range p["appends"].([]any) {
			size += number(d.(map[string]any)["size_bytes"]) + 2
		}
		if number(effective["size_bytes"]) != size {
			return ErrProtocol
		}
	}
	return nil
}

func (p *Protocol) ValidatePromptProvenance(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(PromptProvenanceSchema, raw, ControlLimit)
	if err != nil {
		return nil, err
	}
	result := v.(map[string]any)
	if err := checkProvenance(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Protocol) checkAdmission(body map[string]any) error {
	previous := ""
	for _, name := range body["operations"].([]any) {
		s := name.(string)
		if s <= previous {
			return ErrProtocol
		}
		if _, ok := p.operations[s]; !ok {
			return ErrProtocol
		}
		previous = s
	}
	limits := body["limits"].(map[string]any)
	campaign, harness := limits["campaign"].(map[string]any), limits["harness"].(map[string]any)
	for key, value := range body["remaining_limits"].(map[string]any) {
		if number(value) > number(campaign[key]) {
			return ErrProtocol
		}
	}
	if number(campaign["model_turns"]) > number(harness["max_model_turns"]) || number(campaign["observation_bytes"]) > number(harness["max_read_bytes"]) {
		return ErrProtocol
	}
	return nil
}

func profileRank(profile any) int {
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

func (p *Protocol) ValidateEngineContext(raw []byte) (map[string]any, error) {
	v, err := p.catalog.Validate(EngineContextSchema, raw, EngineContextLimit)
	if err != nil {
		return nil, err
	}
	c := v.(map[string]any)
	prompt := c["prompt"].(map[string]any)
	provenance := prompt["provenance"].(map[string]any)
	if err := checkProvenance(provenance); err != nil {
		return nil, err
	}
	if err := p.checkAdmission(c); err != nil {
		return nil, err
	}
	refs := c["references"].([]any)
	if !inventoryPaths(refs) {
		return nil, ErrProtocol
	}
	scenario := c["scenario_bundle"].(map[string]any)
	ids := map[any]bool{scenario["entry_id"]: true, prompt["entry_id"]: true}
	if len(ids) != 2 {
		return nil, ErrProtocol
	}
	refIDs, used, bound := map[any]bool{}, map[any]bool{}, map[any]bool{}
	total := int64(len(raw)) + number(scenario["size_bytes"]) + number(provenance["effective"].(map[string]any)["size_bytes"])
	for _, value := range refs {
		e := value.(map[string]any)
		id := e["entry_id"]
		if ids[id] || e["path"] != "artifacts/sha256-"+e["digest"].(string)[7:] {
			return nil, ErrProtocol
		}
		ids[id], refIDs[id] = true, true
		total += number(e["size_bytes"])
	}
	if total > 64<<20 {
		return nil, ErrProtocol
	}
	for _, key := range []string{"artifact_bindings", "omissions"} {
		previous := ""
		for _, value := range c[key].([]any) {
			a := value.(map[string]any)
			id := a["artifact_id"].(string)
			if id <= previous {
				return nil, ErrProtocol
			}
			previous = id
			if key == "artifact_bindings" {
				if !refIDs[a["entry_id"]] {
					return nil, ErrProtocol
				}
				used[a["entry_id"]], bound[id] = true, true
			} else if bound[id] {
				return nil, ErrProtocol
			}
		}
	}
	if len(used) != len(refIDs) {
		return nil, ErrProtocol
	}
	if err := checkSkillSet(c["skills"].(map[string]any)); err != nil {
		return nil, err
	}
	feedback := c["feedback"].(map[string]any)
	kinds := []string{"target_output", "operation_error", "injection_delivery", "oracle_outcome"}
	maximum := []int{1, 3, 4}[profileRank(feedback["profile"])]
	index := 0
	for _, value := range feedback["allowed_kinds"].([]any) {
		for index < maximum && kinds[index] != value {
			index++
		}
		if index == maximum {
			return nil, ErrProtocol
		}
		index++
	}
	target := c["target"].(map[string]any)["capabilities"].(map[string]any)
	for _, group := range []string{"operations", "actions", "services", "file_namespaces", "evidence_classes"} {
		previous := ""
		for _, value := range target[group].([]any) {
			item := value.(map[string]any)
			ref := item["ref"].(string)
			if ref <= previous {
				return nil, ErrProtocol
			}
			previous = ref
			if group == "operations" && (ref != "operation:"+item["operation_id"].(string)) {
				return nil, ErrProtocol
			}
		}
	}
	if !wireEqual(c["model"].(map[string]any)["features"], []any{"text", "function-tools"}) {
		return nil, ErrProtocol
	}
	return c, nil
}

// ValidateLaunchContent links actual supplied context, bundle and prompt bytes to
// startup. It does not open reference/skill files or verify canonical identities.
func (p *Protocol) ValidateLaunchContent(messages [][]byte, tree, set []byte, skills [][]byte, context, bundle, prompt []byte) error {
	if err := p.ValidateStartupInputs(messages, tree, set, skills); err != nil {
		return err
	}
	c, err := p.ValidateEngineContext(context)
	if err != nil {
		return err
	}
	value, err := p.catalog.Validate(ScenarioBundleSchema, bundle, 4<<20)
	if err != nil {
		return err
	}
	b := value.(map[string]any)
	if err := ValidatePrompt(prompt); err != nil {
		return err
	}
	bootstrap, _ := p.ValidateControl("host", messages[0])
	admission, _ := p.ValidateControl("host", messages[4])
	body := admission["body"].(map[string]any)
	for _, key := range []string{"campaign_id", "launch_id", "run_revision"} {
		if !same(c[key], bootstrap[key]) {
			return ErrProtocol
		}
	}
	for _, key := range []string{"contract", "release"} {
		if !wireEqual(c[key], bootstrap["body"].(map[string]any)[key]) {
			return ErrProtocol
		}
	}
	for _, key := range []string{"operations", "limits"} {
		if !wireEqual(c[key], body[key]) {
			return ErrProtocol
		}
	}
	for key, value := range body["remaining_limits"].(map[string]any) {
		if number(value) > number(c["remaining_limits"].(map[string]any)[key]) {
			return ErrProtocol
		}
	}
	skillSet, _ := p.ValidateSkillSet(set)
	if !wireEqual(c["skills"], skillSet) {
		return ErrProtocol
	}
	manifest, _ := p.ValidateInputTree(tree)
	core := map[string]map[string]any{}
	references := []any{}
	for _, value := range manifest["entries"].([]any) {
		e := value.(map[string]any)
		role := e["role"].(string)
		if role == "reference" {
			references = append(references, e)
		} else {
			core[role] = e
		}
	}
	for role, raw := range map[string][]byte{"engine-context": context, "scenario-bundle": bundle, "system-prompt": prompt} {
		for key, value := range rawDescriptor(raw) {
			if !same(core[role][key], value) {
				return ErrProtocol
			}
		}
	}
	cp := c["prompt"].(map[string]any)
	if !wireEqual(c["scenario_bundle"], core["scenario-bundle"]) || !same(cp["entry_id"], core["system-prompt"]["entry_id"]) || !wireEqual(cp["provenance"].(map[string]any)["effective"], rawDescriptor(prompt)) || !wireEqual(c["references"], references) {
		return ErrProtocol
	}
	target := c["target"].(map[string]any)["capabilities"].(map[string]any)
	if !same(b["target_requirements"].(map[string]any)["target_id"], target["target_id"]) || profileRank(c["feedback"].(map[string]any)["profile"]) > profileRank(b["feedback"].(map[string]any)["requested_profile"]) {
		return ErrProtocol
	}
	bindings, omissions, refs := map[any]any{}, map[any]any{}, map[any]map[string]any{}
	for _, v := range c["artifact_bindings"].([]any) {
		a := v.(map[string]any)
		bindings[a["artifact_id"]] = a["entry_id"]
	}
	for _, v := range c["omissions"].([]any) {
		a := v.(map[string]any)
		omissions[a["artifact_id"]] = a["reason"]
	}
	for _, v := range references {
		e := v.(map[string]any)
		refs[e["entry_id"]] = e
	}
	artifacts := b["artifacts"].([]any)
	seen := map[any]bool{}
	for _, group := range []string{"objectives", "scenarios"} {
		for _, value := range b[group].([]any) {
			item := value.(map[string]any)
			if item["required"] == true {
				if ids, ok := item["artifact_refs"].([]any); ok {
					for _, id := range ids {
						if _, included := bindings[id]; !included {
							return ErrProtocol
						}
					}
				}
			}
		}
	}
	if len(bindings)+len(omissions) != len(artifacts) {
		return ErrProtocol
	}
	for _, v := range artifacts {
		a := v.(map[string]any)
		id := a["artifact_id"]
		if seen[id] {
			return ErrProtocol
		}
		seen[id] = true
		if reason, ok := omissions[id]; ok {
			if a["required"] != false || a["omission_reason"] != reason {
				return ErrProtocol
			}
		} else {
			entryID, ok := bindings[id]
			if !ok {
				return ErrProtocol
			}
			if _, ok := a["omission_reason"]; ok {
				return ErrProtocol
			}
			for _, key := range []string{"digest", "size_bytes", "media_type", "schema_id"} {
				if !same(refs[entryID][key], a[key]) {
					return ErrProtocol
				}
			}
		}
	}
	return nil
}
