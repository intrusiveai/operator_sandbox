package contracts

import (
	"encoding/json"
	"errors"
)

// ModelTools returns the package-owned native projection for a verified operation
// set. This only declares tools; the fixed runtime dispatcher grants no authority
// from a declaration and independently validates every call and live binding.
func (p *Protocol) ModelTools(codec string, operations []string) ([]byte, error) {
	return p.modelTools(codec, operations, nil)
}

// ModelToolsSelected freezes the supported injection alternatives for a campaign.
func (p *Protocol) ModelToolsSelected(codec string, operations, surfaces []string) ([]byte, error) {
	if surfaces == nil {
		surfaces = []string{}
	}
	previous := ""
	for _, s := range surfaces {
		if s <= previous || !validSurface(s) {
			return nil, ErrProtocol
		}
		previous = s
	}
	return p.modelTools(codec, operations, surfaces)
}
func validSurface(s string) bool {
	switch s {
	case "model_tool_result", "model_response", "service_response", "mcp_tool_result", "environment_state", "database_response":
		return true
	}
	return false
}
func (p *Protocol) modelTools(codec string, operations, surfaces []string) ([]byte, error) {
	if len(operations) > 64 {
		return nil, ErrProtocol
	}
	admitted := map[string]bool{}
	previous := ""
	for _, name := range operations {
		if _, ok := p.operations[name]; !ok || name <= previous {
			return nil, ErrProtocol
		}
		admitted[name] = true
		previous = name
	}
	raw, ok := p.catalog.sources[ModelToolArgumentsSchema]
	if !ok {
		return nil, ErrCatalog
	}
	v, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	tools := []any{}
	declarations := []any{}
	for _, value := range v.(map[string]any)["oneOf"].([]any) {
		branch := value.(map[string]any)
		enabled := true
		for _, op := range branch["x-operator-required-operations"].([]any) {
			if !admitted[op.(string)] {
				enabled = false
			}
		}
		if !enabled {
			continue
		}
		properties := branch["properties"].(map[string]any)
		name := properties["name"].(map[string]any)["const"]
		parameters := properties["arguments"]
		if projected, ok := branch["x-operator-provider-parameters"]; ok {
			parameters = projected
		}
		if codec == "openai-chat-text-tools-v1" || codec == responsesCodec {
			if projected, ok := branch["x-operator-openai-parameters"]; ok {
				parameters = projected
			}
		}
		if name == "attempt_execute" && (!admitted["engine.injection_delete"] || (surfaces != nil && len(surfaces) == 0)) {
			// Each declaration is decoded afresh, so narrowing cannot mutate the catalog.
			props := parameters.(map[string]any)["properties"].(map[string]any)
			for _, field := range []string{"setup", "injection", "retain_injections"} {
				delete(props, field)
			}
		}
		if name == "attempt_execute" && admitted["engine.injection_delete"] && len(surfaces) > 0 {
			injection := parameters.(map[string]any)["properties"].(map[string]any)["setup"].(map[string]any)["items"].(map[string]any)
			surfaceValues := []any{}
			for _, s := range surfaces {
				surfaceValues = append(surfaceValues, s)
			}
			injection["properties"].(map[string]any)["surface"] = map[string]any{"enum": surfaceValues}
			selected := []any{}
			for _, branch := range injection["oneOf"].([]any) {
				surface := branch.(map[string]any)["properties"].(map[string]any)["surface"].(map[string]any)["const"]
				for _, s := range surfaces {
					if s == surface {
						selected = append(selected, branch)
					}
				}
			}
			injection["oneOf"] = selected
		}
		description := branch["description"]
		switch codec {
		case "openai-chat-text-tools-v1":
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": parameters, "strict": false}})
		case responsesCodec:
			tools = append(tools, map[string]any{"type": "function", "name": name, "description": description, "parameters": parameters, "strict": false})
		case anthropicCodec:
			tools = append(tools, map[string]any{"name": name, "description": description, "input_schema": parameters})
		case bedrockCodec:
			tools = append(tools, map[string]any{"toolSpec": map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"json": parameters}}})
		case geminiCodec:
			declarations = append(declarations, map[string]any{"name": name, "description": description, "parametersJsonSchema": parameters})
		default:
			return nil, ErrProtocol
		}
	}
	if codec == geminiCodec && len(declarations) > 0 {
		tools = append(tools, map[string]any{"functionDeclarations": declarations})
	}
	return canonicalValue(tools, OrdinaryLimit)
}

// ValidateToolArguments validates local arguments; the host allocates attempt bookkeeping.
func (p *Protocol) ValidateToolArguments(name string, raw []byte) (map[string]any, error) {
	value, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		var issue *ArgumentError
		if errors.As(err, &issue) {
			return nil, err
		}
		return nil, argumentIssue("invalid_json", "", "Supply one valid UTF-8 JSON object with unique keys, finite JSON-safe numbers and bounded nesting; do not replay completed actions.")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, argumentIssue("invalid_structure", "", "Expected object. Supply named tool arguments in one JSON object.")
	}
	if name == "attempt_execute" {
		if err := ValidateAttemptOrigin(object, nil); err != nil {
			return nil, err
		}
	}
	if err := checkModelChoices(name, object); err != nil {
		return nil, err
	}
	if name == "finish" {
		if _, exists := object["assessment"]; exists {
			return nil, argumentIssue("obsolete_wrapper", "/assessment", "Put summary, objectives and optional assessment collections beside finish_reason; omit the assessment wrapper.")
		}
	}
	if name == "attempt_execute" || name == "artifact_publish" {
		if _, err := modelContent(object, ""); err != nil {
			return nil, err
		}
	}
	if name == "attempt_execute" {
		if err := p.checkModelContent(object, ""); err != nil {
			var issue *ArgumentError
			if errors.As(err, &issue) {
				return nil, err
			}
		}
	}
	document, _ := Decode(p.catalog.sources[ModelToolArgumentsSchema], OrdinaryLimit)
	matched := false
	for index, value := range document.(map[string]any)["oneOf"].([]any) {
		branch := value.(map[string]any)
		if branch["properties"].(map[string]any)["name"].(map[string]any)["const"] != name {
			continue
		}
		matched = true
		if err := p.catalog.schemas[ModelToolArgumentsSchema].OneOf[index].Properties["arguments"].Validate(object); err != nil {
			return nil, p.catalog.schemaArgumentIssue(err)
		}
		break
	}
	if !matched {
		return nil, argumentIssue("unsupported_tool", "/name", "Use a tool from the advertised catalog.")
	}
	if name == "attempt_execute" {
		if err := p.checkModelContent(object, ""); err != nil {
			return nil, err
		}
	}
	if name == "snapshot_request" {
		if err := checkSnapshotDescription(object); err != nil {
			return nil, err
		}
	}
	if name == "artifact_publish" {
		if err := checkPublishedContent(object); err != nil {
			return nil, err
		}
	}
	if name == "reference_read" || name == "observation_read" {
		if _, ok := object["offset"]; !ok {
			object["offset"] = json.Number("0")
		}
		if _, ok := object["max_bytes"]; !ok {
			object["max_bytes"] = json.Number("4096")
		}
	}
	return object, nil
}

// ToolGuidance is versioned reference material, not another tool catalog.
func (p *Protocol) ToolGuidance(name string) ([]byte, error) {
	raw := p.catalog.sources[ModelToolArgumentsSchema]
	v, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	for _, item := range v.(map[string]any)["oneOf"].([]any) {
		branch := item.(map[string]any)
		props := branch["properties"].(map[string]any)
		if props["name"].(map[string]any)["const"] == name {
			guide := map[string]any{"tool": name, "description": branch["description"], "arguments": props["arguments"], "guidance": "Use the selected resource's details_reference for valid selectors and content. Use typed handles from corresponding verified inputs or tool results; never manufacture them. Runtime rejects invalid values before dispatch."}
			if examples, ok := branch["x-operator-guidance"]; ok {
				guide["examples"] = examples
			}
			return canonicalValue(guide, OrdinaryLimit)
		}
	}
	return nil, ErrProtocol
}
