//go:build linux || darwin

package nativerecovery

import (
	"context"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// RunAttachment handles the pre-preparation window only. The caller holds the
// installation lease, has reconciled Docker, and has verified there is no intact
// campaign.prepared record. Prepared or damaged journals take precedence.
func RunAttachment(ctx context.Context, root, id string, peer Peer) (out Outcome, err error) {
	out = Outcome{CampaignID: id, State: "unconfirmed", Reason: "attachment_identity_unconfirmed", Closure: "unconfirmed", Cleanup: "not-started", TargetStop: "not-permitted"}
	if peer == nil {
		return out, campaign.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return out, e
	}
	// Keep any partial campaign writer locked throughout cleanup. An execution
	// intent or complete preparation must use journal-based recovery instead.
	ids, e := campaign.CampaignIDs(root)
	if e != nil {
		return out, e
	}
	for _, existing := range ids {
		if existing != id {
			continue
		}
		old, e := campaign.OpenNativeRecovery(root, id)
		if e != nil {
			return out, e
		}
		defer old.Close()
		forbidden := false
		_, e = old.Inspect(ctx, func(event campaign.Event) error {
			if event.Kind == "campaign.prepared" || event.Kind == "launch.start-intent" {
				forbidden = true
			}
			return nil
		})
		if e != nil {
			return out, e
		}
		if forbidden {
			return out, campaign.ErrInvalid
		}
	}
	a, s, e := campaign.OpenAttachmentRecovery(root, id)
	if e != nil {
		return out, e
	}
	defer a.Close()
	out.AttachmentDigest = s.Digest
	if s.Intent.AllowTargetStop {
		out.TargetStop = "unconfirmed"
	}
	if s.Binding == nil || s.InstanceID == "" {
		// A status query cannot establish which instance accepted an unsaved reply.
		// Save the uncertainty once, without making any native calls.
		fresh, saved, e := a.Begin()
		if e != nil {
			return out, e
		}
		if fresh {
			return out, a.Finish(out)
		}
		if len(saved) == 0 {
			out.Reason = "prior_recovery_unconfirmed"
			return out, nil
		}
		var previous Outcome
		if interceptor.DecodeTypedBody(saved, &previous, campaign.ManifestLimit) != nil || previous.CampaignID != id || previous.AttachmentDigest != out.AttachmentDigest || previous.ManifestDigest != "" {
			return out, campaign.ErrCorrupt
		}
		return previous, nil
	}
	b := s.Binding
	f := facts{instance: s.InstanceID, binding: b.Binding, allowStop: s.Intent.AllowTargetStop, environment: b.Session.EnvironmentDigest, application: b.Session.AppDigest}
	f.manifest.Target = campaign.TargetBinding{CapabilitySourceDigest: b.Session.CapabilityManifestDigest, NativeFeedbackProfile: b.Session.FeedbackProfile}
	return finalize(ctx, a, f, peer, out)
}
