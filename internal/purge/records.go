//go:build linux || darwin

package purge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

const recordLimit = 1 << 20
const planVersion = "operator.dev/purge-plan/v1alpha1"

type planRecord struct {
	APIVersion string    `json:"api_version"`
	Root       string    `json:"state_root"`
	Inventory  Inventory `json:"inventory"`
}
type envelope struct {
	Value  json.RawMessage `json:"value"`
	Digest string          `json:"digest"`
}

func readRecord(path string, v any) error {
	raw, e := hostconfig.ReadPrivate(path, recordLimit)
	if e != nil {
		return e
	}
	var box envelope
	if interceptor.DecodeTypedBody(raw, &box, recordLimit) != nil {
		return ErrUnconfirmed
	}
	d, e := contracts.CanonicalDigest(box.Value, recordLimit)
	if e != nil || d != box.Digest {
		return ErrUnconfirmed
	}
	return interceptor.DecodeTypedBody(box.Value, v, recordLimit)
}
func syncDir(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func mkdir(root *os.Root, name string) error {
	if e := root.Mkdir(name, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return e
	}
	if e := privateDir(root, name); e != nil {
		return e
	}
	parent, e := root.OpenRoot(filepath.Dir(name))
	if e != nil {
		return e
	}
	defer parent.Close()
	return syncDir(parent)
}

// Publication is atomic and durable before callers remove anything. A partial
// pending plan is never authority to delete; fresh preflight may replace it.
func publish(ctx context.Context, path string, value any) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	raw, e = contracts.Canonicalize(raw, recordLimit)
	if e != nil {
		return e
	}
	boxed, e := json.Marshal(envelope{raw, contracts.RawDigest(raw)})
	if e != nil || len(boxed) > recordLimit {
		return ErrUnconfirmed
	}
	parent, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = privateDir(parent, "."); e != nil {
		return e
	}
	name := filepath.Base(path)
	if _, e = parent.Lstat(name); !errors.Is(e, os.ErrNotExist) {
		return ErrUnconfirmed
	}
	// This pending name is owned exclusively by retention/managed-copy locks.
	if e = parent.Remove(name + ".pending"); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	f, e := parent.OpenFile(name+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(boxed)
	e = errors.Join(e, f.Sync(), f.Close())
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = parent.Rename(name+".pending", name); e != nil {
		return e
	}
	return syncDir(parent)
}
func readPending(root *os.Root, path string) (map[string]Inventory, error) {
	ids, e := names(root, "purges")
	if e != nil {
		return nil, e
	}
	out := map[string]Inventory{}
	for _, id := range ids {
		if !validID(id) {
			return nil, ErrUnconfirmed
		}
		if e = privateDir(root, "purges/"+id); e != nil {
			return nil, e
		}
		var rec planRecord
		e = readRecord(filepath.Join(path, "purges", id, "plan.json"), &rec)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if rec.APIVersion != planVersion || rec.Root != path || rec.Inventory.CampaignID != id || validateInventory(path, rec.Inventory) != nil {
			return nil, ErrUnconfirmed
		}
		out[id] = rec.Inventory
	}
	return out, nil
}
func validateInventory(root string, v Inventory) error {
	if !validID(v.CampaignID) || len(v.Groups) > maxEntries || len(v.Starts) > maxEntries {
		return ErrUnconfirmed
	}
	if v.Binding != nil && (v.Binding.Validate() != nil || v.Binding.CampaignID != v.CampaignID) {
		return ErrUnconfirmed
	}
	seen := map[string]bool{}
	starts := map[string]bool{}
	for _, s := range v.Starts {
		if !startPattern.MatchString(s.ID) || !digestPattern.MatchString(s.Digest) || starts[s.ID] {
			return ErrUnconfirmed
		}
		starts[s.ID] = true
	}
	for _, g := range v.Groups {
		if !absolute(g.Path) || seen[g.Path] || g.Inode == 0 || g.Entries < 1 || g.Bytes < 0 {
			return ErrUnconfirmed
		}
		seen[g.Path] = true
		switch g.Kind {
		case "campaigns", "attachments", "managed-copies":
			if g.Path != filepath.Join(root, g.Kind, v.CampaignID) {
				return ErrUnconfirmed
			}
		case "starts":
			if !starts[filepath.Base(g.Path)] || g.Path != filepath.Join(root, "starts", filepath.Base(g.Path)) {
				return ErrUnconfirmed
			}
		case "managed-copy":
			if filepath.Base(g.Path) != v.CampaignID || filepath.Base(filepath.Dir(g.Path)) != "operator-campaigns" {
				return ErrUnconfirmed
			}
		default:
			return ErrUnconfirmed
		}
	}
	return nil
}
