//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type SnapshotPeer interface {
	ListSnapshots(context.Context, interceptor.SnapshotListRequest) (interceptor.SnapshotPage, error)
	InspectSnapshot(context.Context, string, string, string) (interceptor.Checkpoint, error)
}

func isSnapshot(op string) bool {
	return slices.Contains([]string{"engine.snapshot_request", "engine.snapshot_list", "engine.snapshot_inspect", "engine.restore_request"}, op)
}

type stateRequest struct {
	Campaign  string          `json:"campaign_id"`
	Launch    string          `json:"launch_id"`
	Revision  int64           `json:"run_revision"`
	ID        string          `json:"operation_id"`
	Operation string          `json:"operation"`
	Body      json.RawMessage `json:"body"`
}
type stateReply struct {
	Result any                   `json:"result,omitempty"`
	Error  *attemptadapter.Fault `json:"error,omitempty"`
}

func stateDenied(code string, issues ...contracts.ArgumentError) stateReply {
	message, disposition := "The operation was rejected before execution. Read the selected tool guidance and correct its arguments.", "correct-and-resubmit"
	switch code {
	case "POLICY_DENIED":
		message = "This operation is outside the campaign policy. Select a permitted capability from the target reference; repeating this request cannot grant permission."
		disposition = "gap-and-continue"
	case "UNSUPPORTED_CAPABILITY":
		message = "The target does not support this capability. Use the advertised target capabilities or report the limitation."
		disposition = "gap-and-continue"
	case "LIMIT_EXCEEDED", "ARTIFACT_BUDGET_EXCEEDED", "SNAPSHOT_BUDGET_EXCEEDED":
		message = "This operation cannot fit the remaining allowance. Do not repeat it or increase request values; use retained evidence and finish with an honest assessment or unavailable reason."
		disposition = "gap-and-continue"
	case "STATE_CHANGED":
		message = "The target state changed. Inspect the current state before choosing a new action; do not replay completed operations."
		disposition = "gap-and-continue"
	case "SNAPSHOT_NOT_FOUND":
		message = "Snapshot is unavailable. Use snapshot_list for available identities, then snapshot_inspect to check readiness."
	case "SNAPSHOT_INCOMPATIBLE":
		message = "Snapshot cannot be restored in this target binding. Use snapshot_list and snapshot_inspect to select a compatible ready snapshot."
		disposition = "gap-and-continue"
	case "RECORD_REFERENCE_INVALID":
		message = "Use a historical record handle returned by successful record_append; execution and artifact handles are not records."
	case "IDEMPOTENCY_CONFLICT":
		message = "This call identity is already bound to different arguments. Do not replay completed actions. A corrected rejected request requires a new call identity."
	case "UPLOAD_NOT_FOUND":
		message = "Upload is unavailable. Use a committed artifact handle or start a new bounded artifact publication."
	case "UPLOAD_RANGE_INVALID":
		message = "Upload range is invalid. Continue only within the upload's declared byte range."
	case "UPLOAD_INCOMPLETE":
		message = "Upload has missing parts. Complete the declared upload before committing it."
	}
	reason, field := strings.ToLower(code), ""
	if len(issues) == 1 {
		reason, field, message = issues[0].Reason, issues[0].FieldPath, issues[0].Correction
		if reason == "invalid_resolution_context" {
			code, disposition = "INTERNAL_ERROR", "terminate"
			message = "Operator could not construct a valid native request from verified state. Stop and report the product error; do not supply internal identities or replay completed actions."
		}
	}
	diagnostic := marshal(map[string]any{"source": "operator", "stage": "admission", "reason": reason, "field_path": field, "message": message, "correction": message, "execution_occurred": "no", "resubmission_safe": disposition == "correct-and-resubmit"})
	return stateReply{Error: &attemptadapter.Fault{Code: code, Message: message, InstancePath: field, Diagnostic: diagnostic, Effect: "none", Disposition: disposition}}
}
func marshal(v any) []byte { raw, _ := json.Marshal(v); return raw }
func (s *Service) stateEnvelope(raw []byte, seq int64, result stateReply) ([]byte, error) {
	var out map[string]any
	canonical, err := contracts.Canonicalize(raw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(canonical, &out)
	delete(out, "body")
	delete(out, "timeout_ms")
	out["kind"], out["seq"] = "response", seq
	if result.Error != nil {
		out["error"] = result.Error
	} else {
		out["result"] = result.Result
	}
	reply := marshal(out)
	_, err = s.target().Protocol().ValidateResponse(raw, reply)
	return reply, err
}

// observeTool owns the campaign-wide namespace before any local or native effect.
// A returned reply is either an admission denial or an immutable saved result.
func (s *Service) observeTool(raw []byte) (stateRequest, *stateReply, error) {
	canonical, err := contracts.Canonicalize(raw, contracts.OrdinaryLimit)
	if err != nil {
		return stateRequest{}, nil, err
	}
	var q stateRequest
	if json.Unmarshal(canonical, &q) != nil {
		return stateRequest{}, nil, ErrService
	}
	m := s.writer.Manifest()
	if q.Campaign != m.CampaignID || q.Launch != m.LaunchID {
		return stateRequest{}, nil, ErrService
	}
	t := s.attempts.Tools()
	observe := t.Observe
	if s.completion.finalizing || q.Operation == "engine.finish" || q.Operation == "engine.diagnostic_report" {
		observe = t.ObserveFinalization
	}
	_, replay, err := observe(campaign.ToolInput{CampaignID: q.Campaign, OperationID: q.ID, Operation: q.Operation, WorkerInstanceID: s.target().Live().Binding().WorkerInstanceID, RunRevision: q.Revision, Body: q.Body})
	if err != nil {
		if s.writer.Fence().Err() != nil {
			return stateRequest{}, nil, err
		}
		code := "IDEMPOTENCY_CONFLICT"
		if errors.Is(err, contracts.ErrLimit) {
			if err := s.beginFinalization(); err != nil {
				return stateRequest{}, nil, err
			}
			code = "LIMIT_EXCEEDED"
		} else if q.Revision != s.attempts.Status().RunRevision {
			code = "STATE_CHANGED"
		}
		reply := stateDenied(code)
		return q, &reply, nil
	}
	if replay {
		_, saved, err := t.Lookup(q.ID)
		if err != nil || saved == nil {
			return stateRequest{}, nil, ErrService
		}
		var reply stateReply
		if json.Unmarshal(saved, &reply) != nil {
			return stateRequest{}, nil, campaign.ErrCorrupt
		}
		return q, &reply, nil
	}
	if err := s.chargeFinalization(q); err != nil {
		return stateRequest{}, nil, err
	}
	return q, nil, nil
}

// handleSnapshot runs under the ordinary gate. The same tool ledger owns IDs for
// all state operations; a saved restore reply is replayed without another rebind.
func (s *Service) handleSnapshot(ctx context.Context, raw []byte, seq int64) ([]byte, error) {
	q, saved, err := s.observeTool(raw)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		return s.stateEnvelope(raw, seq, *saved)
	}
	t := s.attempts.Tools()
	// Reserve native audit before any possible external mutation.
	reservation := "state:" + contracts.RawDigest([]byte(q.ID))[7:]
	if _, err = s.writer.AppendReserving(campaign.Entry{RunRevision: q.Revision, Kind: "state.admitted", Metadata: marshal(map[string]any{"operation_id": q.ID, "operation": q.Operation})}, reservation, 2<<20); err != nil {
		return nil, err
	}
	reply, next, err := s.stateOperation(ctx, q, reservation)
	if err != nil {
		return nil, err
	}
	response, err := s.stateEnvelope(raw, seq, reply)
	if err != nil {
		return nil, err
	}
	if err = t.Finish(q.ID, marshal(reply), 0); err != nil {
		return nil, err
	}
	if next != nil {
		// Keep the verified replacement available to terminal cleanup even if adoption
		// fails. Guest execution is still serialized and the watcher remains paused.
		s.live.Store(next)
		if err = s.broker.Rebind(int64(next.Live().Binding().RunRevision), next.Binding()); err != nil {
			return nil, err
		}
	}
	_, err = s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "state.completed", Metadata: marshal(map[string]any{"operation_id": q.ID, "snapshot_admissions": s.snapshotAdmissions, "snapshot_bytes": s.snapshotBytes})}, reservation, true)
	if err == nil {
		s.restoring.Store(false)
	}
	return response, err
}

func (s *Service) remaining() map[string]int64 {
	parsed, _ := s.target().Protocol().ValidateEngineContext(s.config.Prepared.Context())
	limits := map[string]int64{}
	for k, v := range parsed["remaining_limits"].(map[string]any) {
		n, _ := v.(json.Number).Float64()
		limits[k] = int64(n)
	}
	limits["campaign_time_ms"] = max(0, time.Until(s.deadline).Milliseconds())
	limits["attempt_admissions"] -= s.attempts.Status().Admissions
	reads := s.attempts.Tools().Usage()
	limits["observation_reads"] -= reads.Requests
	limits["observation_bytes"] -= reads.Bytes + reads.Reserved
	objects, bytes := s.config.Prepared.ArtifactUsage()
	limits["artifact_objects"] -= objects + s.artifacts.objects
	limits["artifact_bytes"] -= bytes + s.artifacts.bytes
	limits["model_turns"] -= s.model.turns
	limits["model_tokens"] -= s.model.tokens
	limits["snapshot_admissions"] -= s.snapshotAdmissions
	limits["snapshot_bytes"] -= s.snapshotBytes
	return limits
}

func (s *Service) stateAudit(q stateRequest, reservation, kind string, request, response []byte) error {
	entry := campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: kind, Metadata: marshal(map[string]any{"operation_id": q.ID})}
	for _, part := range []struct {
		role string
		raw  []byte
	}{{"request", request}, {"response", response}} {
		if len(part.raw) > 0 {
			if len(part.raw) > 256<<10 {
				return ErrService
			}
			entry.Content = append(entry.Content, campaign.Content{Role: part.role, MediaType: "application/json", Bytes: part.raw})
		}
	}
	_, err := s.writer.AppendStored(entry, reservation, false)
	return err
}
func (s *Service) stateNative(ctx context.Context, q stateRequest, reservation string, p interceptor.PreparedOperation, decode func(interceptor.Response) (interceptor.Response, error)) (interceptor.Response, error) {
	if err := s.stateAudit(q, reservation, "state.native-intent", p.Bytes(), nil); err != nil {
		return interceptor.Response{}, err
	}
	if err := ctx.Err(); err != nil {
		return interceptor.Response{}, err
	}
	return interceptor.Call(p.Request().Operation, p.Request().RequestID,
		func() (interceptor.Response, error) { return s.config.Peer.Execute(ctx, p) },
		func(r interceptor.Response) error {
			return s.stateAudit(q, reservation, "state.native-result", nil, boundedResponse(r))
		},
		decode)
}

func snapshotMetadata(cp interceptor.Checkpoint, campaignID string) map[string]any {
	result := map[string]any{"campaign_id": campaignID, "source_session": cp.SourceSessionID, "checkpoint_id": cp.ID, "label": cp.Label, "description": cp.Description, "created_at": cp.CreatedAt.UTC().Format(time.RFC3339Nano), "status": cp.Status, "canonical_size_bytes": cp.CanonicalSizeBytes}
	// Native metadata names the parent checkpoint but not its source session.
	// Do not invent a parent handle from the child's source session.
	return result
}
func (s *Service) compatibleSnapshot(cp interceptor.Checkpoint) bool {
	var source struct {
		Environment string `json:"environment_digest"`
		Application string `json:"application_digest"`
	}
	_ = json.Unmarshal(s.target().Live().Export().NativeJSON(), &source)
	return (cp.CampaignID == "" || cp.CampaignID == s.target().Live().CampaignID()) && cp.EnvironmentDigest == source.Environment && cp.AppDigest == source.Application && cp.CanonicalSizeBytes >= 0 && cp.CanonicalSizeBytes <= contracts.MaxSafeInteger
}
func readError(err error) (stateReply, error) {
	var remote *interceptor.RemoteError
	if errors.Is(err, interceptor.ErrRequest) {
		return stateDenied("INVALID_ARGUMENTS", contracts.ArgumentError{Reason: "invalid_resolution_context"}), nil
	}
	if errors.As(err, &remote) && remote.Response.Status == 403 && remote.Response.Code() == "session_not_in_campaign" {
		return stateDenied("POLICY_DENIED"), nil
	}
	if errors.As(err, &remote) && remote.Response.Status == 404 && remote.Response.Code() == "checkpoint_not_found" {
		return stateDenied("SNAPSHOT_NOT_FOUND"), nil
	}
	return stateReply{}, err
}

// Retain exact native checkpoint metadata as host evidence; only its bounded
// public projection enters the harness response.
func (s *Service) retainCheckpoint(q stateRequest, reservation string, cp interceptor.Checkpoint) error {
	return s.stateAudit(q, reservation, "state.checkpoint", nil, marshal(cp))
}
