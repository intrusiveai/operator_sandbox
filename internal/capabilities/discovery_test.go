package capabilities

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
)

func discoveryFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../schemas/fixtures/discoverable-injection-targets/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func discoveryService(m map[string]any, id string) map[string]any {
	for _, v := range m["services"].([]any) {
		s := v.(map[string]any)
		if s["id"] == id {
			return s
		}
	}
	panic("missing fixture service")
}
func discoveryEndpoint(m map[string]any, id string) map[string]any {
	return discoveryService(m, id)["endpoints"].([]any)[0].(map[string]any)
}

// The fixture is produced by Interceptor's exporter at the approved source,
// including native ordering/omitempty hash input; not synthesized from OpenAPI.
func TestInterceptorContractDiscoveryCapabilities(t *testing.T) {
	raw := discoveryFixture(t)
	d, err := contractcheck.Load("../interceptor/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Validate("#/components/schemas/adaptive.CapabilityManifest", raw); err != nil {
		t.Fatal(err)
	}
	e, err := FromNative(catalogForTest(t), raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	n := e.native
	n.Digest = ""
	expected, err := os.ReadFile("../../schemas/fixtures/discoverable-injection-targets/native-digest-input.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshal(t, n), expected) {
		t.Fatal("native endpoint serialization differs")
	}
	public, err := os.ReadFile("../../schemas/fixtures/discoverable-injection-targets/public-capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, e.PublicJSON(), public)
	if _, err = Import(catalogForTest(t), public, raw, "delivery-example"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte("not-exported")) {
		t.Fatal("private values exposed")
	}
	for _, svc := range e.ExecutionFacts().Services {
		if (svc.ID == "static" || svc.ID == "head-only") && (len(svc.Endpoints) != 0 || len(svc.InjectionSurfaces) != 0) {
			t.Fatal("unselectable sink advertises routes")
		}
		if svc.ID == "restricted" {
			svc.Endpoints[0].Methods[0] = "CHANGED"
		}
	}
	if bytes.Contains(marshal(t, e.ExecutionFacts()), []byte("CHANGED")) {
		t.Fatal("mutable metadata alias")
	}
}

func TestDiscoveryMetadataRejections(t *testing.T) {
	changes := map[string]func(map[string]any){
		"unknown field":      func(m map[string]any) { discoveryEndpoint(m, "restricted")["unknown"] = true },
		"duplicate method":   func(m map[string]any) { discoveryEndpoint(m, "restricted")["methods"] = []string{"GET", "GET"} },
		"HEAD":               func(m map[string]any) { discoveryEndpoint(m, "restricted")["methods"] = []string{"HEAD"} },
		"bad method":         func(m map[string]any) { discoveryEndpoint(m, "restricted")["methods"] = []string{"GET bad"} },
		"lowercase method":   func(m map[string]any) { discoveryEndpoint(m, "restricted")["methods"] = []string{"get"} },
		"relative path":      func(m map[string]any) { discoveryEndpoint(m, "restricted")["paths"] = []string{"collect"} },
		"query in path":      func(m map[string]any) { discoveryEndpoint(m, "restricted")["paths"] = []string{"/collect?key=1"} },
		"wrong root":         func(m map[string]any) { discoveryEndpoint(m, "restricted")["injection_root"] = "service-record" },
		"mixed route fields": func(m map[string]any) { discoveryEndpoint(m, "restricted")["method"] = "GET" },
		"missing delivery":   func(m map[string]any) { delete(discoveryEndpoint(m, "restricted"), "delivery") },
		"empty keys":         func(m map[string]any) { delete(discoveryEndpoint(m, "idp"), "keys") },
		"duplicate key":      func(m map[string]any) { discoveryEndpoint(m, "idp")["keys"] = []string{"delegation", "delegation"} },
		"empty fields":       func(m map[string]any) { delete(discoveryEndpoint(m, "idp"), "mutable_fields") },
		"mixed state fields": func(m map[string]any) { discoveryEndpoint(m, "idp")["paths"] = []string{"/"} },
		"unsupported kind":   func(m map[string]any) { discoveryEndpoint(m, "idp")["kind"] = "future-state" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(discoveryFixture(t), &m); err != nil {
				t.Fatal(err)
			}
			change(m)
			if _, err := FromNative(catalogForTest(t), resign(t, marshal(t, m)), "delivery-example"); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	raw := bytes.Replace(discoveryFixture(t), []byte("/collect"), []byte("/changed"), 1)
	if _, err := FromNative(catalogForTest(t), raw, "delivery-example"); err == nil {
		t.Fatal("modified hash accepted")
	}
}
