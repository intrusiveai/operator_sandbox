//go:build linux || darwin

package contractstore

import (
	"context"
	"github.com/intrusiveai/operator_sandbox/contracts"
)

// SupportedVersion and SupportedDigest are pinned by reproducible host release
// builds. Empty values identify flexible development executables, not releases.
var SupportedVersion, SupportedDigest string

// LoadRuntime rejects configuration that selects validators outside the host
// executable's supported release pin. Inspection/publication tools use Load so
// an older trusted executable can authenticate a future installation offline.
func LoadRuntime(ctx context.Context, directory string, expected contracts.PackageIdentity) (*Loaded, error) {
	if (SupportedVersion != "" || SupportedDigest != "") && (SupportedVersion != expected.Version || SupportedDigest != expected.Digest || SupportedVersion == "" || SupportedDigest == "") {
		return nil, ErrPin
	}
	return Load(ctx, directory, expected)
}
