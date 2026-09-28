//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"os"
)

// PurgeGuard retains the existing writer inode throughout preflight/deletion.
// It does not require evidence integrity when an independent valid Docker binding
// establishes the container identity; missing identity requires a complete journal
// proving that no create was dispatched.
type PurgeGuard struct {
	root *os.Root
	file *os.File
}

func (g *PurgeGuard) Close() error { return errors.Join(g.file.Close(), g.root.Close()) }
func LockPurgeGroup(lease *RetentionLease, stateRoot, kind, id string) (*PurgeGuard, error) {
	if !lease.ExclusiveFor(stateRoot) || !validID(id) || (kind != "campaigns" && kind != "attachments") {
		return nil, ErrInvalid
	}
	state, e := os.OpenRoot(stateRoot)
	if e != nil {
		return nil, e
	}
	defer state.Close()
	for _, p := range []string{".", kind, kind + "/" + id} {
		if e = privateDir(state, p); e != nil {
			return nil, e
		}
	}
	r, e := state.OpenRoot(kind + "/" + id)
	if e != nil {
		return nil, e
	}
	l, e := lock(r, false)
	if e != nil {
		r.Close()
		return nil, e
	}
	return &PurgeGuard{r, l}, nil
}
func (g *PurgeGuard) DockerIdentity(ctx context.Context, id string) (*DockerBinding, error) {
	raw, e := readFile(g.root, "launch/docker-binding.json", ManifestLimit)
	if e == nil {
		var b DockerBinding
		if decode(raw, &b, ManifestLimit) != nil || b.Validate() != nil || b.CampaignID != id {
			return nil, ErrCorrupt
		}
		return &b, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	started := false
	report, e := inspectRoot(ctx, g.root, id, func(v Event) error {
		if v.Kind == "launch.start-intent" {
			started = true
		}
		return nil
	}, false)
	if e != nil || !report.JournalIntact || started {
		return nil, ErrCorrupt
	}
	return nil, nil
}

// RemovePurgeTree reuses the bounded no-follow, same-device transient traversal.
// It MUST only be called on a preflighted, exclusively owned managed group.
func RemovePurgeTree(ctx context.Context, parent *os.Root, name string, device uint64) error {
	budget := 1000000
	if e := removeTransientTree(ctx, parent, name, device, 0, &budget); e != nil {
		return e
	}
	return syncDir(parent, ".")
}
