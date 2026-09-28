//go:build linux || darwin

package purge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

const copyVersion = "operator.dev/managed-campaign-copy/v1alpha1"

type copyRecord struct {
	APIVersion string `json:"api_version"`
	StateRoot  string `json:"state_root"`
	CampaignID string `json:"campaign_id"`
	Directory  string `json:"directory"`
}

// ManagedCopy holds campaign/retention exclusion while a host producer writes
// into a dedicated managed output subtree. Explicit export never uses this API.
type ManagedCopy struct {
	Directory string
	guard     *campaign.NativeRecovery
}

func (c *ManagedCopy) Close() error { return c.guard.Close() }

// BeginManagedCopy registers an empty dedicated subtree before accepting bytes.
// Run inputs, start links and explicit external exports are never registered.
func BeginManagedCopy(ctx context.Context, root, id, output string) (copy *ManagedCopy, err error) {
	if !absolute(root) || !validID(id) || !absolute(output) {
		return nil, campaign.ErrInvalid
	}
	guard, e := campaign.OpenNativeRecovery(root, id)
	if e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			guard.Close()
		}
	}()
	output, e = filepath.EvalSymlinks(output)
	if e != nil {
		return nil, e
	}
	state, e := filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(state, output)
	if e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return nil, campaign.ErrInvalid
	}
	parent, e := os.OpenRoot(output)
	if e != nil {
		return nil, e
	}
	defer parent.Close()
	if e = privateDir(parent, "."); e != nil {
		return nil, e
	}
	if e = mkdir(parent, "operator-campaigns"); e != nil {
		return nil, e
	}
	r, e := parent.OpenRoot("operator-campaigns")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if _, e = r.Lstat(id); !errors.Is(e, os.ErrNotExist) {
		return nil, ErrUnconfirmed
	}
	path := filepath.Join(output, "operator-campaigns", id)
	rec := copyRecord{copyVersion, root, id, path}
	registry, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer registry.Close()
	if e = mkdir(registry, "managed-copies"); e != nil {
		return nil, e
	}
	if e = mkdir(registry, "managed-copies/"+id); e != nil {
		return nil, e
	}
	key := contracts.RawDigest([]byte(path))[7:] + ".json"
	if e = publish(ctx, filepath.Join(root, "managed-copies", id, key), rec); e != nil {
		return nil, e
	}
	if e = mkdir(r, id); e != nil {
		return nil, e
	}
	if e = publish(ctx, filepath.Join(path, "managed.json"), rec); e != nil {
		return nil, e
	}
	return &ManagedCopy{path, guard}, nil
}
func registeredCopies(ctx context.Context, root, id string) ([]Group, error) {
	registry, e := os.OpenRoot(filepath.Join(root, "managed-copies", id))
	if e != nil {
		return nil, e
	}
	defer registry.Close()
	ns, e := names(registry, ".")
	if e != nil {
		return nil, e
	}
	groups := []Group{}
	for _, name := range ns {
		var rec copyRecord
		if e = readRecord(filepath.Join(root, "managed-copies", id, name), &rec); e != nil {
			return nil, e
		}
		if rec.APIVersion != copyVersion || rec.StateRoot != root || rec.CampaignID != id || !absolute(rec.Directory) || filepath.Base(rec.Directory) != id || filepath.Base(filepath.Dir(rec.Directory)) != "operator-campaigns" || name != contracts.RawDigest([]byte(rec.Directory))[7:]+".json" {
			return nil, ErrUnconfirmed
		}
		if _, e = os.Lstat(rec.Directory); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return nil, e
		}
		// Both independent host registration and the dedicated copy marker must agree.
		var marker copyRecord
		if e = readRecord(filepath.Join(rec.Directory, "managed.json"), &marker); e != nil || marker != rec {
			return nil, ErrUnconfirmed
		}
		g, e := inventoryGroup(ctx, "managed-copy", rec.Directory)
		if e != nil {
			return nil, e
		}
		groups = append(groups, g)
	}
	return groups, nil
}
