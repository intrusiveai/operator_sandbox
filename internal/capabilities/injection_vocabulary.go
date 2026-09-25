// Native interceptor/v1 injection vocabulary, pinned with native_types.go.
package capabilities

var injectionVocabulary = func() map[string]InjectionProfile {
	result := map[string]InjectionProfile{}
	for _, p := range injectionProfiles([]string{"model_tool_result", "service_response", "mcp_tool_result", "environment_state"}) {
		result[p.Surface] = p
	}
	return result
}()

func injectionProfiles(surfaces []string) []InjectionProfile {
	profiles := make([]InjectionProfile, 0, len(surfaces))
	for _, surface := range surfaces {
		profile := InjectionProfile{Surface: surface}
		switch surface {
		case "model_tool_result":
			profile.Scopes = []string{"once", "next_turn", "standing"}
			profile.Placements = []string{"replace", "prepend", "append", "insert", "merge"}
			profile.SelectorFields = []string{"tool_name", "call_ordinal", "arguments_json_pointer", "arguments_equal", "arguments_contain", "turn_id"}
			profile.Carriers = []string{"model_client_tool_result"}
		case "service_response":
			profile.Scopes = []string{"once", "standing"}
			profile.Placements = []string{"replace", "prepend", "append", "insert", "merge"}
			profile.SelectorFields = []string{"service", "method", "path", "call_ordinal", "query_equal", "headers_equal", "request_body_json_pointer", "request_body_equal", "request_body_contain", "turn_id"}
			profile.Carriers = []string{"managed_http_response", "dynamic_attacker_sink_response"}
		case "mcp_tool_result":
			profile.Scopes = []string{"once", "standing"}
			profile.Placements = []string{"replace", "prepend", "append", "insert", "merge"}
			profile.SelectorFields = []string{"service", "tool_name", "call_ordinal", "arguments_json_pointer", "arguments_equal", "arguments_contain", "turn_id"}
			profile.Carriers = []string{"mcp_tools_call_result"}
		case "environment_state":
			profile.Scopes = []string{"once", "next_turn", "standing"}
			profile.Placements = []string{"replace", "prepend", "append", "insert", "merge"}
			profile.SelectorFields = []string{"service", "collection", "key", "path", "file_namespace", "relative_path", "turn_id"}
			profile.Carriers = []string{"managed_service_state", "mutable_file"}
		}
		profiles = append(profiles, profile)
	}
	return profiles
}
