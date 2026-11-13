package capabilities

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
	"os"
	"testing"
)

func TestInterceptorContractModelResponseCapabilities(t *testing.T) {
	root := "../../schemas/fixtures/model-response-injection/"
	raw, err := os.ReadFile(root + "interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
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
	expected, err := os.ReadFile(root + "native-digest-input.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshal(t, n), expected) {
		t.Fatal("native serialization changed")
	}
	public, err := os.ReadFile(root + "public-capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, e.PublicJSON(), public)
	if _, err = Import(catalogForTest(t), public, raw, "delivery-example"); err != nil {
		t.Fatal(err)
	}
}

func TestModelResponseCapabilityRejections(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/fixtures/model-response-injection/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(map[string]any){
		"missing codecs":       func(p map[string]any) { delete(p, "model_codecs") },
		"unknown codec":        func(p map[string]any) { p["model_codecs"] = []string{"embeddings"} },
		"duplicate codec":      func(p map[string]any) { p["model_codecs"] = []string{"openai_responses", "openai_responses"} },
		"wrong placement root": func(p map[string]any) { p["placement_root"] = "tool-result" },
		"next turn":            func(p map[string]any) { p["scopes"] = []string{"next_turn"} },
		"tool selector":        func(p map[string]any) { p["selector_fields"] = []string{"tool_name"} },
	} {
		t.Run(name, func(t *testing.T) {
			var n map[string]any
			if err = json.Unmarshal(raw, &n); err != nil {
				t.Fatal(err)
			}
			for _, v := range n["injection_profiles"].([]any) {
				p := v.(map[string]any)
				if p["surface"] == "model_response" {
					change(p)
				}
			}
			if _, err = FromNative(catalogForTest(t), resign(t, marshal(t, n)), "delivery-example"); err == nil {
				t.Fatal("invalid capability metadata accepted")
			}
		})
	}
	if _, err = FromNative(catalogForTest(t), bytes.Replace(raw, []byte("response-body"), []byte("changed-body"), 1), "delivery-example"); err == nil {
		t.Fatal("modified digest accepted")
	}
}
