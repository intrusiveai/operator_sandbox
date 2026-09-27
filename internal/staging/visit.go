//go:build linux || darwin

package staging

import (
	"context"
	"sort"
)

// Visit reads mounted inventory files in canonical path order; the private
// wrapper receipt is verified but is not part of the mounted content inventory. Each callback
// receives verified, independently owned bytes. A final inventory check is required
// before callers publish a completion marker; any failure leaves the visit partial.
func (t *Tree) Visit(ctx context.Context, consume func(string, []byte) error) error {
	if consume == nil {
		return ErrInputs
	}
	if err := t.Verify(ctx); err != nil {
		return err
	}
	root, err := t.open()
	if err != nil {
		return err
	}
	defer root.Close()
	names := make([]string, 0, len(t.files))
	for name := range t.files {
		if name != "staging-receipt.json" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		want := t.files[name]
		raw, err := read(ctx, root, name, want.size, &want)
		if err != nil {
			return err
		}
		if err = consume(name, raw); err != nil {
			return err
		}
	}
	return t.Verify(ctx)
}
