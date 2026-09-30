package capabilities

import (
	"os"
	"testing"
)

func TestExpandedInterceptorAuthenticationExport(t *testing.T) {
	raw, err := os.ReadFile("../interceptor/testdata/capability-authentication.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := verifyNative(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.ModelProviders) != 8 {
		t.Fatal("missing provider catalog")
	}
}
