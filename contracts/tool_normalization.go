package contracts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ToolResource is trusted resolution data from Harness's verified runtime state.
// It is never model input and never replaces independent host authorization.
type ToolResource struct {
	Kind       string         `json:"kind"`
	CampaignID string         `json:"campaign_id"`
	Value      map[string]any `json:"value"`
	Available  bool           `json:"available"`
	Inspected  bool           `json:"inspected"`
	Live       bool           `json:"live"`
}
type ToolResolution struct {
	CampaignID   string                  `json:"campaign_id"`
	CallID       string                  `json:"call_id"`
	Resources    map[string]ToolResource `json:"resources"`
	DatabaseKeys map[string][]string     `json:"database_keys,omitempty"`
}
type ToolNormalization struct {
	Arguments             map[string]any    `json:"arguments"`
	Paths                 map[string]string `json:"paths"`
	ObservationSelections []map[string]any  `json:"observation_selections"`
}

// RemapToolPath preserves unknown host paths and uses the longest boundary match.
func RemapToolPath(paths map[string]string, path string) string {
	best := ""
	found := false
	for k := range paths {
		if (path == k || strings.HasPrefix(path, k+"/")) && (!found || len(k) > len(best)) {
			best = k
			found = true
		}
	}
	if found {
		return paths[best] + path[len(best):]
	}
	return path
}
func modelContent(a map[string]any, path string) (map[string]any, error) {
	keys := []string{}
	for _, k := range []string{"json", "text", "base64", "artifact"} {
		if _, ok := a[k]; ok {
			keys = append(keys, k)
		}
	}
	if len(keys) != 1 {
		return nil, argumentIssue("content_choice", path, "Supply exactly one of json, text, base64 or a returned artifact handle.")
	}
	k := keys[0]
	media, hasMedia := a["media_type"]
	switch k {
	case "artifact":
		if hasMedia {
			return nil, argumentIssue("artifact_media_override", path+"/media_type", "Artifact media type comes from its committed descriptor; omit media_type.")
		}
		return map[string]any{"artifact_receipt": a[k]}, nil
	case "json":
		if hasMedia && media != "application/json" {
			return nil, argumentIssue("invalid_media_type", path+"/media_type", "JSON requires application/json; omit media_type or use that value.")
		}
		return map[string]any{"encoding": "json", "value": a[k]}, nil
	case "base64":
		if !hasMedia {
			return nil, argumentIssue("media_type_required", path+"/media_type", "Supply the media type of the decoded base64 bytes.")
		}
		return map[string]any{"encoding": "base64", "data": a[k], "media_type": media}, nil
	default:
		if !hasMedia {
			media = "text/plain"
		}
		return map[string]any{"encoding": "utf8", "text": a[k], "media_type": media}, nil
	}
}
func (p *Protocol) checkModelContent(a map[string]any, path string) error {
	body, err := modelContent(a, path)
	if err != nil {
		return err
	}
	if _, ok := body["artifact_receipt"]; !ok {
		if _, err = p.ResolveDirectInput(body); err != nil {
			var ae *ArgumentError
			if errors.As(err, &ae) {
				field := ae.FieldPath
				if field == "/value" {
					field = "/json"
				}
				if field == "/data" {
					field = "/base64"
				}
				return argumentIssue(ae.Reason, path+field, ae.Correction)
			}
			return err
		}
	}
	if injection, ok := a["injection"].(map[string]any); ok {
		return p.checkModelContent(injection, "/injection")
	}
	return nil
}

type toolNormalizer struct {
	ctx        ToolResolution
	paths      map[string]string
	selections []map[string]any
	locals     map[string][2]string
	err        error
}

func (n *toolNormalizer) fail(reason, path, correction string) {
	if n.err == nil {
		if reason == "invalid_resolution_context" {
			correction = "Internal resource resolution failed. Stop this operation and report the product error; do not supply internal identities or replay completed actions."
		}
		n.err = argumentIssue(reason, path, correction)
	}
}
func textField(a map[string]any, k string) string { s, _ := a[k].(string); return s }
func listField(a map[string]any, k string) []any {
	v, _ := a[k].([]any)
	if v == nil {
		return []any{}
	}
	return v
}
func copyFields(a map[string]any, keys ...string) map[string]any {
	o := map[string]any{}
	for _, k := range keys {
		if v, ok := a[k]; ok {
			o[k] = v
		}
	}
	return o
}
func (n *toolNormalizer) identity(path string) string {
	sum := sha256.Sum256([]byte(n.ctx.CampaignID + "\n" + n.ctx.CallID + "\n" + path))
	return fmt.Sprintf("generated-%x", sum[:16])
}
func (n *toolNormalizer) resource(h any, kind, path string, observed, live bool) map[string]any {
	handle, _ := h.(string)
	r, ok := n.ctx.Resources[handle]
	if !ok {
		n.fail("unknown_handle", path, "Use a handle returned by a successful tool or verified campaign input; never invent one.")
		return map[string]any{}
	}
	if r.Kind != kind {
		n.fail("wrong_handle_kind", path, "Use a returned "+kind+" handle; other resource categories cannot substitute for it.")
	}
	if r.CampaignID != n.ctx.CampaignID {
		n.fail("foreign_handle", path, "Select a resource from this campaign.")
	}
	if live && !r.Live {
		n.fail("stale_handle", path, "Refresh the resource in the current target binding; do not replay prior effects.")
	}
	if observed && (!r.Available || !r.Inspected) {
		n.fail("evidence_not_inspected", path, n.inspectionCorrection(r))
	}
	if kind == "artifact" && !r.Available {
		n.fail("unavailable_artifact", path, "Use an available committed artifact handle.")
	}
	if r.Value == nil {
		n.fail("invalid_resolution_context", path, "Supply the verified canonical resource fields.")
		return map[string]any{}
	}
	// Resolution data is trusted but malformed bookkeeping must fail closed.
	required := map[string][]string{"execution": {"attempt_receipt_id"}, "observation": {"attempt_receipt_id", "entry_id"}, "artifact": {"artifact_receipt", "purpose"}, "record": {"record_receipt_id"}, "hypothesis": {"hypothesis_id"}, "uncertainty": {"gap_id"}, "reference": {"reference_id", "entry_id"}, "injection": {"attempt_receipt_id", "action_id"}, "checkpoint": {"source_session", "checkpoint_id"}, "session": {"source_session"}}
	for _, key := range required[kind] {
		v := textField(r.Value, key)
		if !validModelIdentity(v) || len(v) == 0 || len(v) > 128 {
			n.fail("invalid_resolution_context", path, "Supply valid canonical resource identifiers.")
		}
	}
	if extent, ok := r.Value["range"]; ok {
		m, ok := extent.(map[string]any)
		if !ok || len(m) != 2 {
			n.fail("invalid_resolution_context", path, "Supply a verified bounded byte range.")
		} else {
			offset, ok1 := m["offset"].(json.Number)
			length, ok2 := m["length"].(json.Number)
			if !ok1 || !ok2 {
				n.fail("invalid_resolution_context", path, "Supply a verified bounded byte range.")
			} else {
				a, e1 := offset.Int64()
				b, e2 := length.Int64()
				if e1 != nil || e2 != nil || a < 0 || b < 1 || a > MaxSafeInteger-b {
					n.fail("invalid_resolution_context", path, "Supply a verified bounded byte range.")
				}
			}
		}
	}
	return r.Value
}
func (n *toolNormalizer) ref(h any, kind, path string) string {
	handle, _ := h.(string)
	if local, ok := n.locals[handle]; ok && (kind == "hypothesis" || kind == "uncertainty") {
		if local[0] != kind {
			n.fail("wrong_handle_kind", path, "Select a "+kind+" label.")
		}
		return local[1]
	}
	if kind == "uncertainty" {
		n.fail("unknown_uncertainty_label", path, "Reference a label declared in top-level uncertainties. Put explanatory prose in that entry's description; do not replay completed attempts.")
		return ""
	}
	r := n.resource(h, kind, path, false, false)
	key := map[string]string{"execution": "attempt_receipt_id", "record": "record_receipt_id", "hypothesis": "hypothesis_id", "uncertainty": "gap_id"}[kind]
	v, ok := r[key].(string)
	if !ok {
		n.fail("invalid_resolution_context", path, "Supply the canonical resource identifier.")
	}
	return v
}
func containsValue(a []any, v any) bool {
	b, _ := json.Marshal(v)
	for _, x := range a {
		c, _ := json.Marshal(x)
		if string(b) == string(c) {
			return true
		}
	}
	return false
}
func (n *toolNormalizer) refs(a map[string]any, field, kind, mp, hp, outfield string) []any {
	out := []any{}
	for i, h := range listField(a, field) {
		path := fmt.Sprintf("%s/%s/%d", mp, field, i)
		v := n.ref(h, kind, path)
		if !containsValue(out, v) {
			n.paths[fmt.Sprintf("%s/%s/%d", hp, outfield, len(out))] = path
			out = append(out, v)
		}
	}
	return out
}
func (n *toolNormalizer) observations(a map[string]any, mp, hp string, claim, hasField bool) ([]any, []any) {
	refs, receipts := []any{}, []any{}
	for i, h := range listField(a, "observations") {
		path := fmt.Sprintf("%s/observations/%d", mp, i)
		r := n.resource(h, "observation", path, claim, false)
		ref := copyFields(r, "attempt_receipt_id", "entry_id", "range")
		if r["attempt_receipt_id"] == nil || r["entry_id"] == nil {
			n.fail("invalid_resolution_context", path, "Supply the observation receipt and entry.")
		}
		if !containsValue(refs, ref) {
			n.paths[fmt.Sprintf("%s/observation_refs/%d", hp, len(refs))] = path
			refs = append(refs, ref)
		}
		if !containsValue(receipts, r["attempt_receipt_id"]) {
			n.paths[fmt.Sprintf("%s/attempt_receipt_refs/%d", hp, len(receipts))] = path
			receipts = append(receipts, r["attempt_receipt_id"])
		}
		if !hasField {
			n.selections = append(n.selections, map[string]any{"field_path": path, "observation_ref": ref})
		}
	}
	for i, h := range listField(a, "attempts") {
		path := fmt.Sprintf("%s/attempts/%d", mp, i)
		v := n.ref(h, "execution", path)
		if !containsValue(receipts, v) {
			n.paths[fmt.Sprintf("%s/attempt_receipt_refs/%d", hp, len(receipts))] = path
			receipts = append(receipts, v)
		}
	}
	if len(refs) > 32 || len(receipts) > 32 {
		n.fail("reference_limit", mp, "Select at most 32 distinct observations and execution receipts per item.")
	}
	return refs, receipts
}
func (n *toolNormalizer) content(a map[string]any, mp, hp string) map[string]any {
	for _, key := range []string{"json", "text", "base64", "artifact"} {
		if _, ok := a[key]; ok {
			n.paths[hp] = mp + "/" + key
			break
		}
	}
	body, err := modelContent(a, mp)
	if err != nil {
		if n.err == nil {
			n.err = err
		}
		return map[string]any{}
	}
	if h, ok := a["artifact"]; ok {
		r := n.resource(h, "artifact", mp+"/artifact", false, false)
		if r["purpose"] != "payload" && !(hp == "/input" && r["purpose"] == "carrier") {
			allowed := "payload"
			if hp == "/input" {
				allowed = "payload, carrier"
			}
			n.fail("artifact_purpose", mp+"/artifact", "Select an artifact with the required input purpose: "+allowed+".")
		}
		body = map[string]any{"artifact_receipt": r["artifact_receipt"]}
		n.paths[hp+"/artifact_receipt"] = mp + "/artifact"
	} else {
		for _, pair := range [][2]string{{"json", "value"}, {"text", "text"}, {"base64", "data"}, {"media_type", "media_type"}} {
			if _, ok := a[pair[0]]; ok {
				n.paths[hp+"/"+pair[1]] = mp + "/" + pair[0]
			}
		}
	}
	return body
}
func (n *toolNormalizer) provenance(a map[string]any, mp, hp string) map[string]any {
	if err := ValidateAttemptOrigin(a, nil); err != nil && n.err == nil {
		n.err = err
	}
	out := copyFields(a, "origin", "scenario_id")
	for k := range out {
		n.paths[hp+"/"+k] = mp + "/" + k
	}
	if h, ok := a["parent"]; ok {
		out["parent_hypothesis_id"] = n.ref(h, "hypothesis", mp+"/parent")
		n.paths[hp+"/parent_hypothesis_id"] = mp + "/parent"
	}
	return out
}
func (n *toolNormalizer) entry(kind string, a map[string]any, mp, hp string) map[string]any {
	fields := map[string][]string{"objectives": {"objective_id", "outcome", "summary"}, "claims": {"statement", "interpretation", "assurance", "confidence"}, "hypotheses": {"outcome", "summary", "objective_refs"}, "coverage": {"objective_id", "status", "reason", "alternative"}, "uncertainties": {"kind", "description"}}
	out := copyFields(a, fields[kind]...)
	n.paths[hp] = mp
	for k := range out {
		n.paths[hp+"/"+k] = mp + "/" + k
	}
	obs, receipts := n.observations(a, mp, hp, kind != "uncertainties", kind == "claims" || kind == "uncertainties")
	if kind != "uncertainties" {
		out["attempt_receipt_refs"] = receipts
	}
	if kind == "claims" || kind == "uncertainties" {
		out["observation_refs"] = obs
	}
	if kind == "objectives" || kind == "hypotheses" || kind == "uncertainties" {
		out["record_refs"] = n.refs(a, "records", "record", mp, hp, "record_refs")
	}
	switch kind {
	case "claims":
		out["claim_id"] = n.identity(mp)
		out["uncertainty_refs"] = n.refs(a, "uncertainties", "uncertainty", mp, hp, "uncertainty_refs")
		if a["interpretation"] == "supported" && len(obs) == 0 {
			n.fail("evidence_required", mp+"/observations", "Supported claims require inspected observation handles.")
		}
	case "hypotheses":
		out["hypothesis_id"] = n.identity(mp)
		out["objective_refs"] = listField(a, "objective_refs")
		out["provenance"] = n.provenance(a, mp, hp+"/provenance")
	case "coverage":
		if h, ok := a["hypothesis"]; ok {
			out["hypothesis_id"] = n.ref(h, "hypothesis", mp+"/hypothesis")
			n.paths[hp+"/hypothesis_id"] = mp + "/hypothesis"
		}
		if a["status"] == "tested" && len(receipts) == 0 {
			n.fail("evidence_required", mp+"/attempts", "Tested coverage requires executed attempts.")
		}
	case "uncertainties":
		out["gap_id"] = n.identity(mp)
		inputs := []any{}
		for i, h := range listField(a, "inputs") {
			path := fmt.Sprintf("%s/inputs/%d", mp, i)
			r := n.resource(h, "reference", path, false, false)
			inputs = append(inputs, copyFields(r, "entry_id", "range"))
			n.paths[fmt.Sprintf("%s/input_refs/%d", hp, i)] = path
		}
		out["input_refs"] = inputs
	}
	if (kind == "objectives" || kind == "hypotheses") && a["outcome"] == "supported" && len(receipts) == 0 {
		n.fail("evidence_required", mp+"/attempts", "Supported outcomes require execution evidence; otherwise report untested or inconclusive.")
	}
	return out
}
func (n *toolNormalizer) normalize(name string, a map[string]any) map[string]any {
	switch name {
	case "attempt_execute":
		out := copyFields(a, "origin", "operation_id", "scenario_id", "caller_principal_id", "rationale", "strategy_provenance_ref", "technique_refs")
		out["input"] = n.content(a, "", "/input")
		if h, ok := a["parent"]; ok {
			out["parent_attempt_receipt_id"] = n.ref(h, "execution", "/parent")
			n.paths["/parent_attempt_receipt_id"] = "/parent"
		}
		out["observation_selection"] = map[string]any{"mode": "all-permitted"}
		if kinds, ok := a["observations"]; ok {
			out["observation_selection"] = map[string]any{"mode": "selected", "kinds": kinds}
		}
		n.paths["/observation_selection"] = "/observations"
		n.paths["/observation_selection/kinds"] = "/observations"
		actions := []any{}
		for i, v := range listField(a, "setup") {
			action := v.(map[string]any)
			mp := fmt.Sprintf("/setup/%d", i)
			hp := fmt.Sprintf("/pre_actions/%d/parameters", i)
			n.paths[hp] = mp
			selector := action["selector"].(map[string]any)
			if action["surface"] == "environment_state" && selector["key"] != nil {
				key := selector["key"].(map[string]any)
				cols, ok := n.ctx.DatabaseKeys[textField(selector, "service")+"/"+textField(selector, "collection")]
				valid := ok && len(key) == len(cols)
				for _, col := range cols {
					value, exists := key[col]
					if !exists || value == nil {
						valid = false
					}
					switch value.(type) {
					case map[string]any, []any:
						valid = false
					}
				}
				if !valid {
					n.fail("invalid_database_key", mp+"/selector/key", "Supply exactly the selected database policy key columns with non-null scalar values.")
				}
				raw, err := canonicalValue(key, OrdinaryLimit)
				if err != nil && n.err == nil {
					n.err = err
				}
				selector["key"] = string(raw)
			}
			actions = append(actions, map[string]any{"action_id": n.identity(mp), "action_type": "interceptor.injection/v1alpha1", "parameters": action})
		}
		out["pre_actions"] = actions
		if len(actions) > 0 {
			input := n.content(a["injection"].(map[string]any), "/injection", "/injection_input")
			out["injection_input"] = input
			out["retain_injections"] = false
			if v, ok := a["retain_injections"]; ok {
				out["retain_injections"] = v
			}
			if _, ok := input["artifact_receipt"]; !ok && input["encoding"] != "json" {
				n.fail("injection_json_required", "/injection", "Use canonical JSON injection content or a committed JSON artifact.")
			}
		}
		return out
	case "finish":
		out := map[string]any{"finish_reason": a["finish_reason"]}
		if h, ok := a["artifact"]; ok {
			r := n.resource(h, "artifact", "/artifact", false, false)
			if r["purpose"] != "conclusion" {
				n.fail("artifact_purpose", "/artifact", "Select a committed conclusion artifact.")
			}
			out["artifact_receipt"] = r["artifact_receipt"]
			n.paths["/artifact_receipt"] = "/artifact"
			return out
		}
		if v, ok := a["unavailable_reason"]; ok {
			out["unavailable"] = map[string]any{"reason": v}
			n.paths["/unavailable/reason"] = "/unavailable_reason"
			return out
		}
		for _, pair := range [][2]string{{"hypotheses", "hypothesis"}, {"uncertainties", "uncertainty"}} {
			for i, v := range listField(a, pair[0]) {
				item := v.(map[string]any)
				if label, ok := item["label"].(string); ok {
					path := fmt.Sprintf("/%s/%d", pair[0], i)
					_, local := n.locals[label]
					_, existing := n.ctx.Resources[label]
					if local || existing {
						n.fail("ambiguous_label", path+"/label", "Use a unique local label distinct from all existing resource handles.")
					}
					n.locals[label] = [2]string{pair[1], n.identity(path)}
				}
			}
		}
		assessment := map[string]any{"summary": a["summary"], "status": "partial", "record_refs": n.refs(a, "records", "record", "", "/assessment", "record_refs")}
		if v, ok := a["status"]; ok {
			assessment["status"] = v
		}
		n.paths["/assessment"] = ""
		n.paths["/assessment/record_refs"] = "/records"
		for _, coll := range []string{"objectives", "hypotheses", "claims", "coverage", "uncertainties"} {
			entries := []any{}
			for i, v := range listField(a, coll) {
				mp := fmt.Sprintf("/%s/%d", coll, i)
				entries = append(entries, n.entry(coll, v.(map[string]any), mp, "/assessment"+mp))
			}
			assessment[coll] = entries
		}
		parents := map[string]string{}
		for _, v := range assessment["hypotheses"].([]any) {
			h := v.(map[string]any)
			parents[textField(h, "hypothesis_id")] = textField(h["provenance"].(map[string]any), "parent_hypothesis_id")
		}
		for i, v := range assessment["hypotheses"].([]any) {
			key := textField(v.(map[string]any), "hypothesis_id")
			seen := map[string]bool{}
			for {
				parent, ok := parents[key]
				if !ok {
					break
				}
				if seen[key] {
					n.fail("hypothesis_cycle", fmt.Sprintf("/hypotheses/%d/parent", i), "Remove self-parenting or the hypothesis parent cycle.")
					break
				}
				seen[key] = true
				key = parent
			}
		}
		out["assessment"] = assessment
		return out
	case "record_append":
		kind := textField(a, "kind")
		n.paths["/record"] = ""
		n.paths["/record_kind"] = "/kind"
		r := map[string]any{"record_refs": n.refs(a, "records", "record", "", "/record", "record_refs")}
		switch kind {
		case "hypothesis":
			r["hypothesis_id"] = n.identity("/hypothesis")
			r["provenance"] = n.provenance(a, "", "/record/provenance")
			r["statement"] = a["statement"]
			for _, k := range []string{"objective_refs", "assumptions", "predicted_observations"} {
				r[k] = listField(a, k)
			}
			if v, ok := a["surface_ref"]; ok {
				r["surface_ref"] = v
			}
		case "progress":
			r["summary"] = a["summary"]
			r["objective_refs"] = listField(a, "objective_refs")
			r["hypothesis_refs"] = n.refs(a, "hypotheses", "hypothesis", "", "/record", "hypothesis_refs")
			r["observation_refs"], r["attempt_receipt_refs"] = n.observations(a, "", "/record", true, true)
			if v, ok := a["decision"]; ok {
				r["decision"] = v
			}
			if _, ok := a["interpretation"]; ok {
				assessment := copyFields(a, "interpretation", "confidence", "assurance")
				assessment["summary"] = a["assessment_summary"]
				assessment["gaps"] = listField(a, "gaps")
				r["assessment"] = assessment
				for k := range assessment {
					model := k
					if k == "summary" {
						model = "assessment_summary"
					}
					n.paths["/record/assessment/"+k] = "/" + model
				}
				if a["interpretation"] == "supported" && len(r["observation_refs"].([]any)) == 0 {
					n.fail("evidence_required", "/observations", "Supported progress requires inspected observation handles.")
				}
			}
		case "lineage":
			r["hypothesis_id"] = n.ref(a["hypothesis"], "hypothesis", "/hypothesis")
			r["attempt_receipt_id"] = n.ref(a["attempt"], "execution", "/attempt")
			r["mutation_summary"] = a["mutation_summary"]
			n.paths["/record/hypothesis_id"] = "/hypothesis"
			n.paths["/record/attempt_receipt_id"] = "/attempt"
			if h, ok := a["parent"]; ok {
				r["parent_attempt_receipt_id"] = n.ref(h, "execution", "/parent")
				n.paths["/record/parent_attempt_receipt_id"] = "/parent"
			}
		case "coverage":
			entries := []any{}
			for i, v := range listField(a, "entries") {
				mp := fmt.Sprintf("/entries/%d", i)
				entries = append(entries, n.entry("coverage", v.(map[string]any), mp, "/record"+mp))
			}
			r["entries"] = entries
		}
		return map[string]any{"record_kind": kind, "record": r}
	case "artifact_publish":
		body := n.content(a, "", "/content")
		delete(body, "media_type")
		return map[string]any{"purpose": a["purpose"], "media_type": a["media_type"], "content": body}
	case "reference_read", "observation_read":
		key, kind := "reference", "reference"
		if name == "observation_read" {
			key = "observation"
			kind = "observation"
		}
		r := n.resource(a[key], kind, "/"+key, false, false)
		out := copyFields(a, "offset", "max_bytes")
		if name == "observation_read" {
			out["receipt_id"] = r["attempt_receipt_id"]
			out["entry_id"] = r["entry_id"]
			n.paths["/receipt_id"] = "/observation"
			n.paths["/entry_id"] = "/observation"
		} else {
			out["reference_id"] = r["reference_id"]
			n.paths["/reference_id"] = "/reference"
		}
		if extent, ok := r["range"].(map[string]any); ok {
			if n.err != nil {
				return out
			}
			remaining := number(extent["offset"]) + number(extent["length"]) - number(out["offset"])
			if number(out["offset"]) < number(extent["offset"]) || remaining <= 0 {
				n.fail("range_exceeded", "/offset", fmt.Sprintf("This handle permits offsets [%d,%d). Read within that range; for further pages use continuation.arguments with the original readable handle.", number(extent["offset"]), number(extent["offset"])+number(extent["length"])))
			}
			if number(out["max_bytes"]) > remaining {
				out["max_bytes"] = json.Number(fmt.Sprint(remaining))
			}
		}
		return out
	case "injection_delete":
		r := n.resource(a["injection"], "injection", "/injection", false, true)
		n.paths["/attempt_receipt_id"] = "/injection"
		n.paths["/action_id"] = "/injection"
		return copyFields(r, "attempt_receipt_id", "action_id")
	case "snapshot_inspect", "restore_request":
		r := n.resource(a["snapshot"], "checkpoint", "/snapshot", false, false)
		n.paths["/source_session"] = "/snapshot"
		n.paths["/checkpoint_id"] = "/snapshot"
		return copyFields(r, "source_session", "checkpoint_id")
	case "snapshot_list":
		out := copyFields(a, "offset", "limit")
		if h, ok := a["session"]; ok {
			out["source_session"] = n.resource(h, "session", "/session", false, false)["source_session"]
			n.paths["/source_session"] = "/session"
		}
		return out
	}
	return a
}

// NormalizeToolArguments is pure and deterministic for one stable native call.
// Call conflict handling, resource verification and effective host allowances
// remain with the existing dispatcher. Expanded requests are validated here too.
func (p *Protocol) NormalizeToolArguments(name string, raw []byte, ctx ToolResolution) (ToolNormalization, error) {
	args, err := p.ValidateToolArguments(name, raw)
	if err != nil {
		return ToolNormalization{}, err
	}
	for _, id := range []string{ctx.CampaignID, ctx.CallID} {
		if len(id) == 0 || len(id) > 128 || !validModelIdentity(id) {
			return ToolNormalization{}, argumentIssue("invalid_resolution_context", "", "Internal resource resolution failed. Stop this operation and report the product error; do not supply internal identities or replay completed actions.")
		}
	}
	if len(ctx.Resources) > 4096 {
		return ToolNormalization{}, argumentIssue("invalid_resolution_context", "", "Internal resource resolution failed. Stop this operation and report the product error; do not supply internal identities or replay completed actions.")
	}
	// Canonical copying accepts ordinary Go integer values and prevents mutating
	// the caller's verified state while normalizing database keys.
	contextRaw, contextErr := json.Marshal(ctx)
	if contextErr != nil {
		return ToolNormalization{}, ErrJSON
	}
	if _, contextErr = Decode(contextRaw, OrdinaryLimit); contextErr != nil {
		return ToolNormalization{}, contextErr
	}
	decoder := json.NewDecoder(strings.NewReader(string(contextRaw)))
	decoder.UseNumber()
	if contextErr = decoder.Decode(&ctx); contextErr != nil {
		return ToolNormalization{}, contextErr
	}
	n := toolNormalizer{ctx: ctx, paths: map[string]string{}, selections: []map[string]any{}, locals: map[string][2]string{}}
	result := n.normalize(name, args)
	// Conclusion semantic checks report paths within the draft, while request
	// schema checks include /assessment. Both refer to the same model fields.
	if name == "finish" {
		aliases := map[string]string{}
		for host, model := range n.paths {
			if strings.HasPrefix(host, "/assessment/") {
				aliases[strings.TrimPrefix(host, "/assessment")] = model
			}
		}
		for host, model := range aliases {
			n.paths[host] = model
		}
	}

	if n.err != nil {
		return ToolNormalization{}, n.err
	}
	raw, err = canonicalValue(result, OrdinaryLimit)
	if err != nil {
		return ToolNormalization{}, err
	}
	if name != "artifact_publish" && name != "reference_read" {
		operation := p.operations["engine."+name]
		if len(raw) > operation.MaxRequestBytes {
			return ToolNormalization{}, argumentIssue("request_too_large", "", "Expanded request exceeds its operation limit; reduce content/references or use a committed artifact.")
		}
		schema := operation.RequestSchema
		if _, err = p.catalog.Validate(schema, raw, OrdinaryLimit); err != nil {
			issue := p.catalog.schemaArgumentIssue(err)
			path := RemapToolPath(n.paths, issue.FieldPath)
			if path == issue.FieldPath && (strings.Contains(path, "receipt") || strings.Contains(path, "binding") || strings.Contains(path, "_id") && !strings.Contains(path, "objective_id") && !strings.Contains(path, "scenario_id") && !strings.Contains(path, "operation_id")) {
				return ToolNormalization{}, argumentIssue("invalid_resolution_context", "", "Internal resource resolution failed. Stop this operation and report the product error; do not supply internal identities or replay completed actions.")
			}
			issue.FieldPath = path
			return ToolNormalization{}, issue
		}
	}
	return ToolNormalization{result, n.paths, n.selections}, nil
}
func validModelIdentity(s string) bool {
	for i, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		if i > 0 && strings.ContainsRune("._:-", c) {
			continue
		}
		return false
	}
	return true
}

func checkModelChoices(name string, a map[string]any) error {
	has := func(k string) bool { _, ok := a[k]; return ok }
	if name == "finish" {
		if has("assessment") {
			return argumentIssue("obsolete_wrapper", "/assessment", "Put summary, objectives and optional assessment collections beside finish_reason; omit the assessment wrapper.")
		}
		inline := false
		for _, k := range []string{"summary", "objectives", "status", "hypotheses", "claims", "coverage", "uncertainties", "records"} {
			inline = inline || has(k)
		}
		count := 0
		for _, b := range []bool{inline, has("artifact"), has("unavailable_reason")} {
			if b {
				count++
			}
		}
		if count != 1 {
			return argumentIssue("finish_choice", "", "Supply inline summary and objectives, or artifact, or unavailable_reason; never combine these alternatives.")
		}
		if inline {
			for _, k := range []string{"summary", "objectives"} {
				if !has(k) {
					return argumentIssue("finish_field_required", "/"+k, "Inline finish requires both summary and objectives.")
				}
			}
		}
	}
	if name == "attempt_execute" {
		setup, ok := a["setup"].([]any)
		if !has("setup") {
			ok = true
		}
		if ok {
			if len(setup) > 0 && !has("injection") {
				return argumentIssue("injection_required", "/injection", "Nonempty setup requires injection content.")
			}
			if len(setup) == 0 {
				for _, k := range []string{"injection", "retain_injections"} {
					if has(k) {
						return argumentIssue("setup_required", "/"+k, "Injection content and retention require nonempty setup; omit them when no setup is requested.")
					}
				}
			}
		}
	}
	if name == "record_append" && a["kind"] == "progress" {
		fields := []string{"interpretation", "confidence", "assurance", "assessment_summary"}
		any := false
		for _, k := range fields {
			any = any || has(k)
		}
		if any {
			for _, k := range fields {
				if !has(k) {
					return argumentIssue("assessment_fields_required", "/"+k, "Supply interpretation, confidence, assurance and assessment_summary together, or omit all four.")
				}
			}
		} else if has("gaps") {
			return argumentIssue("assessment_fields_required", "/gaps", "gaps is only allowed with the complete progress assessment.")
		}
	}
	return nil
}

func checkPublishedContent(a map[string]any) error {
	if value, ok := a["base64"].(string); ok {
		b, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || base64.StdEncoding.EncodeToString(b) != value {
			return argumentIssue("invalid_base64", "/base64", "Supply canonical padded base64 or use JSON/UTF-8 input.")
		}
	}
	return nil
}

// Suggest only verified same-entry inspected resources, never identifier prefixes.
func (n *toolNormalizer) inspectionCorrection(r ToolResource) string {
	candidates := []string{}
	for h, v := range n.ctx.Resources {
		if len(h) <= 64 && validModelIdentity(h) && v.Kind == "observation" && v.CampaignID == n.ctx.CampaignID && v.Available && v.Inspected && textField(v.Value, "entry_id") != "" && textField(v.Value, "entry_id") == textField(r.Value, "entry_id") && textField(v.Value, "attempt_receipt_id") == textField(r.Value, "attempt_receipt_id") {
			candidates = append(candidates, h)
		}
	}
	sort.Strings(candidates)
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}
	if len(candidates) > 0 {
		raw, _ := json.Marshal(candidates)
		return "Cite inspected ranges from this entry: " + string(raw) + ". These do not establish inspection of unread bytes."
	}
	return "No inspected citation for this entry is available. Call observation_read with the original readable handle, then cite the returned citation handle; follow continuation.arguments for additional pages."
}
