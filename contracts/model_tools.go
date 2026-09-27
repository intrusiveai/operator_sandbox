package contracts

import "encoding/json"

// ModelTools returns the package-owned native projection for a verified operation
// set. This only declares tools; the fixed runtime dispatcher grants no authority
// from a declaration and independently validates every call and live binding.
func (p *Protocol) ModelTools(codec string, operations []string) ([]byte, error) {
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

// ValidateToolArguments performs only local argument shape validation. For a new
// attempt, allocate its bookkeeping tuple BEFORE calling this, after bounded
// object decoding. A later rejection never refunds the allocated submission index.
// Enforce configured argument byte limits and dispatch only advertised tools.
func (p *Protocol) ValidateToolArguments(name string, raw []byte) (map[string]any, error) {
	value, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrProtocol
	}
	encoded, err := json.Marshal(map[string]any{"name": name, "arguments": object})
	if err != nil {
		return nil, ErrProtocol
	}
	if _, err = p.catalog.Validate(ModelToolArgumentsSchema, encoded, OrdinaryLimit); err != nil {
		return nil, err
	}
	return object, nil
}
