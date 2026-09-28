package capabilities

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

// Save publishes the native companion before the public document. Existing
// public exports are never overwritten; a shared companion must match exactly.
// Failure may leave incomplete files, which normal import validation rejects.
func (e *Export) Save(ctx context.Context, publicPath string) error {
	if e == nil || !filepath.IsAbs(publicPath) || filepath.Clean(publicPath) != publicPath || filepath.Base(publicPath) == e.CompanionName() {
		return ErrProjection
	}
	r, err := os.OpenRoot(filepath.Dir(publicPath))
	if err != nil {
		return err
	}
	defer r.Close()
	if _, err = r.Lstat(filepath.Base(publicPath)); !errors.Is(err, os.ErrNotExist) {
		return ErrProjection
	}
	write := func(name string, raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(raw)
		if err == nil {
			err = f.Sync()
		}
		return errors.Join(err, f.Close())
	}
	if err = write(e.CompanionName(), e.raw); errors.Is(err, os.ErrExist) {
		var raw []byte
		raw, err = staging.Capture(ctx, filepath.Join(filepath.Dir(publicPath), e.CompanionName()), contracts.OrdinaryLimit)
		if err == nil && !bytes.Equal(raw, e.raw) {
			err = ErrProjection
		}
	}
	if err != nil {
		return err
	}
	if err = write(filepath.Base(publicPath), e.public); err != nil {
		return err
	}
	f, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

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
