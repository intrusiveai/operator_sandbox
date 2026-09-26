package capabilities

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

// LoadExport reads a public export and its hash-named sibling companion as
// bounded regular files. Capture rejects leaf symlinks, hard links, special files
// and changes during reading. No document-controlled URL/path is followed.
func LoadExport(ctx context.Context, catalog *contracts.Catalog, publicPath, targetID string) (*Export, error) {
	if catalog == nil {
		return nil, contracts.ErrCatalog
	}
	raw, err := staging.Capture(ctx, publicPath, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	value, err := catalog.Validate(contracts.TargetCapabilityManifestSchema, raw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	digest := value.(map[string]any)["source"].(map[string]any)["raw_digest"].(string)
	name := "sha256-" + strings.TrimPrefix(digest, "sha256:") + ".json"
	native, err := staging.Capture(ctx, filepath.Join(filepath.Dir(publicPath), name), contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	return Import(catalog, raw, native, targetID)
}
