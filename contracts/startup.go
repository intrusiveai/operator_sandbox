package contracts

import "strings"

// ValidateControl checks one direction's closed control union. It grants no
// filesystem access or execution admission; those depend on live host gates.
func (p *Protocol) ValidateControl(direction string, raw []byte) (map[string]any, error) {
	var schema string
	switch direction {
	case "host":
		schema = EngineHostControlSchema
	case "guest":
		schema = EngineGuestControlSchema
	default:
		return nil, ErrProtocol
	}
	v, err := p.catalog.Validate(schema, raw, ControlLimit)
	if err != nil {
		return nil, err
	}
	message := v.(map[string]any)
	body := message["body"].(map[string]any)
	switch message["kind"] {
	case "bootstrap", "confinement_ready":
		expected := "fifo"
		if strings.HasPrefix(body["host_platform"].(string), "darwin/") {
			expected = "spool"
		}
		if body["transport"] != expected {
			return nil, ErrProtocol
		}
	case "initialize", "initialized", "admission_open":
		binding := body["binding"].(map[string]any)
		for _, key := range []string{"campaign_id", "launch_id", "run_revision"} {
			if !same(message[key], binding[key]) {
				return nil, ErrProtocol
			}
		}
		if message["kind"] != "admission_open" {
			if !same(body["input_tree"].(map[string]any)["digest"], binding["input_tree_digest"]) || !same(body["skill_set"].(map[string]any)["digest"], binding["skill_set_digest"]) {
				return nil, ErrProtocol
			}
		} else {
			previous := ""
			for _, name := range body["operations"].([]any) {
				s := name.(string)
				if s <= previous {
					return nil, ErrProtocol
				}
				if _, ok := p.operations[s]; !ok {
					return nil, ErrProtocol
				}
				previous = s
			}
			limits := body["limits"].(map[string]any)
			campaign := limits["campaign"].(map[string]any)
			for key, value := range body["remaining_limits"].(map[string]any) {
				if number(value) > number(campaign[key]) {
					return nil, ErrProtocol
				}
			}
			harness := limits["harness"].(map[string]any)
			if number(campaign["model_turns"]) > number(harness["max_model_turns"]) || number(campaign["observation_bytes"]) > number(harness["max_read_bytes"]) {
				return nil, ErrProtocol
			}
		}
	}
	return message, nil
}

// ValidateStartup checks a complete successful five-message transcript, not live
// deadlines, confinement, journal gates or transport I/O. Termination is validated
// separately and cannot make an incomplete startup count as admitted.
func (p *Protocol) ValidateStartup(raw [][]byte) error {
	if len(raw) != 5 {
		return ErrProtocol
	}
	directions := []string{"host", "guest", "host", "guest", "host"}
	kinds := []string{"bootstrap", "confinement_ready", "initialize", "initialized", "admission_open"}
	seqs := []int64{0, 0, 1, 1, 2}
	messages := make([]map[string]any, 5)
	bodies := make([]map[string]any, 5)
	for i, data := range raw {
		m, err := p.ValidateControl(directions[i], data)
		if err != nil {
			return err
		}
		if m["kind"] != kinds[i] || number(m["seq"]) != seqs[i] {
			return ErrProtocol
		}
		if i > 0 {
			for _, key := range []string{"campaign_id", "launch_id", "run_revision"} {
				if !same(m[key], messages[0][key]) {
					return ErrProtocol
				}
			}
		}
		messages[i] = m
		bodies[i] = m["body"].(map[string]any)
	}
	for key, value := range bodies[1] {
		if !wireEqual(value, bodies[0][key]) {
			return ErrProtocol
		}
	}
	for _, key := range []string{"input_tree", "skill_set", "binding", "engine_context_object_digest"} {
		if !wireEqual(bodies[2][key], bodies[3][key]) {
			return ErrProtocol
		}
	}
	if !wireEqual(bodies[3]["contract"], bodies[0]["contract"]) || !wireEqual(bodies[4]["binding"], bodies[2]["binding"]) {
		return ErrProtocol
	}
	binding := bodies[2]["binding"].(map[string]any)
	contract := bodies[0]["contract"].(map[string]any)
	release := bodies[0]["release"].(map[string]any)
	for key, value := range map[string]any{"contract_package_version": contract["version"], "contract_package_digest": contract["digest"], "image_digest": release["image_digest"], "release_record_digest": release["release_record_digest"]} {
		if !same(binding[key], value) {
			return ErrProtocol
		}
	}
	return nil
}

// wireEqual compares decoded typed wire values, preserving integer equivalence.
// This is not a canonicalizer or a digest recipe.
func wireEqual(left, right any) bool {
	switch l := left.(type) {
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok || len(l) != len(r) {
			return false
		}
		for k, v := range l {
			rv, ok := r[k]
			if !ok || !wireEqual(v, rv) {
				return false
			}
		}
		return true
	case []any:
		r, ok := right.([]any)
		if !ok || len(l) != len(r) {
			return false
		}
		for i, v := range l {
			if !wireEqual(v, r[i]) {
				return false
			}
		}
		return true
	default:
		return same(left, right)
	}
}
