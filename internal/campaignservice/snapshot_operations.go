//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
)

func (s *Service) stateOperation(ctx context.Context, q stateRequest, reservation string) (stateReply, *preparation.Target, error) {
	if !s.target().AllowsOperationFeatures(q.Operation) {
		return stateDenied("UNSUPPORTED_CAPABILITY"), nil, nil
	}
	peer := s.config.Peer.(SnapshotPeer)
	var body struct {
		Label       string `json:"label"`
		Description string `json:"description"`
		Source      string `json:"source_session"`
		Checkpoint  string `json:"checkpoint_id"`
		Offset      int64  `json:"offset"`
		Limit       int    `json:"limit"`
	}
	_ = json.Unmarshal(q.Body, &body)
	if q.Operation == "engine.snapshot_request" {
		limits := s.remaining()
		if limits["snapshot_admissions"] == 0 || limits["snapshot_bytes"] == 0 {
			return stateDenied("SNAPSHOT_BUDGET_EXCEEDED"), nil, nil
		}
		view, err := s.inspect(ctx, false)
		if err != nil {
			return stateReply{}, nil, err
		}
		binding := s.target().Live().Binding()
		deadline, _ := ctx.Deadline()
		p, err := interceptor.PrepareSnapshotCreate(interceptor.OperationRequest{RequestID: "snapshot-" + contracts.RawDigest([]byte(q.ID))[7:39], OperationID: "snapshot-" + contracts.RawDigest([]byte(q.ID))[7:39], CampaignID: q.Campaign, SessionID: binding.SessionID, WorkerInstanceID: binding.WorkerInstanceID, RunRevision: binding.RunRevision, ExpectedSessionRevision: view.Session.Revision, Deadline: deadline}, interceptor.SnapshotCreate{Label: body.Label, Description: body.Description, MaximumCommittedBytes: limits["snapshot_bytes"]})
		if err != nil {
			return stateDenied("INVALID_ARGUMENTS", contracts.ArgumentError{Reason: "invalid_resolution_context"}), nil, nil
		}
		if err = s.config.Runtime.CheckRunning(ctx, s.config.Docker); err != nil {
			return stateReply{}, nil, err
		}
		// Charge before dispatch. Admitted failures retain the count; bytes are charged
		// only once a hash-verified commitment receipt has been retained.
		if err = s.stateAudit(q, reservation, "state.snapshot-admission", marshal(map[string]any{"admissions": s.snapshotAdmissions + 1, "maximum_committed_bytes": limits["snapshot_bytes"]}), nil); err != nil {
			return stateReply{}, nil, err
		}
		s.snapshotAdmissions++
		var cp interceptor.Checkpoint
		r, err := s.stateNative(ctx, q, reservation, p, func(r interceptor.Response) (interceptor.Response, error) {
			verified, err := r.Validate()
			if err != nil {
				return r, err
			}
			if verified.Status == 429 && verified.Code() == "snapshot_bytes_exhausted" {
				return verified, nil
			}
			cp, err = interceptor.DecodeSnapshotCreated(verified, p, view.Session)
			if err == nil && verified.SessionRevision <= view.Session.Revision {
				err = diagnostictext.New("Snapshot creation did not advance the session revision.")
			}
			return verified, err
		})
		if err != nil {
			return stateReply{}, nil, err
		}
		if r.Status == 429 && r.Code() == "snapshot_bytes_exhausted" {
			if save := s.recordNativeRejection(q.ID, &interceptor.RemoteError{Response: r}); save != nil {
				return stateReply{}, nil, errors.Join(&interceptor.RemoteError{Response: r}, save)
			}
			if _, err = s.inspect(ctx, false); err != nil {
				return stateReply{}, nil, err
			}
			return stateDenied("SNAPSHOT_BUDGET_EXCEEDED"), nil, nil
		}
		if err = s.retainCheckpoint(q, reservation, cp); err != nil {
			return stateReply{}, nil, err
		}
		s.snapshotBytes += cp.CanonicalSizeBytes
		s.checkpointLineage[cp.SourceSessionID+"/"+cp.ID] = cloneLineage(s.lineage, "")
		return stateReply{Result: map[string]any{"receipt_id": "snapshot-" + contracts.RawDigest([]byte(q.ID))[7:39], "snapshot": snapshotMetadata(cp, q.Campaign), "remaining_limits": s.remaining()}}, nil, nil
	}
	if q.Operation == "engine.snapshot_list" {
		if body.Offset > interceptor.MaxSnapshotInventory {
			return stateDenied("INVALID_ARGUMENTS", contracts.ArgumentError{Reason: "snapshot_offset_exceeded", FieldPath: "/offset", Correction: fmt.Sprintf("Offset must be at most %d. Use snapshot_list from offset 0 and follow its returned next_offset.", interceptor.MaxSnapshotInventory)}), nil, nil
		}
		if body.Limit == 0 {
			body.Limit = int(s.sizes.SnapshotPage)
		}
		page, err := peer.ListSnapshots(ctx, interceptor.SnapshotListRequest{CampaignID: q.Campaign, SourceSessionID: body.Source, Offset: int(body.Offset), Limit: body.Limit})
		if err != nil {
			r, e := readError(err)
			if save := s.recordNativeRejection(q.ID, err); save != nil {
				return stateReply{}, nil, errors.Join(err, save)
			}
			return r, nil, e
		}
		if page.CampaignID != q.Campaign {
			return stateReply{}, nil, ErrService
		}
		entries := []any{}
		for _, cp := range page.Checkpoints {
			if !s.compatibleSnapshot(cp) {
				return stateReply{}, nil, ErrService
			}
			if err = s.retainCheckpoint(q, reservation, cp); err != nil {
				return stateReply{}, nil, err
			}
			entries = append(entries, snapshotMetadata(cp, q.Campaign))
		}
		result := map[string]any{"campaign_id": q.Campaign, "snapshots": entries, "offset": body.Offset, "total": page.Total}
		if page.NextOffset != nil {
			result["next_offset"] = *page.NextOffset
		}
		return stateReply{Result: result}, nil, nil
	}
	cp, err := peer.InspectSnapshot(ctx, q.Campaign, body.Source, body.Checkpoint)
	if err != nil {
		r, e := readError(err)
		if save := s.recordNativeRejection(q.ID, err); save != nil {
			return stateReply{}, nil, errors.Join(err, save)
		}
		return r, nil, e
	}
	if cp.SourceSessionID != body.Source || cp.ID != body.Checkpoint {
		return stateReply{}, nil, ErrService
	}
	if !s.compatibleSnapshot(cp) {
		if q.Operation == "engine.restore_request" {
			return stateDenied("SNAPSHOT_INCOMPATIBLE"), nil, nil
		}
		return stateReply{}, nil, ErrService
	}
	if err = s.retainCheckpoint(q, reservation, cp); err != nil {
		return stateReply{}, nil, err
	}
	if q.Operation == "engine.snapshot_inspect" {
		return stateReply{Result: map[string]any{"snapshot": snapshotMetadata(cp, q.Campaign)}}, nil, nil
	}
	view, err := s.inspect(ctx, false)
	if err != nil {
		return stateReply{}, nil, err
	}
	prior := s.target()
	binding := prior.Live().Binding()
	if binding.RunRevision >= contracts.MaxSafeInteger {
		return stateDenied("LIMIT_EXCEEDED"), nil, nil
	}
	deadline, _ := ctx.Deadline()
	p, err := interceptor.PrepareLifecycle(interceptor.LifecycleRequest{CampaignID: q.Campaign, SessionID: binding.SessionID, WorkerInstanceID: binding.WorkerInstanceID, RunRevision: binding.RunRevision, OperationID: "restore-" + contracts.RawDigest([]byte(q.ID))[7:39], Operation: "snapshot.restore", SourceSessionID: body.Source, CheckpointID: body.Checkpoint}, deadline)
	if err != nil {
		return stateReply{}, nil, err
	}
	if err = s.config.Runtime.CheckRunning(ctx, s.config.Docker); err != nil {
		return stateReply{}, nil, err
	}
	// Mark a host-known transition before external contact, so idle status polling
	// cannot mistake native transitioning/closed state for an independent failure.
	s.restoring.Store(true)
	s.transitionEpoch.Add(1)
	if err = s.stateAudit(q, reservation, "state.restore-intent", p.Bytes(), nil); err != nil {
		return stateReply{}, nil, err
	}
	if err = ctx.Err(); err != nil {
		return stateReply{}, nil, err
	}
	var replacement interceptor.RestoreResult
	r, err := interceptor.Call("snapshot.restore", p.Request().OperationID,
		func() (interceptor.Response, error) { return s.config.Peer.ExecuteLifecycle(ctx, p) },
		func(r interceptor.Response) error {
			return s.stateAudit(q, reservation, "state.restore-result", nil, boundedResponse(r))
		},
		func(r interceptor.Response) (interceptor.Response, error) {
			verified, err := r.Validate()
			if err != nil {
				return r, err
			}
			if verified.Status == 409 && verified.Code() == "checkpoint_unavailable_or_incompatible" {
				return verified, nil
			}
			replacement, err = interceptor.DecodeRestore(verified, p, binding, view.Session)
			return verified, err
		})
	if err != nil {
		return stateReply{}, nil, err
	}
	if r.Status == 409 && r.Code() == "checkpoint_unavailable_or_incompatible" {
		if save := s.recordNativeRejection(q.ID, &interceptor.RemoteError{Response: r}); save != nil {
			return stateReply{}, nil, errors.Join(&interceptor.RemoteError{Response: r}, save)
		}
		// A precise preflight response plus a fresh healthy original binding proves
		// execution can continue. Other errors never reopen the campaign.
		if _, err = s.inspect(ctx, false); err != nil {
			return stateReply{}, nil, err
		}
		return stateDenied("SNAPSHOT_INCOMPATIBLE"), nil, nil
	}
	status, err := s.config.Peer.Status(ctx, q.Campaign)
	if err != nil {
		return stateReply{}, nil, err
	}
	next, err := prior.Replacement(replacement, status)
	if err != nil {
		return stateReply{}, nil, err
	}
	if err = s.rememberEvidenceTarget(next, &cp); err != nil {
		return stateReply{}, nil, err
	}
	if _, err = s.inspectTarget(ctx, false, next); err != nil {
		return stateReply{}, nil, err
	}
	if err = s.stateAudit(q, reservation, "state.replacement-verified", marshal(map[string]any{"binding": replacement.Binding, "status": status}), nil); err != nil {
		return stateReply{}, nil, err
	}
	s.lineage = cloneLineage(s.checkpointLineage[cp.SourceSessionID+"/"+cp.ID], replacement.Binding.SessionID)
	return stateReply{Result: map[string]any{"transition_receipt": "transition-" + contracts.RawDigest([]byte(q.ID))[7:39], "previous_run_revision": q.Revision, "run_revision": replacement.Binding.RunRevision, "snapshot": snapshotMetadata(cp, q.Campaign), "remaining_limits": s.remaining(), "harness_disposition": "continue"}}, next, nil
}
