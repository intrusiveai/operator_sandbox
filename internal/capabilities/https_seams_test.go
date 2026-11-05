package capabilities

import (
	"bytes"
	"os"
	"testing"
)

func TestHTTPSSeamsPublishedNativeCapture(t *testing.T) {
	// An actual published Interceptor export, not Operator's own serializer,
	// fixes the field order used by the native digest calculation.
	raw, err := os.ReadFile("testdata/https-seams-native.json")
	if err != nil {
		t.Fatal(err)
	}
	c := catalogForTest(t)
	e, err := FromNative(c, raw, "qualification")
	if err != nil {
		t.Fatal(err)
	}
	if e.SourceDigest() != "sha256:c1d82dd91365f4b49eaf88497e18c0293ecd7136dfb3cf113a5d7f68e351319e" || len(e.native.Network.HTTPSSeams) != 1 {
		t.Fatal("native identity or declaration changed")
	}
	if _, err := Import(c, e.PublicJSON(), raw, "qualification"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(e.PublicJSON(), []byte("qualification-guardrail")) || bytes.Contains(e.PublicJSON(), []byte("https_seams")) {
		t.Fatal("host-managed seam exposed in public capabilities")
	}
}

func seamVariant(t *testing.T, change func(map[string]any, map[string]any)) []byte {
	t.Helper()
	return nativeVariant(t, func(v map[string]any) {
		network := v["network"].(map[string]any)
		network["dns"].(map[string]any)["enabled"] = true
		network["https_frontdoor"].(map[string]any)["enabled"] = true
		seam := map[string]any{"id": "external-guardrail", "maximum_message_bytes": 1048576}
		network["https_seams"] = []any{seam}
		if change != nil {
			change(network, seam)
		}
	})
}

func TestHTTPSSeamsRetainProvenanceWithoutGrantingHarnessCapabilities(t *testing.T) {
	c := catalogForTest(t)
	raw := seamVariant(t, nil)
	export, err := FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, export.NativeJSON()) || len(export.native.Network.HTTPSSeams) != 1 {
		t.Fatal("native seam declaration lost")
	}
	before, err := FromNative(c, fixture(t, "interceptor-export.json"), "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	if before.SourceDigest() == export.SourceDigest() || before.ProjectionDigest() != export.ProjectionDigest() {
		t.Fatal("seams must affect native identity, not injection/feedback/tool eligibility")
	}
	if bytes.Contains(export.PublicJSON(), []byte("external-guardrail")) || bytes.Contains(export.PublicJSON(), []byte("https_seams")) {
		t.Fatal("host network declaration projected to Harness")
	}
	if _, err := Import(c, export.PublicJSON(), raw, "delivery-example"); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(c, export.PublicJSON(), fixture(t, "interceptor-export.json"), "delivery-example"); err == nil {
		t.Fatal("accepted another native companion")
	}
	changed := object(t, raw)
	changed["network"].(map[string]any)["https_seams"].([]any)[0].(map[string]any)["maximum_message_bytes"] = 2048
	if _, err := FromNative(c, marshal(t, changed), "delivery-example"); err == nil {
		t.Fatal("accepted tampered native digest")
	}
}

func TestHTTPSSeamsRejectMalformedDeclarations(t *testing.T) {
	c := catalogForTest(t)
	for name, change := range map[string]func(map[string]any, map[string]any){
		"duplicate":       func(n, s map[string]any) { n["https_seams"] = []any{s, s} },
		"empty id":        func(n, s map[string]any) { s["id"] = "" },
		"invalid id":      func(n, s map[string]any) { s["id"] = "External/URL" },
		"reserved id":     func(n, s map[string]any) { s["id"] = "model-proxy" },
		"zero limit":      func(n, s map[string]any) { s["maximum_message_bytes"] = 0 },
		"excessive limit": func(n, s map[string]any) { s["maximum_message_bytes"] = (256 << 20) + 1 },
		"no dns":          func(n, s map[string]any) { n["dns"].(map[string]any)["enabled"] = false },
		"no tls":          func(n, s map[string]any) { n["https_frontdoor"].(map[string]any)["enabled"] = false },
		"host origin":     func(n, s map[string]any) { s["origin"] = "https://private.invalid" },
		"credentials":     func(n, s map[string]any) { s["credential"] = "synthetic-secret" },
		"missing limit":   func(n, s map[string]any) { delete(s, "maximum_message_bytes") },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FromNative(c, seamVariant(t, change), "delivery-example"); err == nil {
				t.Fatal("accepted invalid seam capability")
			}
		})
	}
}
