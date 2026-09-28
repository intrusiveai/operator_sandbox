//go:build linux || darwin

package purge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

const Consequence = "Purged evidence can no longer generate new reports or supply retained feedback/checkpoint exports. Independent exports and Interceptor session data are preserved."

type Services interface {
	ReconcileRetired(context.Context, *campaign.RetentionLease, string, string, string) error
}
type RemovedGroup struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Status string `json:"status"`
}
type CampaignResult struct {
	CampaignID string         `json:"campaign_id"`
	Status     string         `json:"status"`
	Groups     []RemovedGroup `json:"groups"`
}
type Result struct {
	SelectionComplete bool             `json:"selection_complete"`
	APIVersion        string           `json:"api_version"`
	All               bool             `json:"all"`
	Selected          []string         `json:"selected_ids"`
	Status            string           `json:"status"`
	Code              string           `json:"code"`
	Campaigns         []CampaignResult `json:"campaigns"`
	Consequence       string           `json:"consequence"`
}

func result(selection Selection) Result {
	r := Result{APIVersion: "operator.dev/purge-result/v1alpha1", All: selection.All, Selected: []string{}, Status: "refused", Code: "preflight_unconfirmed", Campaigns: []CampaignResult{}, Consequence: Consequence}
	if selection.CampaignID != "" {
		r.Selected = append(r.Selected, selection.CampaignID)
		r.SelectionComplete = true
	}
	return r
}
func Run(ctx context.Context, root string, selection Selection, docker Docker, services Services) (Result, error) {
	if !validSelection(root, selection) {
		return result(selection), campaign.ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return result(selection), e
	}
	if _, e := os.Lstat(root); errors.Is(e, os.ErrNotExist) {
		out := result(selection)
		out.SelectionComplete = true
		out.Status, out.Code = "complete", "purged"
		if !selection.All {
			out.Campaigns = append(out.Campaigns, CampaignResult{selection.CampaignID, "already_absent", []RemovedGroup{}})
		}
		return out, nil
	}

	p, e := Prepare(ctx, root, selection, docker)
	if e != nil {
		out := refusedResult(root, selection)
		switch {
		case errors.Is(e, campaign.ErrActive):
			out.Code = "busy"
		case errors.Is(e, ErrDocker):
			out.Code = "container_inactivity_unconfirmed"
		case errors.Is(e, ErrIdentity):
			out.Code = "launch_identity_unconfirmed"
		default:
			out.Code = "inventory_unconfirmed"
		}
		return out, e
	}
	defer p.Close()
	r, e := p.Execute(ctx, services)
	r.All = selection.All
	return r, e
}

// Execute first retires all starts, confirms all service/container cleanup, and
// durably records every plan. Only then may any campaign bytes be deleted.
func (p *Plan) Execute(ctx context.Context, services Services) (out Result, err error) {
	out = result(Selection{})
	out.Code = "retirement_unconfirmed"
	out.SelectionComplete = true
	if p == nil || p.closed || !p.retention.ExclusiveFor(p.Root) {
		return out, campaign.ErrActive
	}
	for _, v := range p.Inventories {
		out.Selected = append(out.Selected, v.CampaignID)
		out.Campaigns = append(out.Campaigns, CampaignResult{v.CampaignID, "not_removed", []RemovedGroup{}})
	}
	root, e := os.OpenRoot(p.Root)
	if e != nil {
		return out, e
	}
	defer root.Close()
	pending, e := readPending(root, p.Root)
	if e != nil {
		return out, e
	}
	for _, v := range p.Inventories {
		if e = validateInventory(p.Root, v); e != nil {
			return out, e
		}
		if pending[v.CampaignID].CampaignID != "" {
			continue
		}
		for _, s := range v.Starts {
			if _, e = startrequest.Retire(ctx, p.retention, p.Root, s.ID, s.Digest); e != nil {
				return out, e
			}
		}
	}
	for _, v := range p.Inventories {
		if pending[v.CampaignID].CampaignID != "" {
			continue
		}
		for _, s := range v.Starts {
			if services == nil {
				return out, ErrUnconfirmed
			}
			if e = services.ReconcileRetired(ctx, p.retention, p.Root, s.ID, s.Digest); e != nil {
				return out, e
			}
		}
	}
	out.Code = "container_cleanup_unconfirmed"
	for _, v := range p.Inventories {
		if v.Binding == nil {
			continue
		}
		if p.docker == nil {
			return out, ErrUnconfirmed
		}
		state := p.docker.CheckInactive(ctx, *v.Binding)
		if !state.Confirmed {
			return out, ErrUnconfirmed
		}
		if state.State != "absent" {
			if e = p.docker.RemoveStopped(ctx, *v.Binding); e != nil {
				return out, e
			}
			state = p.docker.CheckInactive(ctx, *v.Binding)
			if !state.Confirmed || state.State != "absent" {
				return out, ErrUnconfirmed
			}
		}
	}
	out.Code = "plan_publication_failed"
	if e = mkdir(root, "purges"); e != nil {
		return out, e
	}
	for _, v := range p.Inventories {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		if len(v.Groups) == 0 || pending[v.CampaignID].CampaignID != "" {
			continue
		}
		if e = mkdir(root, "purges/"+v.CampaignID); e != nil {
			return out, e
		}
		if e = publish(ctx, filepath.Join(p.Root, "purges", v.CampaignID, "plan.json"), planRecord{planVersion, p.Root, v}); e != nil {
			return out, e
		}
	}
	out.Status, out.Code = "partial", "deletion_incomplete"
	for i, v := range p.Inventories {
		row := &out.Campaigns[i]
		row.Status = "partial"
		for _, g := range v.Groups {
			removed := RemovedGroup{g.Kind, g.Path, "not_removed"}
			row.Groups = append(row.Groups, removed)
			n := len(row.Groups) - 1
			if e = verifyGroup(ctx, g, true); e != nil {
				return out, e
			}
			_, e = os.Lstat(g.Path)
			if errors.Is(e, os.ErrNotExist) {
				row.Groups[n].Status = "already_absent"
				continue
			}
			if e != nil {
				return out, e
			}
			remove := p.remove
			if remove == nil {
				remove = removeGroup
			}
			if e = remove(ctx, g); e != nil {
				row.Groups[n].Status = "partial"
				return out, e
			}
			row.Groups[n].Status = "removed"
		}
		// Delete the retry record last. Failed removal remains retryable, even when
		// all evidence is gone and only an empty administrative directory survives.
		dir := filepath.Join(p.Root, "purges", v.CampaignID)
		if _, e = os.Lstat(dir); e == nil {
			g, e := inventoryGroup(ctx, "purges", dir)
			if e != nil {
				return out, e
			}
			if e = removeGroup(ctx, g); e != nil {
				return out, e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return out, e
		}
		row.Status = "removed"
		if len(v.Groups) == 0 {
			row.Status = "already_absent"
		}
	}
	out.Status, out.Code = "complete", "purged"
	return out, nil
}
func removeGroup(ctx context.Context, g Group) error {
	parent, e := os.OpenRoot(filepath.Dir(g.Path))
	if e != nil {
		return e
	}
	defer parent.Close()
	// Traversal checks each directory identity and device again, unlinks links,
	// and restores write permission only inside this exclusively owned group.
	return campaign.RemovePurgeTree(ctx, parent, filepath.Base(g.Path), g.Device)
}

// A refused all-selection may be unable to acquire its exclusions. Report IDs
// visible in a read-only best-effort inventory, explicitly not a stable selection.
func refusedResult(root string, selection Selection) Result {
	out := result(selection)
	if !selection.All {
		return out
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return out
	}
	defer r.Close()
	ids := map[string]bool{}
	for _, kind := range []string{"campaigns", "attachments", "managed-copies", "purges"} {
		ns, e := names(r, kind)
		if e != nil {
			continue
		}
		for _, id := range ns {
			if validID(id) {
				ids[id] = true
			}
		}
	}
	if starts, e := startrequest.Inventory(root); e == nil {
		for _, s := range starts {
			ids[s.Request.Selection.CampaignID] = true
		}
	}
	for id := range ids {
		out.Selected = append(out.Selected, id)
	}
	sort.Strings(out.Selected)
	return out
}
