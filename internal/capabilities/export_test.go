package capabilities

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func catalogForTest(t *testing.T) *contracts.Catalog {
	t.Helper()
	c, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../schemas/fixtures/capability-chain/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	v, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}
func resign(t *testing.T, raw []byte) []byte {
	t.Helper()
	var n nativeManifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&n); err != nil {
		t.Fatal(err)
	}
	n.Digest = ""
	digest := contracts.RawDigest(marshal(t, n))
	v := object(t, raw)
	v["digest"] = digest
	return marshal(t, v)
}
func nativeVariant(t *testing.T, change func(map[string]any)) []byte {
	t.Helper()
	v := object(t, fixture(t, "interceptor-export.json"))
	change(v)
	return resign(t, marshal(t, v))
}
func equalJSON(t *testing.T, a, b []byte) {
	t.Helper()
	ca, err := contracts.Canonicalize(a, contracts.OrdinaryLimit)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := contracts.Canonicalize(b, contracts.OrdinaryLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ca, cb) {
		t.Fatalf("JSON differs\ngot %s\nwant %s", ca, cb)
	}
}

func TestNativeGoldenChain(t *testing.T) {
	c := catalogForTest(t)
	raw := fixture(t, "interceptor-export.json")
	e, err := FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	n := e.native
	n.Digest = ""
	if !bytes.Equal(marshal(t, n), fixture(t, "native-digest-input.json")) {
		t.Fatal("native digest serialization drift")
	}
	equalJSON(t, e.PublicJSON(), fixture(t, "public-capabilities.json"))
	projected, err := contracts.Canonicalize(marshal(t, e.projection), contracts.OrdinaryLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(projected, fixture(t, "projection-canonical.json")) {
		t.Fatal("JCS projection drift")
	}
	if _, err = Import(c, e.PublicJSON(), raw, e.TargetID()); err != nil {
		t.Fatal(err)
	}
	if e.CompanionName() != "sha256-"+strings.TrimPrefix(contracts.RawDigest(raw), "sha256:")+".json" {
		t.Fatal("companion filename")
	}
	for _, key := range []string{"method", "path", "network", "model_providers", "cross_vm_operations"} {
		if _, ok := e.projection[key]; ok {
			t.Fatal("host-only field exposed")
		}
		if _, ok := e.projection["operations"].([]any)[0].(map[string]any)[key]; ok {
			t.Fatal("native route exposed")
		}
	}
	raw[0] = '!'
	a := e.NativeJSON()
	a[0] = '!'
	b := e.PublicJSON()
	b[0] = '!'
	if e.NativeJSON()[0] != '{' || e.PublicJSON()[0] != '{' {
		t.Fatal("mutable export")
	}
}
func TestNativeRejections(t *testing.T) {
	c := catalogForTest(t)
	cases := map[string]func(map[string]any){
		"version":             func(v map[string]any) { v["api_version"] = "future" },
		"profile":             func(v map[string]any) { v["delivery_schema_profile"] = "future" },
		"unknown":             func(v map[string]any) { v["network"].(map[string]any)["secret"] = true },
		"case alias":          func(v map[string]any) { v["DIGEST"] = v["digest"] },
		"null scalar":         func(v map[string]any) { v["snapshot_capable"] = nil },
		"missing scalar":      func(v map[string]any) { delete(v, "snapshot_capable") },
		"duplicate operation": func(v map[string]any) { v["operations"] = append(v["operations"].([]any), v["operations"].([]any)[0]) },
		"duplicate endpoint": func(v map[string]any) {
			s := records(v["services"])[0]
			s["endpoints"] = append(s["endpoints"].([]any), s["endpoints"].([]any)[0])
		},
		"unknown surface":             func(v map[string]any) { records(v["injection_profiles"])[0]["surface"] = "future" },
		"unknown placement":           func(v map[string]any) { records(v["injection_profiles"])[0]["placements"] = []string{"execute"} },
		"unexpected provider catalog": func(v map[string]any) { v["model_providers"] = []any{} },
		"obsolete version":            func(v map[string]any) { v["api_version"] = "interceptor.dev/capability-manifest/v1alpha2" },
		"unknown feedback":            func(v map[string]any) { v["feedback"].(map[string]any)["kinds"] = []string{"future"} },
		"invalid delivery": func(v map[string]any) {
			records(v["operations"])[0]["delivery"].(map[string]any)["input"].(map[string]any)["schema"] = map[string]any{"$ref": "https://invalid.example/schema"}
		},
		"delivery status": func(v map[string]any) { records(v["operations"])[0]["delivery_status"] = "missing-input-contract" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw := nativeVariant(t, change)
			if _, err := FromNative(c, raw, "delivery-example"); err == nil {
				t.Fatal("accepted invalid native export")
			}
		})
	}
	raw := fixture(t, "interceptor-export.json")
	for _, bad := range [][]byte{bytes.Replace(raw, []byte("synthetic agent"), []byte("different agent"), 1), append([]byte(`{"kind":"x",`), raw[1:]...), []byte(`{"kind":"\ud800"}`), bytes.Repeat([]byte(" "), contracts.OrdinaryLimit+1)} {
		if _, err := FromNative(c, bad, "delivery-example"); err == nil {
			t.Fatal("accepted malformed native bytes")
		}
	}
}
func TestImportRejectsTampering(t *testing.T) {
	c := catalogForTest(t)
	native := fixture(t, "interceptor-export.json")
	raw := fixture(t, "public-capabilities.json")
	for _, change := range []func(map[string]any){
		func(v map[string]any) {
			v["source"].(map[string]any)["raw_digest"] = "sha256:" + strings.Repeat("0", 64)
		},
		func(v map[string]any) { v["capability_projection_digest"] = "sha256:" + strings.Repeat("0", 64) },
		func(v map[string]any) {
			records(v["capabilities"].(map[string]any)["operations"])[0]["ref"] = "operation:other"
		},
		func(v map[string]any) {
			p := v["capabilities"].(map[string]any)
			records(p["operations"])[0]["delivery"].(map[string]any)["description"] = "Forged"
			d, _ := contracts.CanonicalDigest(marshal(t, p), contracts.OrdinaryLimit)
			v["capability_projection_digest"] = d
		},
	} {
		v := object(t, raw)
		change(v)
		if _, err := Import(c, marshal(t, v), native, "delivery-example"); err == nil {
			t.Fatal("accepted forged public export")
		}
	}
	if _, err := Import(c, raw, append(native, ' '), "delivery-example"); err == nil {
		t.Fatal("ignored companion raw hash")
	}
}
func TestMissingDeliveryAndProjectionNormalization(t *testing.T) {
	c := catalogForTest(t)
	raw := nativeVariant(t, func(v map[string]any) {
		delete(v, "delivery_schema_profile")
		for _, op := range records(v["operations"]) {
			delete(op, "delivery")
			delete(op, "delivery_status")
			delete(op, "maximum_input_bytes")
		}
		for _, svc := range records(v["services"]) {
			delete(svc, "endpoints")
			delete(svc, "implementation")
		}
		v["services"] = nil
		v["file_namespaces"] = nil
	})
	e, err := FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.projection["services"].([]any)) != 0 || e.projection["operations"].([]any)[0].(map[string]any)["delivery_status"] != "missing-input-contract" {
		t.Fatal("legacy normalization")
	}
	// A source-only change preserves the static projection; benign schema values
	// use full Unicode/JCS while native hashing preserves HTML escapes and numbers.
	base, err := FromNative(c, fixture(t, "interceptor-export.json"), "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	raw = nativeVariant(t, func(v map[string]any) {
		v["interceptor_release"] = "new"
		v["target"].(map[string]any)["version"] = "new"
		v["cross_vm_operations"] = append(v["cross_vm_operations"].([]any), "snapshot.create")
	})
	changed, err := FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	if base.ProjectionDigest() != changed.ProjectionDigest() || base.SourceDigest() == changed.SourceDigest() {
		t.Fatal("volatile provenance affected projection")
	}
	raw = nativeVariant(t, func(v map[string]any) {
		d := records(v["operations"])[0]["delivery"].(map[string]any)
		d["description"] = "Unicode 界 😀 <>&"
		d["input"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["optional"] = map[string]any{"type": "number", "minimum": json.Number("0.5")}
	})
	if _, err := FromNative(c, raw, "delivery-example"); err != nil {
		t.Fatal(err)
	}
}
