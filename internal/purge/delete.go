//go:build linux || darwin

package purge

import (
	"context"
	"errors"
	"os"
	"path/filepath"

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
	APIVersion  string           `json:"api_version"`
	All         bool             `json:"all"`
	Selected    []string         `json:"selected_ids"`
	Status      string           `json:"status"`
	Code        string           `json:"code"`
	Campaigns   []CampaignResult `json:"campaigns"`
	Consequence string           `json:"consequence"`
}

func result(selection Selection) Result {
	r := Result{APIVersion: "operator.dev/purge-result/v1alpha1", All: selection.All, Selected: []string{}, Status: "refused", Code: "preflight_unconfirmed", Campaigns: []CampaignResult{}, Consequence: Consequence}
	if selection.CampaignID != "" {
		r.Selected = append(r.Selected, selection.CampaignID)
	}
	return r
}
func Run(ctx context.Context, root string, selection Selection, docker Docker, services Services) (Result, error) {
	p, e := Prepare(ctx, root, selection, docker)
	if e != nil {
		return result(selection), e
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
