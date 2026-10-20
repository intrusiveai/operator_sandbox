//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type resolvedAttempt struct {
	InputDigest string
	Plan        []byte
}

// The ordinary service gate serializes this map. Recovery never resumes requests;
// the immutable journal retains both the submitted request and resolved plan.
func (s *Service) resolveAttempt(ctx context.Context, id string, raw []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := contracts.Canonicalize(raw, contracts.ControlLimit)
	if err != nil {
		return nil, err
	}
	digest := contracts.RawDigest(canonical)
	if old, ok := s.resolvedAttempts[id]; ok {
		if old.InputDigest != digest {
			return nil, campaign.ErrConflict
		}
		return append([]byte(nil), old.Plan...), nil
	}
	var body map[string]any
	_ = json.Unmarshal(canonical, &body)
	if err := contracts.ValidateAttemptOrigin(body, s.completion.scenarios); err != nil {
		return nil, err
	}
	index := s.attempts.Status().HighWatermark + 1
	if index > contracts.MaxSafeInteger {
		return nil, contracts.ErrLimit
	}
	attemptID := fmt.Sprintf("attempt-%d", index)
	plan := map[string]any{"api_version": "operator.dev/engine-attempt-request/v1alpha2", "kind": "EngineAttemptRequest", "request_id": id, "attempt_id": attemptID, "attempt_index": index, "generation": 1, "thread_id": fmt.Sprintf("thread-%d", index), "origin": body["origin"], "generator": map[string]any{"kind": "operator-engine", "release_digest": s.writer.Manifest().ReleaseRecordDigest}, "pre_actions": []any{}, "cleanup": map[string]any{"delete_actions_after_observation": true}, "observation_selection": map[string]any{"mode": "all-permitted"}}
	for _, key := range []string{"scenario_id", "pre_actions", "observation_selection", "rationale", "strategy_provenance_ref", "technique_refs"} {
		if v, ok := body[key]; ok {
			plan[key] = v
		}
	}
	if receipt, ok := body["parent_attempt_receipt_id"].(string); ok {
		publication, e := s.attempts.FindPublication(receipt)
		if e != nil {
			return nil, &contracts.ArgumentError{Reason: "invalid_parent_receipt", FieldPath: "/parent_attempt_receipt_id", Correction: "Use the execution receipt from a completed attempt; rejected submissions have no execution receipt."}
		}
		saved, _, e := s.attempts.Lookup(publication.RequestID)
		if e != nil {
			return nil, e
		}
		parent, ok := s.history[saved.AttemptID]
		if !ok {
			return nil, &contracts.ArgumentError{Reason: "parent_not_completed", FieldPath: "/parent_attempt_receipt_id", Correction: "Select an attempt with verified completed lineage."}
		}
		if parent.CampaignGeneration >= contracts.MaxSafeInteger {
			return nil, contracts.ErrLimit
		}
		plan["parent_attempt_id"] = parent.Context.AttemptID
		plan["thread_id"] = parent.Context.ThreadID
		plan["generation"] = parent.CampaignGeneration + 1
	}
	input, err := s.resolveInput(id, body["input"].(map[string]any), "input")
	if err != nil {
		return nil, err
	}
	plan["payload"] = input
	invocation := map[string]any{"operation_id": body["operation_id"], "input_source": "payload", "media_type": input.MediaType}
	if principal, ok := body["caller_principal_id"]; ok {
		invocation["caller_principal_id"] = principal
	}
	if setup := plan["pre_actions"].([]any); len(setup) > 0 {
		injection, e := s.resolveInput(id, body["injection_input"].(map[string]any), "injection_input")
		if e != nil {
			return nil, e
		}
		if injection.MediaType != "application/json" || injection.Canonicalization != "jcs-v1" {
			return nil, &contracts.ArgumentError{Reason: "invalid_injection_input", FieldPath: "/injection_input", Correction: "Use JSON input or a canonical application/json payload artifact for injection setup."}
		}
		plan["payload"] = injection
		plan["carrier"] = input
		invocation["input_source"] = "carrier"
		for _, action := range setup {
			action.(map[string]any)["parameters"].(map[string]any)["payload_digest"] = injection.Digest
		}
		if retain, ok := body["retain_injections"].(bool); ok {
			plan["cleanup"] = map[string]any{"delete_actions_after_observation": !retain}
		}
	}
	plan["invocation"] = invocation
	encoded := marshal(plan)
	if _, err = s.target().Protocol().Catalog().Validate(contracts.AttemptPlanSchema, encoded, contracts.ControlLimit); err != nil {
		return nil, err
	}
	if _, err = s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "attempt.resolved", Metadata: marshal(map[string]any{"operation_id": id, "input_digest": digest, "attempt_id": attemptID, "attempt_index": index}), Content: []campaign.Content{{Role: "resolved-plan", MediaType: "application/json", Bytes: encoded}}}, "", false); err != nil {
		return nil, s.retentionFailure(id, "input", err)
	}
	if s.resolvedAttempts == nil {
		s.resolvedAttempts = map[string]resolvedAttempt{}
	}
	s.resolvedAttempts[id] = resolvedAttempt{digest, encoded}
	return append([]byte(nil), encoded...), nil
}

func (s *Service) resolveInput(id string, value map[string]any, field string) (interceptor.ArtifactDescriptor, error) {
	resolved, err := s.target().Protocol().ResolveDirectInput(value)
	if err != nil {
		return interceptor.ArtifactDescriptor{}, err
	}
	if resolved.ArtifactReceipt != "" {
		for _, u := range s.artifacts.uploads {
			if u.Receipt != resolved.ArtifactReceipt {
				continue
			}
			if u.Commit == nil || (u.Purpose != "payload" && (field != "input" || u.Purpose != "carrier")) {
				break
			}
			data, e := s.readUpload(u)
			if e != nil {
				return interceptor.ArtifactDescriptor{}, s.retentionFailure(id, "input", e)
			}
			if s.target().Protocol().ValidateArtifactContent(u.Begin, data) != nil {
				return interceptor.ArtifactDescriptor{}, s.retentionFailure(id, "input", campaign.ErrCorrupt)
			}
			return u.Artifact, nil
		}
		return interceptor.ArtifactDescriptor{}, &contracts.ArgumentError{Reason: "invalid_artifact_receipt", FieldPath: "/" + field + "/artifact_receipt", Correction: "Use direct input or a committed payload/carrier receipt returned by artifact publication in this campaign. Do not invent a digest or repeat a descriptor."}
	}
	desc, err := s.retainContent(id, "payload", resolved.Bytes, resolved.MediaType, resolved.Canonicalization)
	var argument *contracts.ArgumentError
	if err != nil && !errors.As(err, &argument) {
		return desc, s.retentionFailure(id, "input", err)
	}
	return desc, err
}

// Reuse the budgeted upload store internally; no additional guest operations or
// receipts are required for direct input. All parts are retained before returning.
func (s *Service) retainContent(id, purpose string, data []byte, media, canonicalization string) (interceptor.ArtifactDescriptor, error) {
	desc := interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(data), SizeBytes: int64(len(data)), MediaType: media, Canonicalization: canonicalization}
	q := stateRequest{ID: id, Revision: s.attempts.Status().RunRevision, Operation: "engine.artifact_begin"}
	q.Body = marshal(map[string]any{"purpose": purpose, "artifact": desc})
	wire := map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 0, "campaign_id": s.writer.Manifest().CampaignID, "launch_id": s.writer.Manifest().LaunchID, "run_revision": q.Revision, "call_id": "retention", "operation_id": id, "operation": q.Operation, "timeout_ms": 30000, "body": json.RawMessage(q.Body)}
	r, err := s.artifactOperation(q, marshal(wire))
	if err != nil {
		return desc, err
	}
	if r.Error != nil {
		return desc, &contracts.ArgumentError{Reason: r.Error.Code, FieldPath: "/input", Correction: "Input cannot be retained within the campaign artifact allowance."}
	}
	u := s.artifacts.uploads[r.Result.(map[string]any)["upload_id"].(string)]
	if len(data) > 0 {
		q.Operation = "engine.artifact_put_part"
		q.Body = marshal(map[string]any{"upload_id": u.ID, "offset": 0, "content": base64.StdEncoding.EncodeToString(data)})
		r, err = s.artifactOperation(q, nil)
		if err != nil {
			return desc, err
		}
		if r.Error != nil {
			return desc, ErrService
		}
	}
	q.Operation = "engine.artifact_commit"
	q.Body = marshal(map[string]any{"upload_id": u.ID})
	r, err = s.artifactOperation(q, nil)
	if err != nil {
		return desc, err
	}
	if r.Error != nil {
		return desc, errors.New(r.Error.Code)
	}
	return desc, nil
}
