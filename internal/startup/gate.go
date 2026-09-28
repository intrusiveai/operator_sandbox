//go:build linux || darwin

// Package startup reconciles prior execution before a new worker is admitted.
// It never reopens an old journal or resumes a campaign's execution.
package startup

import (
	"context"
	"errors"
	"os"
	"sync/atomic"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

var ErrUnresolved = errors.New("prior campaign execution remains unresolved")

type Docker interface {
	termination.Killer
	CheckInactive(context.Context, campaign.DockerBinding) dockercontrol.Inactivity
	RemoveStopped(context.Context, campaign.DockerBinding) error
	ResolveCreate(context.Context, campaign.CreateIntent) (campaign.DockerBinding, error)
}

type Prior struct {
	CampaignID       string                     `json:"campaign_id"`
	JournalIntact    bool                       `json:"journal_intact"`
	State            string                     `json:"state"`
	Inactivity       dockercontrol.Inactivity   `json:"inactivity"`
	Termination      *termination.Receipt       `json:"termination,omitempty"`
	BindingRecovered bool                       `json:"binding_recovered,omitempty"`
	Transient        *campaign.TransientCleanup `json:"transient,omitempty"`
	Native           *nativerecovery.Outcome    `json:"native,omitempty"`
}

// Gate owns the same installation-wide lease needed by the new worker. Callers
// keep it until that worker finishes, including cleanup. Closing is not a stop.
type Gate struct {
	lease     *campaign.HostLease
	stateRoot string
	claimed   atomic.Bool
	closed    atomic.Bool
	finalized atomic.Bool
	prior     []Prior
}

func (g *Gate) Close() error {
	if g == nil {
		return nil
	}
	g.closed.Store(true)
	return g.lease.Close()
}

// Claim transfers this fresh gate to exactly one worker composition for its root.
func (g *Gate) Claim(stateRoot string) bool {
	return g != nil && g.stateRoot == stateRoot && !g.closed.Load() && g.claimed.CompareAndSwap(false, true)
}

// Acquire serializes new workers, inspects old groups, and confirms exact Docker
// inactivity. Active orphan containers are independently terminated; stopped ones
// are removed by full binding. Lost create replies require verified discovery
// before cleanup. Damaged evidence stays retained and explicitly incomplete.
func Acquire(ctx context.Context, stateRoot string, docker Docker) (*Gate, []Prior, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if docker == nil {
		return nil, nil, ErrUnresolved
	}
	lease, err := campaign.AcquireHostLease(stateRoot)
	if err != nil {
		return nil, nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			lease.Close()
		}
	}()
	ids, err := campaign.CampaignIDs(stateRoot)
	if err != nil {
		return nil, nil, err
	}
	records := []Prior{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, records, err
		}
		prior := Prior{CampaignID: id, State: "unresolved"}
		records = append(records, prior)
		p := &records[len(records)-1]
		started := false
		report, inspectErr := campaign.Inspect(stateRoot, id, func(e campaign.Event) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if e.Kind == "launch.start-intent" {
				started = true
			}
			return nil
		})
		p.JournalIntact = inspectErr == nil && report.JournalIntact
		// Never kill a live writer, even if an older caller omitted the host lease.
		if errors.Is(inspectErr, campaign.ErrActive) {
			return nil, records, campaign.ErrActive
		}
		b, bindingErr := campaign.ReadDockerBinding(stateRoot, id)
		if bindingErr != nil {
			if errors.Is(bindingErr, os.ErrNotExist) && p.JournalIntact && !started {
				p.State = "never-created"
				cleanup, err := campaign.CleanupTransient(ctx, stateRoot, id, true)
				p.Transient = &cleanup
				if err != nil {
					return nil, records, ErrUnresolved
				}
				continue
			}
			if !errors.Is(bindingErr, os.ErrNotExist) || !p.JournalIntact || !started {
				return nil, records, ErrUnresolved
			}
			intent, err := campaign.ReadCreateIntent(ctx, stateRoot, id)
			if err != nil {
				return nil, records, ErrUnresolved
			}
			b, err = docker.ResolveCreate(ctx, intent)
			if err != nil || b.CampaignID != id || campaign.SaveRecoveredDockerBinding(ctx, stateRoot, b) != nil {
				return nil, records, ErrUnresolved
			}
			p.BindingRecovered = true
		}
		p.Inactivity = docker.CheckInactive(ctx, b)
		if !p.Inactivity.Confirmed && p.Inactivity.State == "active" {
			receipt := termination.New(docker).Terminate(ctx, stateRoot, id, termination.NewRequestID(), "startup-recovery")
			p.Termination = &receipt
			if !receipt.Successful() {
				return nil, records, ErrUnresolved
			}
			p.Inactivity = docker.CheckInactive(ctx, b)
		}
		if !p.Inactivity.Confirmed {
			return nil, records, ErrUnresolved
		}
		if p.Inactivity.State != "absent" {
			if err := docker.RemoveStopped(ctx, b); err != nil {
				return nil, records, ErrUnresolved
			}
			p.Inactivity = docker.CheckInactive(ctx, b)
			if !p.Inactivity.Confirmed || p.Inactivity.State != "absent" {
				return nil, records, ErrUnresolved
			}
		}
		p.State = "container-absent"
		cleanup, err := campaign.CleanupTransient(ctx, stateRoot, id, true)
		p.Transient = &cleanup
		if err != nil {
			return nil, records, ErrUnresolved
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, records, err
	}
	attachmentIDs, err := campaign.AttachmentIDs(stateRoot)
	if err != nil {
		return nil, records, err
	}
	known := map[string]bool{}
	for _, row := range records {
		known[row.CampaignID] = true
	}
	for _, id := range attachmentIDs {
		if !known[id] {
			records = append(records, Prior{CampaignID: id, State: "attachment-only"})
		}
	}
	accepted = true
	return &Gate{lease: lease, stateRoot: stateRoot, prior: append([]Prior(nil), records...)}, records, nil
}

// FinalizeNative runs only while this gate owns startup, before a fresh worker
// claims it. A damaged journal permits Docker cleanup but never native mutation.
func (g *Gate) FinalizeNative(ctx context.Context, peer nativerecovery.Peer) ([]Prior, error) {
	if g == nil || g.closed.Load() || g.claimed.Load() || !g.finalized.CompareAndSwap(false, true) {
		return nil, ErrUnresolved
	}
	rows := append([]Prior(nil), g.prior...)
	attachmentIDs, err := campaign.AttachmentIDs(g.stateRoot)
	if err != nil {
		return rows, err
	}
	attachments := map[string]bool{}
	for _, id := range attachmentIDs {
		attachments[id] = true
	}
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		p := &rows[i]
		if p.State == "attachment-only" {
			out, err := nativerecovery.RunAttachment(ctx, g.stateRoot, p.CampaignID, peer)
			p.Native = &out
			if err != nil {
				return rows, err
			}
			continue
		}
		if !p.JournalIntact {
			p.Native = &nativerecovery.Outcome{CampaignID: p.CampaignID, State: "unconfirmed", Reason: "evidence_unavailable", Closure: "unconfirmed", Cleanup: "unconfirmed", TargetStop: "unconfirmed"}
			continue
		}
		out, err := nativerecovery.Run(ctx, g.stateRoot, p.CampaignID, p.State == "container-absent" || p.State == "never-created", peer)
		p.Native = &out
		if err != nil {
			return rows, err
		}
		if attachments[p.CampaignID] && out.State == "not-prepared" {
			out, err = nativerecovery.RunAttachment(ctx, g.stateRoot, p.CampaignID, peer)
			p.Native = &out
			if err != nil {
				return rows, err
			}
		}
	}
	return rows, nil
}
