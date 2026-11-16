package capabilities

import (
	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
	"reflect"
	"testing"
)

func TestInterceptorContractCapabilityDecoder(t *testing.T) {
	d, err := contractcheck.Load("../interceptor/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Compare(reflect.TypeFor[nativeManifest](), "#/components/schemas/adaptive.CapabilityManifest", false); err != nil {
		t.Fatal(err)
	}
	// Native bytes are captured from Interceptor, not generated from OpenAPI.
	raw := fixture(t, "interceptor-export.json")
	if err = d.Validate("#/components/schemas/adaptive.CapabilityManifest", raw); err != nil {
		t.Fatal(err)
	}
	c := catalogForTest(t)
	if _, err = FromNative(c, raw, "contract-check"); err != nil {
		t.Fatal(err)
	}
}
