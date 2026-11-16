package attemptadapter

import (
	"net/http"
	"slices"
	"strings"

	"github.com/intrusiveai/operator_sandbox/internal/nativedelivery"
)

func responseEndpointMatches(ep nativedelivery.Endpoint, method, path string) bool {
	switch ep.Kind {
	case "http-route":
		return ep.Method == method && endpointMatches(ep.Path, path)
	case "http-route-set":
		// The profile still supplies one concrete route. Never expand an unrestricted
		// capability into wildcard authorization or interpret allowlist path templates.
		if method == "" || method == http.MethodHead || method != strings.ToUpper(method) {
			return false
		}
		if _, err := http.NewRequest(method, "http://local.invalid/", nil); err != nil {
			return false
		}
		return (len(ep.Methods) == 0 || slices.Contains(ep.Methods, method)) && (len(ep.Paths) == 0 || slices.Contains(ep.Paths, path))
	}
	return false
}

func validStateCollectionRoute(ep nativedelivery.Endpoint, r Route) bool {
	if !slices.Contains(ep.Keys, r.Target.Key) {
		return false
	}
	for _, pointer := range r.Pointers {
		parts := pointerParts(pointer)
		if len(parts) == 0 || !slices.Contains(ep.MutableFields, parts[0]) {
			return false
		}
	}
	return true
}
