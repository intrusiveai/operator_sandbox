//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeevidence"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
)

// EvidenceConfig is host policy. Peer defaults to the configured native client;
// alternate implementations are for host integrations/tests, never guest input.
type EvidenceConfig struct {
	Peer            EvidencePeer
	MaxArchiveBytes int64
	Timeout         time.Duration
	TotalTimeout    time.Duration
}
type EvidencePeer = nativeevidence.Peer
type evidenceTarget = nativeevidence.Target
type EvidenceOutcome = nativeevidence.Outcome

func (s *Service) rememberEvidenceTarget(t *preparation.Target, cp *interceptor.Checkpoint) error {
	id, maximum := t.NativeEvidence()
	if cp != nil {
		if cp.SourceSessionID != id.ParentSessionID || cp.ID != id.ParentCheckpointID {
			return ErrService
		}
		id.ParentCheckpointDigest = cp.Hash
	}
	if !id.Valid() {
		return ErrService
	}
	s.evidenceMu.Lock()
	for _, known := range s.evidenceTargets {
		if known.Identity.SessionID == id.SessionID {
			s.evidenceMu.Unlock()
			if known.Identity != id || known.NativeMaxBytes != maximum {
				return ErrService
			}
			return nil
		}
	}
	target := evidenceTarget{Identity: id, NativeMaxBytes: maximum, Reservation: "evidence:" + contracts.RawDigest([]byte(id.SessionID))[7:]}
	// Keep the independently verified binding available for best-effort collection
	// even if its journal reservation fails and execution must terminate.
	s.evidenceTargets = append(s.evidenceTargets, target)
	s.evidenceMu.Unlock()
	_, err := s.writer.AppendReserving(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "evidence.session-selected", Metadata: marshal(map[string]string{"session_id": id.SessionID}), Content: []campaign.Content{{Role: "evidence-selection", MediaType: "application/json", Bytes: marshal(target)}}}, target.Reservation, 2<<20)
	return err
}
func (s *Service) collectEvidence() []EvidenceOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Evidence.TotalTimeout)
	defer cancel()
	targets := s.selectedEvidence()
	outcomes := make([]EvidenceOutcome, 0, len(targets))
	peer := s.config.Evidence.Peer
	if peer == nil {
		peer, _ = s.config.Peer.(EvidencePeer)
	}
	directory, dirErr := s.writer.EvidenceDirectory()
	for _, target := range targets {
		outcome := EvidenceOutcome{SessionID: target.Identity.SessionID, State: "missing", LocalMaxBytes: s.config.Evidence.MaxArchiveBytes, NativeMaxBytes: target.NativeMaxBytes}
		switch {
		case ctx.Err() != nil:
			outcome.Reason = "collection_deadline"
		case peer == nil:
			outcome.Reason = "export_unavailable"
		case target.NativeMaxBytes <= 0:
			outcome.Reason = "native_limit_unavailable"
		case dirErr != nil:
			outcome.Reason = "storage_unavailable"
		default:
			child, cancel := context.WithTimeout(ctx, s.config.Evidence.Timeout)
			outcome = nativeevidence.Collect(child, peer, directory, target, s.config.Evidence.MaxArchiveBytes, func(ctx context.Context, v *interceptor.VerifiedEvidence) (campaign.NativeEvidence, error) {
				return s.writer.RetainEvidence(ctx, v, target.Reservation)
			})
			cancel()
		}
		// A successful archive-adoption event and this per-session result are separate
		// commits. Failure is reported in memory without claiming durable publication.
		outcome.Recorded = true
		_, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "evidence.collection-result", Metadata: marshal(outcome)}, target.Reservation, true)
		if err != nil {
			outcome.Recorded = false
			outcome.Reason = "result_persistence_failed"
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

// Detached copies prevent callers of Wait from mutating retained outcomes.
func cloneTerminal(r TerminalResult) TerminalResult {
	raw, _ := json.Marshal(r)
	var out TerminalResult
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Service) selectedEvidence() []evidenceTarget {
	s.evidenceMu.Lock()
	defer s.evidenceMu.Unlock()
	return slices.Clone(s.evidenceTargets)
}
