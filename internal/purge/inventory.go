//go:build linux || darwin

// Package purge retires and removes only host-recorded campaign groups. It never
// interprets harness paths as deletion authority or resumes interrupted work.
package purge

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

var ErrUnconfirmed = errors.New("purge preflight could not confirm inactive managed resources")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var startPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

const maxEntries = 100000

type Docker interface {
	CheckInactive(context.Context, campaign.DockerBinding) dockercontrol.Inactivity
	RemoveStopped(context.Context, campaign.DockerBinding) error
}
type Selection struct {
	CampaignID string
	All        bool
}
type Group struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Device  uint64 `json:"device"`
	Inode   uint64 `json:"inode"`
	Entries int64  `json:"entries"`
	Bytes   int64  `json:"size_bytes"`
}
type Start struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type Inventory struct {
	CampaignID string                  `json:"campaign_id"`
	Groups     []Group                 `json:"groups"`
	Starts     []Start                 `json:"starts"`
	Binding    *campaign.DockerBinding `json:"docker_binding,omitempty"`
	Inactivity string                  `json:"inactivity"`
}

// Plan holds all exclusions until deletion finishes or the caller closes it.
// The MVP serializes retirement across one installed state root.
type Plan struct {
	Root        string
	Inventories []Inventory
	retention   *campaign.RetentionLease
	host        *campaign.HostLease
	guards      []*campaign.PurgeGuard
	runs        []*startrequest.RunLock
	docker      Docker
	closed      bool
	remove      func(context.Context, Group) error
}

func (p *Plan) Close() error {
	if p == nil || p.closed {
		return nil
	}
	p.closed = true
	var errs []error
	for _, l := range p.runs {
		errs = append(errs, l.Close())
	}
	for _, l := range p.guards {
		errs = append(errs, l.Close())
	}
	errs = append(errs, p.host.Close(), p.retention.Close())
	return errors.Join(errs...)
}
func validID(s string) bool { return idPattern.MatchString(s) && s != "." && s != ".." }
func absolute(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && s != "/" && len(s) <= 4096 && !strings.ContainsAny(s, "\x00\r\n")
}
func privateDir(root *os.Root, name string) error {
	i, e := root.Lstat(name)
	if e != nil {
		return e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.IsDir() || i.Mode().Perm()&0077 != 0 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
		return ErrUnconfirmed
	}
	return nil
}
func names(root *os.Root, path string) ([]string, error) {
	if e := privateDir(root, path); errors.Is(e, os.ErrNotExist) {
		return []string{}, nil
	} else if e != nil {
		return nil, e
	}
	d, e := root.Open(path)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	ns, e := d.Readdirnames(maxEntries + 1)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(ns) > maxEntries {
		return nil, ErrUnconfirmed
	}
	sort.Strings(ns)
	return ns, nil
}

func Prepare(ctx context.Context, root string, selection Selection, docker Docker) (plan *Plan, err error) {
	if !absolute(root) || selection.All == (selection.CampaignID != "") || (!selection.All && !validID(selection.CampaignID)) {
		return nil, campaign.ErrInvalid
	}
	p := &Plan{Root: root, docker: docker, Inventories: []Inventory{}}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	p.retention, err = campaign.AcquireRetentionLease(root, true)
	if err != nil {
		return nil, err
	}
	p.host, err = campaign.AcquireHostLease(root)
	if err != nil {
		return nil, err
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	inventories := map[string]*Inventory{}
	add := func(id string) *Inventory {
		if inventories[id] == nil {
			inventories[id] = &Inventory{CampaignID: id, Groups: []Group{}, Starts: []Start{}, Inactivity: "never-created"}
		}
		return inventories[id]
	}
	selected := func(id string) bool { return selection.All || id == selection.CampaignID }
	// Pending transactions retain exact identity for retry even if ordinary
	// campaign/start records have already been removed.
	pending, e := readPending(r, root)
	if e != nil {
		return nil, e
	}
	purgeIDs, e := names(r, "purges")
	if e != nil {
		return nil, e
	}
	for _, id := range purgeIDs {
		if !validID(id) {
			return nil, ErrUnconfirmed
		}
		if selected(id) {
			add(id)
		}
	}
	covered := map[string]bool{}
	for id, v := range pending {
		if selected(id) {
			copy := v
			inventories[id] = &copy
		}
		for _, s := range v.Starts {
			covered[s.ID] = true
		}
	}
	for _, kind := range []string{"campaigns", "attachments", "managed-copies"} {
		ids, e := names(r, kind)
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			if !validID(id) {
				return nil, ErrUnconfirmed
			}
			if !selected(id) || pending[id].CampaignID != "" {
				continue
			}
			if e = privateDir(r, kind+"/"+id); e != nil {
				return nil, e
			}
			v := add(id)
			if kind != "managed-copies" {
				guard, e := campaign.LockPurgeGroup(p.retention, root, kind, id)
				if e != nil {
					return nil, e
				}
				p.guards = append(p.guards, guard)
				if kind == "campaigns" {
					v.Binding, e = guard.DockerIdentity(ctx, id)
					if e != nil {
						return nil, e
					}
				}
			}
			g, e := inventoryGroup(ctx, kind, filepath.Join(root, kind, id))
			if e != nil {
				return nil, e
			}
			v.Groups = append(v.Groups, g)
			if kind == "managed-copies" {
				copies, e := registeredCopies(ctx, root, id)
				if e != nil {
					return nil, e
				}
				v.Groups = append(v.Groups, copies...)
			}
		}
	}
	starts, e := startrequest.InventoryExcept(root, covered)
	if e != nil {
		return nil, e
	}
	lockedRuns := map[string]bool{}
	for _, s := range starts {
		id := s.Request.Selection.CampaignID
		if !selected(id) {
			continue
		}
		v := add(id)
		run := s.Request.Selection.RunDirectory
		if !lockedRuns[run] {
			l, e := startrequest.LockRun(run)
			// Removed source projects are not retained campaign data. Other failures
			// (including a symlink/damaged lock) remain uncertainty.
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
			if l != nil {
				p.runs = append(p.runs, l)
			}
			lockedRuns[run] = true
		}
		g, e := inventoryGroup(ctx, "starts", filepath.Join(root, "starts", s.Request.Selection.StartRequestID))
		if e != nil {
			return nil, e
		}
		v.Groups = append(v.Groups, g)
		v.Starts = append(v.Starts, Start{s.Request.Selection.StartRequestID, s.Digest})
	}
	if !selection.All {
		add(selection.CampaignID)
	}
	ids := make([]string, 0, len(inventories))
	for id := range inventories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		v := inventories[id]
		if v.Binding != nil {
			if docker == nil {
				return nil, ErrUnconfirmed
			}
			state := docker.CheckInactive(ctx, *v.Binding)
			if !state.Confirmed {
				return nil, ErrUnconfirmed
			}
			v.Inactivity = state.State
		}
		// Revalidate saved paths and inode identity on every partial-deletion retry.
		for _, g := range v.Groups {
			if e = verifyGroup(ctx, g, true); e != nil {
				return nil, e
			}
		}
		if e = validateInventory(root, *v); e != nil {
			return nil, e
		}
		p.Inventories = append(p.Inventories, *v)
	}
	return p, nil
}

func inventoryGroup(ctx context.Context, kind, path string) (Group, error) {
	g := Group{Kind: kind, Path: path}
	parent, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return g, e
	}
	defer parent.Close()
	i, e := parent.Lstat(filepath.Base(path))
	if e != nil {
		return g, e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.IsDir() {
		return g, ErrUnconfirmed
	}
	g.Device, g.Inode = uint64(st.Dev), uint64(st.Ino)
	e = scan(ctx, parent, filepath.Base(path), g.Device, 0, &g)
	return g, e
}
func verifyGroup(ctx context.Context, g Group, missingOK bool) error {
	if !absolute(g.Path) {
		return ErrUnconfirmed
	}
	current, e := inventoryGroup(ctx, g.Kind, g.Path)
	if missingOK && errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if current.Device != g.Device || current.Inode != g.Inode {
		return ErrUnconfirmed
	}
	return nil
}
func scan(ctx context.Context, parent *os.Root, name string, device uint64, depth int, g *Group) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	g.Entries++
	if g.Entries > 1000000 || depth > 128 {
		return ErrUnconfirmed
	}
	i, e := parent.Lstat(name)
	if e != nil {
		return e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || uint64(st.Dev) != device {
		return ErrUnconfirmed
	}
	if !i.IsDir() {
		if i.Mode().IsRegular() {
			g.Bytes += i.Size()
		}
		return nil
	}
	r, e := parent.OpenRoot(name)
	if e != nil {
		return e
	}
	defer r.Close()
	actual, e := r.Stat(".")
	if e != nil || !os.SameFile(i, actual) {
		return ErrUnconfirmed
	}
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	for {
		ns, e := f.Readdirnames(128)
		for _, n := range ns {
			if err := scan(ctx, r, n, device, depth+1, g); err != nil {
				return err
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}
