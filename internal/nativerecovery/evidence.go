//go:build linux || darwin

package nativerecovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeevidence"
)

type EvidencePeer interface {
	nativeevidence.Peer
	Status(context.Context, string) (interceptor.Status, error)
}
type EvidenceDocker interface {
	CheckInactive(context.Context, campaign.DockerBinding) dockercontrol.Inactivity
}
type EvidenceReport struct {
	Reason         string                     `json:"reason,omitempty"`
	APIVersion     string                     `json:"api_version"`
	CampaignID     string                     `json:"campaign_id"`
	ManifestDigest string                     `json:"run_manifest_digest"`
	Outcomes       []campaign.EvidenceOutcome `json:"outcomes"`
}

func (r EvidenceReport) Complete() bool {
	if r.Reason != "" || r.ManifestDigest == "" || len(r.Outcomes) == 0 {
		return false
	}
	for _, v := range r.Outcomes {
		if v.State != "complete" || !v.Recorded {
			return false
		}
	}
	return true
}

type evidenceFacts struct {
	policy    campaign.EvidencePolicy
	targets   []nativeevidence.Target
	adopted   map[string]campaign.NativeEvidence
	completed map[string]campaign.EvidenceOutcome
	started   bool
}

// CollectEvidence acquires exclusive installation/campaign ownership and only
// reads native state. It never terminates, attaches, restores or starts a target.
func CollectEvidence(ctx context.Context, root, id string, docker EvidenceDocker, peer EvidencePeer) (out EvidenceReport, err error) {
	out = EvidenceReport{APIVersion: "operator.dev/evidence-collection/v1alpha1", CampaignID: id, Outcomes: []campaign.EvidenceOutcome{}}
	defer func() {
		if err == nil {
			return
		}
		switch {
		case errors.Is(err, campaign.ErrActive):
			out.Reason = "execution_not_inactive"
		case errors.Is(err, campaign.ErrCorrupt):
			out.Reason = "retained_evidence_unverified"
		case errors.Is(err, campaign.ErrInvalid):
			out.Reason = "collection_inputs_unavailable"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			out.Reason = "collection_deadline"
		default:
			out.Reason = "collection_unavailable"
		}
	}()
	if e := ctx.Err(); e != nil {
		return out, e
	}
	if docker == nil || peer == nil {
		return out, campaign.ErrInvalid
	}
	lease, e := campaign.AcquireHostLease(root)
	if e != nil {
		return out, e
	}
	defer lease.Close()
	a, e := campaign.OpenNativeRecovery(root, id)
	if e != nil {
		return out, e
	}
	defer a.Close()
	f, e := loadFacts(ctx, a)
	if e != nil {
		return out, e
	}
	out.ManifestDigest = f.digest
	if !f.prepared {
		return out, campaign.ErrInvalid
	}
	evidence, e := loadEvidenceFacts(ctx, a, f)
	if e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(evidence.policy.TotalTimeoutNS))
	defer cancel()
	binding, e := campaign.ReadDockerBinding(root, id)
	if e == nil {
		if inactive := docker.CheckInactive(ctx, binding); !inactive.Confirmed {
			return out, campaign.ErrActive
		}
	} else if !errors.Is(e, os.ErrNotExist) || evidence.started {
		return out, campaign.ErrCorrupt
	}
	for index, target := range evidence.targets {
		if previous, ok := evidence.completed[target.Identity.SessionID]; ok && !campaign.EvidenceRetryable(previous) {
			if previous.ArchivePath != "" {
				record, found := evidence.adopted[target.Identity.SessionID]
				if !found {
					return out, campaign.ErrCorrupt
				}
				previous = verifyEvidenceOutcome(ctx, a, record, previous)
			}
			out.Outcomes = append(out.Outcomes, previous)
			continue
		}
		child, cancel := context.WithTimeout(ctx, time.Duration(evidence.policy.TimeoutNS))
		result, e := collectEvidenceSession(child, a, f, evidence.policy, target, evidence.adopted, peer)
		cancel()
		if e != nil && result.Reason == "" {
			result.Reason = "collection_unavailable"
		}
		out.Outcomes = append(out.Outcomes, result)
		if e != nil {
			for _, pending := range evidence.targets[index+1:] {
				out.Outcomes = append(out.Outcomes, campaign.EvidenceOutcome{SessionID: pending.Identity.SessionID, State: "missing", Reason: "collection_aborted", LocalMaxBytes: evidence.policy.MaxArchiveBytes, NativeMaxBytes: pending.NativeMaxBytes})
			}
			return out, e
		}
	}
	return out, nil
}
func loadEvidenceFacts(ctx context.Context, a *campaign.NativeRecovery, f facts) (out evidenceFacts, err error) {
	out.adopted = map[string]campaign.NativeEvidence{}
	out.completed = map[string]campaign.EvidenceOutcome{}
	selected := map[string]nativeevidence.Target{}
	policySeen := false
	_, err = a.Inspect(ctx, func(event campaign.Event) error {
		switch event.Kind {
		case "launch.start-intent":
			out.started = true
		case "evidence.policy":
			if policySeen || len(selected) != 0 || interceptor.DecodeTypedBody(event.Metadata, &out.policy, campaign.MaxMetadataBytes) != nil || !out.policy.Valid() {
				return campaign.ErrCorrupt
			}
			policySeen = true
		case "evidence.session-selected":
			if !policySeen || len(selected) >= 100000 {
				return campaign.ErrCorrupt
			}
			raw, e := role(a, event, "evidence-selection")
			if e != nil {
				return e
			}
			var target nativeevidence.Target
			if interceptor.DecodeTypedBody(raw, &target, campaign.MaxContentBytes) != nil {
				return campaign.ErrCorrupt
			}
			id := target.Identity
			if !id.Valid() || id.CampaignID != f.manifest.CampaignID || id.EnvironmentDigest != f.environment || id.ApplicationDigest != f.application || id.CapabilityDigest != f.manifest.Target.CapabilitySourceDigest || id.FeedbackProfile != f.manifest.Target.NativeFeedbackProfile || target.NativeMaxBytes < 0 || target.NativeMaxBytes > contracts.MaxSafeInteger || target.Reservation != "evidence:"+contracts.RawDigest([]byte(id.SessionID))[7:] {
				return campaign.ErrCorrupt
			}
			if _, exists := selected[id.SessionID]; exists {
				return campaign.ErrCorrupt
			}
			if len(selected) == 0 {
				if id.SessionID != f.manifest.Target.SessionID || id.ParentSessionID != "" {
					return campaign.ErrCorrupt
				}
			} else {
				if _, ok := selected[id.ParentSessionID]; !ok {
					return campaign.ErrCorrupt
				}
			}
			selected[id.SessionID] = target
			out.targets = append(out.targets, target)
		case "evidence.adopted":
			raw, e := role(a, event, "native-evidence")
			if e != nil {
				return e
			}
			var record campaign.NativeEvidence
			if interceptor.DecodeTypedBody(raw, &record, campaign.MaxContentBytes) != nil {
				return campaign.ErrCorrupt
			}
			id := record.Provenance.Identity.SessionID
			target, ok := selected[id]
			if !ok || record.Provenance.Identity != target.Identity || record.Provenance.Transfer.LocalMaxBytes != out.policy.MaxArchiveBytes || record.Provenance.Transfer.InterceptorMaxBytes != target.NativeMaxBytes {
				return campaign.ErrCorrupt
			}
			if _, exists := out.adopted[id]; exists {
				return campaign.ErrCorrupt
			}
			out.adopted[id] = record
		case "evidence.collection-result":
			var result campaign.EvidenceOutcome
			var fields map[string]json.RawMessage
			if json.Unmarshal(event.Metadata, &fields) != nil {
				return campaign.ErrCorrupt
			}
			// Inspect already verifies this storage envelope. It is not part of
			// the public per-session outcome's closed shape.
			delete(fields, "journal_reservation")
			raw, _ := json.Marshal(fields)
			if interceptor.DecodeTypedBody(raw, &result, campaign.MaxMetadataBytes) != nil {
				return campaign.ErrCorrupt
			}
			target, ok := selected[result.SessionID]
			if !ok || !validEvidenceOutcome(result, target, out.policy) {
				return campaign.ErrCorrupt
			}
			if result.ArchivePath != "" {
				r, ok := out.adopted[result.SessionID]
				if !ok || r.Path != result.ArchivePath || r.Provenance.Transfer.SHA256 != result.ArchiveDigest || r.Provenance.State != result.State {
					return campaign.ErrCorrupt
				}
			}
			if _, exists := out.completed[result.SessionID]; exists {
				return campaign.ErrCorrupt
			}
			out.completed[result.SessionID] = result
		}
		return nil
	})
	if err == nil && (!policySeen || len(out.targets) == 0) {
		err = campaign.ErrInvalid
	}
	return out, err
}
func validEvidenceOutcome(v campaign.EvidenceOutcome, t nativeevidence.Target, p campaign.EvidencePolicy) bool {
	if v.SessionID != t.Identity.SessionID || !v.Recorded || v.LocalMaxBytes != p.MaxArchiveBytes || v.NativeMaxBytes != t.NativeMaxBytes {
		return false
	}
	switch v.State {
	case "complete", "partial":
		return v.ArchivePath != "" && v.ArchiveDigest != ""
	case "missing", "invalid":
		return v.ArchivePath == "" && v.ArchiveDigest == ""
	}
	return false
}
func collectEvidenceSession(ctx context.Context, a *campaign.NativeRecovery, f facts, policy campaign.EvidencePolicy, target nativeevidence.Target, adopted map[string]campaign.NativeEvidence, peer EvidencePeer) (out campaign.EvidenceOutcome, err error) {
	out = campaign.EvidenceOutcome{SessionID: target.Identity.SessionID, State: "missing", LocalMaxBytes: policy.MaxArchiveBytes, NativeMaxBytes: target.NativeMaxBytes}
	raw, _ := json.Marshal(struct {
		Policy campaign.EvidencePolicy `json:"policy"`
		Target nativeevidence.Target   `json:"target"`
	}{policy, target})
	digest, e := contracts.CanonicalDigest(raw, campaign.ManifestLimit)
	if e != nil {
		return out, e
	}
	if record, ok := adopted[out.SessionID]; ok {
		out.State = record.Provenance.State
		out.ArchivePath = record.Path
		out.ArchiveDigest = record.Provenance.Transfer.SHA256
		out.Recorded = true
		return verifyEvidenceOutcome(ctx, a, record, out), nil
	}
	fresh, saved, e := a.BeginEvidence(out.SessionID, digest)
	if e != nil {
		return out, e
	}
	if saved != nil && !validEvidenceOutcome(saved.Outcome, target, policy) {
		return out, campaign.ErrCorrupt
	}
	if !fresh && (saved == nil || campaign.EvidenceRetryable(saved.Outcome)) {
		fresh, saved, e = a.RetryEvidence(out.SessionID, digest)
		if e != nil {
			return out, e
		}
	}
	if !fresh {
		if saved == nil {
			out.Reason = "prior_collection_unconfirmed"
			return out, nil
		}
		if !validEvidenceOutcome(saved.Outcome, target, policy) {
			return out, campaign.ErrCorrupt
		}
		if saved.Outcome.ArchivePath != "" {
			if saved.Archive == nil || saved.Archive.Provenance.Identity != target.Identity || saved.Archive.Path != saved.Outcome.ArchivePath || saved.Archive.Provenance.Transfer.SHA256 != saved.Outcome.ArchiveDigest {
				return out, campaign.ErrCorrupt
			}
			return verifyEvidenceOutcome(ctx, a, *saved.Archive, saved.Outcome), nil
		}
		return saved.Outcome, nil
	}
	var retained *campaign.NativeEvidence
	defer func() {
		out.Recorded = true
		if e := a.FinishEvidence(campaign.EvidenceResult{Outcome: out, Archive: retained}); e != nil {
			out.Recorded = false
			out.Reason = "result_persistence_failed"
			err = errors.Join(err, e)
		}
	}()

	if ctx.Err() != nil {
		out.Reason = "collection_deadline"
		return out, nil
	}
	if target.NativeMaxBytes <= 0 {
		out.Reason = "native_limit_unavailable"
		return out, nil
	}
	status, e := peer.Status(ctx, f.manifest.CampaignID)
	if e != nil || status.InstanceID != f.instance || status.CampaignID != f.manifest.CampaignID || !status.StoreAvailable {
		out.Reason = "native_identity_unconfirmed"
		return out, nil
	}
	if b, ok := status.Sessions[out.SessionID]; !ok || b.SessionID != out.SessionID {
		out.Reason = "native_identity_unconfirmed"
		return out, nil
	}
	directory, e := a.EvidenceDirectory(min(policy.MaxArchiveBytes, target.NativeMaxBytes))
	if e != nil {
		out.Reason = "storage_unavailable"
		return out, nil
	}
	out = nativeevidence.Collect(ctx, peer, directory, target, policy.MaxArchiveBytes, func(ctx context.Context, v *interceptor.VerifiedEvidence) (campaign.NativeEvidence, error) {
		r, e := a.RetainEvidence(ctx, v)
		if e == nil {
			retained = &r
		}
		return r, e
	})
	return out, nil
}

func verifyEvidenceOutcome(ctx context.Context, a *campaign.NativeRecovery, record campaign.NativeEvidence, out campaign.EvidenceOutcome) campaign.EvidenceOutcome {
	if e := a.VerifyRetainedEvidence(ctx, record); e != nil {
		out.State = "invalid"
		out.Reason = "retained_evidence_invalid"
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
			out.State = "missing"
			out.Reason = "collection_deadline"
		}
		out.Recorded = false
		out.ArchivePath = ""
		out.ArchiveDigest = ""
	}
	return out
}
