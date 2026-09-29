//go:build linux || darwin

package contractstore

import (
	"context"
	"errors"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"testing"
)

func TestRuntimeRejectsForeignPinBeforeReadingPackage(t *testing.T) {
	oldVersion, oldDigest := SupportedVersion, SupportedDigest
	defer func() { SupportedVersion, SupportedDigest = oldVersion, oldDigest }()
	SupportedVersion, SupportedDigest = "0.0.0", contracts.RawDigest([]byte("release-approved"))
	for _, pin := range []contracts.PackageIdentity{{Version: "0.1.0", Digest: SupportedDigest}, {Version: SupportedVersion, Digest: contracts.RawDigest([]byte("different"))}} {
		if _, err := LoadRuntime(context.Background(), "/missing/distribution", pin); !errors.Is(err, ErrPin) {
			t.Fatal(err)
		}
	}
}
