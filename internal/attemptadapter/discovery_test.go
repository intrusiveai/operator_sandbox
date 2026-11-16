package attemptadapter

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativedelivery"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func discoveryExport(t *testing.T) (*contracts.Catalog, *capabilities.Export, []byte) {
	t.Helper()
	c, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/fixtures/discoverable-injection-targets/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := capabilities.FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	return c, e, raw
}
func responseRoute(service, method, path string) Route {
	return Route{Surface: "service_response", Target: Target{Service: service, Method: method, Path: path}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{""}}
}
func stateRoute(service, collection, key, pointer string) Route {
	return Route{Surface: "environment_state", Target: Target{Service: service, Collection: collection, Key: key}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{pointer}}
}

func TestDiscoveryRouteAuthorization(t *testing.T) {
	_, e, _ := discoveryExport(t)
	cases := []struct {
		name  string
		route Route
		valid bool
	}{
		{"restricted exact", responseRoute("restricted", "POST", "/collect"), true},
		{"independent lists", responseRoute("restricted", "GET", "/poll"), true},
		{"unrestricted", responseRoute("open", "PATCH", "/chosen"), true},
		{"method only", responseRoute("method-only", "POST", "/chosen"), true},
		{"path only", responseRoute("path-only", "PATCH", "/collect"), true},
		{"forbidden method", responseRoute("restricted", "DELETE", "/collect"), false},
		{"forbidden path", responseRoute("restricted", "POST", "/other"), false},
		{"method only denial", responseRoute("method-only", "DELETE", "/chosen"), false},
		{"path only denial", responseRoute("path-only", "POST", "/other"), false},
		{"HEAD open", responseRoute("open", "HEAD", "/chosen"), false},
		{"HEAD only", responseRoute("head-only", "GET", "/chosen"), false},
		{"static", responseRoute("static", "POST", "/chosen"), false},
		{"missing method", responseRoute("open", "", "/chosen"), false},
		{"invalid method", responseRoute("open", "GET bad", "/chosen"), false},
		{"lowercase method", responseRoute("open", "get", "/chosen"), false},
		{"relative path", responseRoute("open", "POST", "chosen"), false},
		{"query path", responseRoute("open", "POST", "/chosen?x=1"), false},
		{"grant", stateRoute("idp", "grants", "grant", "/active"), true},
		{"grant scopes", stateRoute("idp", "grants", "grant", "/scopes/0"), true},
		{"delegation", stateRoute("idp", "delegations", "delegation", "/active"), true},
		{"fault", stateRoute("idp", "faults", "issuer", "/unavailable"), true},
		{"other issuer", stateRoute("other-idp", "grants", "other-grant", "/active"), true},
		{"cross issuer", stateRoute("idp", "grants", "other-grant", "/active"), false},
		{"empty collection not exported", stateRoute("other-idp", "delegations", "delegation", "/active"), false},
		{"unknown collection", stateRoute("idp", "unknown", "grant", "/active"), false},
		{"unknown key", stateRoute("idp", "grants", "new-grant", "/active"), false},
		{"immutable identity", stateRoute("idp", "grants", "grant", "/principal"), false},
		{"root replacement", stateRoute("idp", "grants", "grant", ""), false},
		{"escaped immutable field", stateRoute("idp", "grants", "grant", "/active~1principal"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Resolve(e, Scopes{OperationIDs: []string{"invoke"}, Routes: []Route{c.route}}, []string{"target_output"}, "")
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.valid, err)
			}
			if err != nil {
				return
			}
			selector := interceptor.Selector{Service: c.route.Target.Service, Method: c.route.Target.Method, Path: c.route.Target.Path, Collection: c.route.Target.Collection, Key: c.route.Target.Key}
			d := interceptor.Definition{Surface: c.route.Surface, Scope: "once", Selector: selector, Placement: interceptor.Placement{Operation: "replace", Pointer: c.route.Pointers[0]}}
			if err = p.authorizeInjection(d, nil); err != nil {
				t.Fatal(err)
			}
			if d.Selector.Path != "" {
				d.Selector.Path = "/not-installed"
			} else {
				d.Selector.Key = "not-installed"
			}
			if err = p.authorizeInjection(d, nil); err == nil {
				t.Fatal("capability broadened the installed route")
			}
		})
	}
	// Route-set paths are literal, unlike the existing http-route template form.
	ep := nativedelivery.Endpoint{Kind: "http-route-set", Methods: []string{"POST"}, Paths: []string{"/items/{id}"}}
	if responseEndpointMatches(ep, "POST", "/items/1") {
		t.Fatal("allowlist treated as a template")
	}
	if !responseEndpointMatches(ep, "POST", "/items/{id}") {
		t.Fatal("literal route did not match")
	}
}

func discoveryPlan(t *testing.T, route Route, payload []byte) (*Plan, []byte) {
	return injectionPlan(t, route, payload, "discoverable-injection-targets")
}
func injectionPlan(t *testing.T, route Route, payload []byte, fixtureName string) (*Plan, []byte) {
	t.Helper()
	c, raw, in := fixture(t)
	nativeRaw, err := os.ReadFile("../../schemas/fixtures/" + fixtureName + "/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	export, err := capabilities.FromNative(c, nativeRaw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	var n map[string]any
	json.Unmarshal(nativeRaw, &n)
	binding := in.Live.Binding()
	a := interceptor.Attachment{CampaignID: "campaign-1", Binding: binding, Capabilities: nativeRaw, Session: interceptor.Session{ID: binding.SessionID, CampaignID: "campaign-1", OperationAPIVersion: interceptor.OperationVersion, Phase: "running", FeedbackProfile: "black-box", EnvironmentDigest: n["environment_digest"].(string), AppDigest: n["application_digest"].(string), CapabilityManifestDigest: export.SourceDigest()}}
	status := interceptor.Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: binding, Sessions: map[string]interceptor.Binding{binding.SessionID: binding}, Phase: "ready", StoreAvailable: true}
	in.Live, err = capabilities.BindLive(c, a, status, "instance-1", "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	in.Policy, err = Resolve(export, Scopes{OperationIDs: []string{"invoke"}, Routes: []Route{route}}, []string{"target_output"}, "")
	if err != nil {
		t.Fatal(err)
	}
	bundleRaw, err := os.ReadFile("../../schemas/fixtures/capability-chain/submitted-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	bundleRaw = mutate(bundleRaw, func(m map[string]any) {
		m["target_requirements"].(map[string]any)["capability_source_digest"] = export.SourceDigest()
		s := m["scenarios"].([]any)[0].(map[string]any)
		s["required_capability_refs"] = []string{"action:injection:" + route.Surface}
		if route.Target.Service != "" {
			s["required_capability_refs"] = append(s["required_capability_refs"].([]string), "service:"+route.Target.Service)
		}
		s["guidance"].(map[string]any)["action_refs"] = []string{"action:injection:" + route.Surface}
	})
	bundle, err := capabilities.ParseBundle(c, bundleRaw)
	if err != nil {
		t.Fatal(err)
	}
	in.Compatibility, err = capabilities.Check(bundle, export, in.Live, in.Policy.CapabilityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	pd := interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(payload), SizeBytes: int64(len(payload)), MediaType: "application/json", Canonicalization: "jcs-v1"}
	in.Artifacts[pd.Digest] = Artifact{CampaignID: "campaign-1", Descriptor: pd, Bytes: payload}
	mode := "simulation"
	selector := map[string]any{"service": route.Target.Service}
	if route.Surface == "model_response" {
		selector = map[string]any{"model_codec": route.Target.ModelCodec, "call_ordinal": "first"}
	} else if route.Surface == "environment_state" {
		mode = "fidelity"
		selector["collection"], selector["key"] = route.Target.Collection, route.Target.Key
	} else {
		selector["method"], selector["path"], selector["call_ordinal"] = route.Target.Method, route.Target.Path, "first"
	}
	protocol, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	guide, err := protocol.ToolGuidance("attempt_execute")
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"exact allowlists", "administrator", "HEAD", "existing key string", "mutable_fields", "not injection payloads", "does not undo"} {
		if !strings.Contains(string(guide), fact) {
			t.Fatalf("missing tool guidance: %s", fact)
		}
	}
	if route.Surface == "model_response" {
		for _, fact := range []string{"target application", "not Harness responses", "model_codec", "response_written", "not SDK acceptance"} {
			if !strings.Contains(string(guide), fact) {
				t.Fatalf("missing model-response tool guidance: %s", fact)
			}
		}
	}
	modelInput := map[string]any{"origin": "scenario", "scenario_id": "scenario-marker", "operation_id": "invoke", "json": map[string]any{"query": "Summarize ticket T-1"}, "injection": map[string]any{"json": json.RawMessage(payload)}, "setup": []any{map[string]any{"surface": route.Surface, "mode": mode, "scope": "once", "selector": selector, "placement": interceptor.Placement{Operation: "replace", Pointer: route.Pointers[0]}}}}
	normalized, err := protocol.NormalizeToolArguments("attempt_execute", encode(modelInput), contracts.ToolResolution{CampaignID: "campaign-1", CallID: "model-call-1"})
	if err != nil {
		t.Fatal(err)
	}
	raw = mutate(raw, func(m map[string]any) {
		m["payload"] = pd
		m["pre_actions"] = normalized.Arguments["pre_actions"]
		m["pre_actions"].([]any)[0].(map[string]any)["parameters"].(map[string]any)["payload_digest"] = pd.Digest
	})
	if _, err := c.Validate(contracts.AttemptPlanSchema, raw, contracts.OrdinaryLimit); err != nil {
		t.Fatal(err)
	}
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	return p, raw
}

// Exercises source export -> live binding -> policy/bundle admission -> native
// arm/invoke/read/delete with retained results. The peer validates actual wire
// exchanges against OpenAPI; it is controlled, not live Interceptor qualification.
func TestInterceptorContractDiscoveredInjectionExchanges(t *testing.T) {
	for _, c := range []struct {
		name    string
		route   Route
		payload string
	}{
		{"attacker response", responseRoute("restricted", "POST", "/collect"), `"injected response"`},
		{"identity grant", stateRoute("idp", "grants", "grant", "/active"), `false`},
		{"identity fault", stateRoute("idp", "faults", "issuer", "/unavailable"), `true`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, raw := discoveryPlan(t, c.route, []byte(c.payload))
			e, _, attempts, peer := executionSetup(t, raw, p)
			d, err := contractcheck.Load("../interceptor/openapi.json")
			if err != nil {
				t.Fatal(err)
			}
			peer.contract = d
			result, err := e.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Receipt == nil || len(result.Injections) != 1 || !result.Injections[0].Deleted {
				t.Fatal("missing feedback or cleanup")
			}
			expected := []string{"artifact.register", "artifact.register", "attempt.register", "injection.arm", "application.invoke", "observation.read", "observation.content.read", "injection.delete"}
			if !reflect.DeepEqual(peer.calls, expected) {
				t.Fatal(peer.calls)
			}
			for _, id := range result.StepIDs {
				step, _, _, err := attempts.NativeSteps().Lookup(id)
				if err != nil || step.State != campaign.ResultCommitted || step.Outcome != "succeeded" {
					t.Fatal("non-durable native result", err)
				}
			}
			if _, err = e.Run(context.Background()); err == nil || !reflect.DeepEqual(peer.calls, expected) {
				t.Fatal("attempt replayed")
			}
		})
	}
}
