package capabilities

import (
	"net/http"
	"strings"

	"github.com/intrusiveai/operator_sandbox/internal/nativedelivery"
)

// Validate metadata before it becomes an authorization input. Empty route-set
// allowlists are meaningful; empty existing-record inventories are not.
func validEndpoint(ep nativedelivery.Endpoint) bool {
	switch ep.Kind {
	case "http-route", "mcp-tool":
		return allowed(ep.InjectionRoot, "response-body", "mcp-tool-result") && len(ep.Methods) == 0 && len(ep.Paths) == 0 && len(ep.Keys) == 0 && len(ep.MutableFields) == 0
	case "http-route-set":
		if ep.InjectionRoot != "response-body" || ep.Delivery == nil || ep.Method != "" || ep.Path != "" || ep.Collection != "" || len(ep.Keys) > 0 || len(ep.MutableFields) > 0 {
			return false
		}
		if !uniqueStrings(ep.Methods) || !uniqueStrings(ep.Paths) {
			return false
		}
		for _, method := range ep.Methods {
			if method == "" || method == http.MethodHead || method != strings.ToUpper(method) {
				return false
			}
			if _, err := http.NewRequest(method, "http://local.invalid/", nil); err != nil {
				return false
			}
		}
		for _, p := range ep.Paths {
			if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#\r\n") {
				return false
			}
		}
		return true
	case "state-collection":
		return ep.InjectionRoot == "service-record" && ep.Delivery != nil && ep.Method == "" && ep.Path == "" && len(ep.Methods) == 0 && len(ep.Paths) == 0 && uniqueIDs([]string{ep.Collection}) && len(ep.Keys) > 0 && uniqueIDs(ep.Keys) && len(ep.MutableFields) > 0 && uniqueIDs(ep.MutableFields)
	}
	return false
}
func uniqueStrings(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
