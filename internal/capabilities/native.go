package capabilities

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/nativedelivery"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func verifyNative(raw []byte) (nativeManifest, map[string]any, error) {
	var n nativeManifest
	value, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	if err != nil || !exactShape(value, reflect.TypeOf(n)) {
		return n, nil, ErrNative
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&n); err != nil {
		return n, nil, ErrNative
	}
	digest := n.Digest
	n.Digest = ""
	encoded, err := json.Marshal(n)
	n.Digest = digest
	if err != nil || contracts.RawDigest(encoded) != digest || !validNative(&n) {
		return nativeManifest{}, nil, ErrNative
	}
	return n, value.(map[string]any), nil
}

// encoding/json accepts case-insensitive struct aliases and null scalar values.
// Reject those before typed native decoding. Maps/interfaces are inert delivery
// schemas/examples and already passed the shared strict JSON decoder.
func exactShape(value any, t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		if value == nil {
			return true
		}
		return exactShape(value, t.Elem())
	}
	switch t.Kind() {
	case reflect.Interface, reflect.Map:
		return true
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			known[name] = true
			v, found := object[name]
			if !found {
				if !contains(tag, "omitempty") {
					return false
				}
				continue
			}
			if !exactShape(v, f.Type) {
				return false
			}
		}
		for key := range object {
			if !known[key] {
				return false
			}
		}
		return true
	case reflect.Slice:
		if value == nil {
			return true
		}
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, v := range items {
			if !exactShape(v, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := value.(string)
		return ok
	case reflect.Bool:
		_, ok := value.(bool)
		return ok
	default:
		_, ok := value.(json.Number)
		return ok
	}
}
func allowed(value string, items ...string) bool { return contains(items, value) }
func subset(values []string, allowed ...string) bool {
	for _, v := range values {
		if !contains(allowed, v) {
			return false
		}
	}
	return true
}
func uniqueIDs(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func validNative(n *nativeManifest) bool {
	if n.APIVersion != "interceptor.dev/capability-manifest/v1alpha3" || n.Kind != "CapabilityManifest" || !digestPattern.MatchString(n.EnvironmentDigest) || !digestPattern.MatchString(n.ApplicationDigest) {
		return false
	}
	deliveryVersion := n.DeliverySchemaProfile == nativedelivery.SchemaProfile
	if n.DeliverySchemaProfile != "" && !deliveryVersion {
		return false
	}
	if !allowed(n.Target.Interface, "http", "command", "stdio", "repl") || len(n.FeedbackProfiles) == 0 || !uniqueIDs(n.FeedbackProfiles) || !subset(n.FeedbackProfiles, "black-box", "diagnostic", "oracle-assisted") {
		return false
	}
	if n.Feedback.ViewVersion != "interceptor.dev/observation-view/v1alpha2" || !subset(n.Feedback.SelectionModes, "all-permitted", "selected") || !subset(n.Feedback.Kinds, profileKinds["oracle-assisted"]...) || n.Feedback.MaximumChunkBytes < 1 || n.Feedback.MaximumChunkBytes > 262144 || n.Feedback.MaximumEntries < 1 || n.Feedback.MaximumEntries > 64 {
		return false
	}
	if !subset(n.CrossVMOperations, "application.invoke", "artifact.read", "artifact.register", "attempt.register", "capabilities.read", "injection.arm", "injection.delete", "observation.content.read", "observation.read", "operation.status", "session.owner", "session.status", "session.stop", "snapshot.create") {
		return false
	}
	surfaces := []string{"model_tool_result", "service_response", "mcp_tool_result", "environment_state"}
	if !subset(n.InjectionSurfaces, surfaces...) {
		return false
	}
	ids := []string{}
	for _, op := range n.Operations {
		ids = append(ids, op.ID)
		if !allowed(op.DeliveryStatus, "", "described", "missing-input-contract") || op.MaximumInputBytes < 0 || op.MaximumInputBytes > nativedelivery.MaxBodyBytes {
			return false
		}
		if op.Delivery != nil {
			if !deliveryVersion || op.DeliveryStatus != "described" || op.MaximumInputBytes < 1 || op.MaximumInputBytes > op.Delivery.Input.MaxBytes || nativedelivery.Validate(op.Delivery) != nil {
				return false
			}
		} else if op.DeliveryStatus == "described" {
			return false
		}
	}
	if !uniqueIDs(ids) {
		return false
	}
	ids = nil
	for _, profile := range n.InjectionProfiles {
		ids = append(ids, profile.Surface)
		permitted, ok := injectionVocabulary[profile.Surface]
		if !ok || !contains(n.InjectionSurfaces, profile.Surface) || !subset(profile.Scopes, permitted.Scopes...) || !subset(profile.Placements, permitted.Placements...) || !subset(profile.SelectorFields, permitted.SelectorFields...) || !subset(profile.Carriers, permitted.Carriers...) {
			return false
		}
	}
	if !uniqueIDs(ids) {
		return false
	}
	ids = nil
	for _, svc := range n.Services {
		ids = append(ids, svc.ID)
		if !allowed(svc.Kind, "fixture_http", "stateful_http", "mcp_http", "oidc", "http_sink") || !allowed(svc.Role, "environment", "identity", "attacker") || !allowed(svc.Implementation, "", "generic") || !allowed(svc.ResponseMode, "", "static", "dynamic") || !subset(svc.Transports, "http", "https") || !subset(svc.InjectionSurfaces, surfaces...) {
			return false
		}
		endpoints := []string{}
		collections := []string{}
		for _, ep := range svc.Endpoints {
			endpoints = append(endpoints, ep.ID)
			if !deliveryVersion || !allowed(ep.Kind, "http-route", "mcp-tool") || !allowed(ep.InjectionRoot, "response-body", "mcp-tool-result") || ep.MaximumRequestBytes < 1 || ep.MaximumRequestBytes > nativedelivery.MaxBodyBytes || nativedelivery.Validate(ep.Delivery) != nil {
				return false
			}
		}
		for _, c := range svc.CreatableCollections {
			collections = append(collections, c.Collection)
			if c.MaxResources < 1 {
				return false
			}
		}
		if !uniqueIDs(endpoints) || !uniqueIDs(collections) {
			return false
		}
	}
	if !uniqueIDs(ids) {
		return false
	}
	ids = nil
	for _, ns := range n.FileNamespaces {
		ids = append(ids, ns.ID)
		if ns.MaxFiles < 0 || ns.MaxFileBytes < 0 {
			return false
		}
	}
	if !uniqueIDs(ids) {
		return false
	}
	if !subset(n.Oracles, "attacker_sink_match", "output_contains", "model_output_contains", "tool_call_match", "service_state_match", "file_state_match", "event_match") {
		return false
	}
	if !subset(n.Network.DNS.RecordTypes, "A") || !allowed(n.Network.DNS.UnknownPolicy, "", "nxdomain", "refused") || !allowed(n.Network.HTTPS.TrustMode, "", "auto", "bundle", "application-managed") || !allowed(n.Network.HTTPS.MinimumVersion, "", "TLS1.2") {
		return false
	}
	return n.Limits.MaximumAttempts > 0 && n.Limits.MaximumArtifacts > 0 && n.Limits.MaximumArtifactBytes > 0 && n.Limits.MaximumInvocations > 0 && n.Limits.MaximumSnapshots >= 0
}
