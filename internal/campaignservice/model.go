//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/modelcodec"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

// ModelProvider is a trusted host dependency bound to an installed private
// profile. Generate makes exactly one native, non-streaming request, with no
// automatic retries. It must enforce response bounds and context cancellation,
// strip transport headers/credentials, and return only native response JSON.
// Any Generate error is treated as a possibly dispatched, terminal outcome.
type ModelProvider interface {
	Generate(context.Context, []byte) ([]byte, error)
}
type ModelConfig struct {
	Provider      ModelProvider
	Policy        []byte
	ProfileDigest string
	Tools         []byte
	// Qualified upper bound on native input tokens, including tools and message
	// framing. This is a host profile bound, never an estimate supplied by a guest.
	MaximumPromptTokens int64
}
type modelState struct {
	codec         string
	limits        contracts.ContextLimits
	policy        []byte
	turns, tokens int64
}

func (s *Service) handleModel(ctx context.Context, raw []byte, seq int64) ([]byte, error) {
	q, saved, err := s.observeTool(raw)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		return s.stateEnvelope(raw, seq, *saved)
	}
	reply, err := s.modelOperation(ctx, q)
	if err != nil {
		return nil, err
	}
	response, err := s.stateEnvelope(raw, seq, reply)
	if err != nil {
		return nil, err
	}
	if err = s.attempts.Tools().Finish(q.ID, marshal(reply), 0); err != nil {
		return nil, err
	}
	if reply.Error != nil && reply.Error.Disposition == "terminate" {
		if d := s.ModelFailure(); d != nil {
			s.Stop(modelprovider.FailureError(*d))
		} else {
			s.Stop(ErrService)
		}
	}
	return response, nil
}
func (s *Service) modelOperation(ctx context.Context, q stateRequest) (stateReply, error) {
	relay, err := s.target().Protocol().ValidateModelRequest(q.Body)
	if err != nil {
		return stateDenied("POLICY_DENIED"), nil
	}
	request, err := modelcodec.Build(s.model.policy, q.Body)
	if err != nil {
		return stateReply{}, err
	}
	nativeValue, err := contracts.Decode(request, contracts.OrdinaryLimit)
	if err != nil {
		return stateReply{}, err
	}
	native := nativeValue.(map[string]any)
	completion := s.model.limits.MaximumCompletionTokens
	reserve := s.config.Model.MaximumPromptTokens + completion
	remaining := s.remaining()
	var loop struct {
		Turns int64 `json:"max_model_turns"`
		Calls int64 `json:"max_tool_calls_per_response"`
	}
	_ = json.Unmarshal(s.writer.Manifest().HarnessLimits, &loop)
	if remaining["model_turns"] < 1 || s.model.turns >= loop.Turns || reserve > remaining["model_tokens"] {
		if err := s.beginFinalization(); err != nil {
			return stateReply{}, err
		}
		return stateDenied("LIMIT_EXCEEDED"), nil
	}
	if err := ctx.Err(); err != nil {
		return stateReply{}, err
	}
	started := time.Now()
	reservation := "model:" + contracts.RawDigest([]byte(q.ID))[7:]
	sizes, err := modelcodec.MeasureModelRequest(s.model.codec, request)
	if err != nil {
		return stateReply{}, err
	}
	meta := marshal(map[string]any{"operation_id": q.ID, "profile_digest": s.config.Model.ProfileDigest, "model_turns": s.model.turns + 1, "model_tokens": s.model.tokens + reserve, "reserved_tokens": reserve, "started_at": started.UTC().Format(time.RFC3339Nano), "request_sizes": sizes, "context_limits": s.model.limits})
	if _, err = s.writer.AppendReserving(campaign.Entry{RunRevision: q.Revision, Kind: "model.intent", Metadata: meta, Content: []campaign.Content{{Role: "provider-request", MediaType: "application/json", Bytes: request}}}, reservation, campaign.MaxContentBytes+2*(campaign.MaxEventBytes+1)); err != nil {
		return stateReply{}, err
	}
	s.model.turns++
	s.model.tokens += reserve
	var response []byte
	var observed modelprovider.Diagnostic
	var callErr error
	evidence := modelprovider.ResponseEvidence{Omission: "provider_evidence_unavailable"}
	if provider, ok := s.config.Model.Provider.(interface {
		GenerateWithEvidence(context.Context, []byte) ([]byte, modelprovider.Diagnostic, modelprovider.ResponseEvidence, error)
	}); ok {
		response, observed, evidence, callErr = provider.GenerateWithEvidence(ctx, request)
	} else if provider, ok := s.config.Model.Provider.(interface {
		GenerateWithMetadata(context.Context, []byte) ([]byte, modelprovider.Diagnostic, error)
	}); ok {
		response, observed, callErr = provider.GenerateWithMetadata(ctx, request)
	} else {
		response, callErr = s.config.Model.Provider.Generate(ctx, request)
		if d := modelprovider.Details(callErr); d != nil {
			observed = *d
		}
	}
	observed.OperationID = q.ID
	// Missing transport metadata is not invented for injected/legacy providers.
	if observed.Model == "" {
		observed.Model, _ = native["model"].(string)
	}
	outcome := "response"
	failureCode, effect := "", ""
	reason := ""
	if callErr != nil {
		outcome = "unknown"
		failureCode, effect = "OUTCOME_UNKNOWN", "unknown"
		if !observed.Valid() {
			observed = observed.WithFailure("unknown", "provider_failed")
		}
		if observed.Category == "http_rejection" {
			outcome = "http-rejected"
		} else if observed.Category == "response_validation" {
			outcome = "invalid-response"
		}
	} else if len(response) > campaign.MaxContentBytes {
		outcome = "invalid-response"
		failureCode, effect = "OUTCOME_UNKNOWN", "unknown"
		reason = "response_too_large"
	} else if ctx.Err() != nil {
		outcome = "unknown"
		failureCode, effect = "OUTCOME_UNKNOWN", "unknown"
		category := "cancellation"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			category = "timeout"
		}
		observed = observed.WithFailure(category, "read_failed")
	}
	result := map[string]any{"receipt_id": "model-" + termination.NewRequestID(), "response": json.RawMessage(response)}
	body := marshal(result)
	var budget contracts.ContextBudget
	var total int64
	var toolCalls int64
	if failureCode == "" {
		parsed, parseErr := modelcodec.Parse(s.model.codec, response)
		usage := parsed.Metrics
		if parseErr != nil {
			observed = observed.WithValidation(parseErr)
			reason = observed.Reason
			failureCode, effect = "INTERNAL_ERROR", "known"
		} else if !usage.Known {
			reason = "usage_unknown"
			failureCode, effect = "OUTCOME_UNKNOWN", "unknown"
		} else if usage.InputTokens > s.config.Model.MaximumPromptTokens || usage.TotalTokens > reserve {
			observed = observed.WithUsageRejection(usage, s.config.Model.MaximumPromptTokens, completion, reserve)
			reason = "usage_exceeds_reservation"
			failureCode, effect = "INTERNAL_ERROR", "known"
		} else if usage.OutputTokens > completion {
			observed = observed.WithValidation(&contracts.ModelValidationError{Stage: "adapter", Reason: "completion_allowance_exceeded", FieldPath: "/response/usage"})
			reason = observed.Reason
			failureCode, effect = "INTERNAL_ERROR", "known"
		} else if relay["purpose"] == "compaction" && usage.ToolCalls > 0 {
			observed = observed.WithValidation(&contracts.ModelValidationError{Stage: "adapter", Reason: "tools_not_requested", FieldPath: "/response"})
			reason = observed.Reason
			failureCode, effect = "INTERNAL_ERROR", "known"
		} else {
			total, toolCalls = usage.TotalTokens, usage.ToolCalls
			budget, err = contracts.NewContextBudget(s.model.limits, usage, completion, remaining["model_tokens"]-total, remaining["model_turns"]-1)
			if err == nil {
				result["context_budget"] = budget
				body = marshal(result)
				_, err = s.target().Protocol().ValidateModelExchange(s.model.policy, q.Body, body)
			}
			if err != nil {
				observed = observed.WithValidation(err)
				reason = observed.Reason
				failureCode, effect = "INTERNAL_ERROR", "known"
			}
		}
	}
	if reason != "" {
		observed = observed.WithFailure("response_validation", reason)
		outcome = "invalid-response"
	}
	metadata := map[string]any{"operation_id": q.ID, "outcome": outcome, "elapsed_ms": time.Since(started).Milliseconds(), "reserved_tokens": reserve}
	if failureCode == "" {
		metadata["context_budget"] = budget
	}
	var primary error
	if failureCode != "" {
		if !observed.Valid() {
			observed = modelprovider.Diagnostic{OperationID: q.ID}.WithFailure("unknown", "provider_failed")
		}
		// Rejected bytes use a separate content role and can never enter the
		// successful result or be delivered to Harness. Headers are excluded.
		response = nil
		if len(evidence.Body) > 0 && len(evidence.Body) <= campaign.MaxContentBytes {
			response = evidence.Body
			observed.RejectedResponse = modelprovider.RejectedResponse{State: "retained", Digest: contracts.RawDigest(response), SizeBytes: int64(len(response)), Redacted: evidence.Redacted}
		} else {
			omission := evidence.Omission
			if len(evidence.Body) > campaign.MaxContentBytes {
				omission = "response_too_large"
			}
			if omission == "" {
				omission = "provider_evidence_unavailable"
			}
			observed.RejectedResponse = modelprovider.RejectedResponse{State: "omitted", Omission: omission}
		}
		s.modelDiagnostic.CompareAndSwap(nil, &observed)
		metadata["diagnostic"] = observed
		primary = modelprovider.FailureError(observed)
	}
	entry := campaign.Entry{RunRevision: q.Revision, Kind: "model.exchange", Metadata: marshal(metadata)}
	if len(response) > 0 {
		role := "provider-response"
		if failureCode != "" {
			role = "provider-response-rejected-untrusted"
		}
		entry.Content = []campaign.Content{{Role: role, MediaType: "application/json", Bytes: response}}
	}
	if _, err = s.writer.AppendStored(entry, reservation, true); err != nil {
		if primary != nil {
			failed := observed
			failed.RejectedResponse = modelprovider.RejectedResponse{State: "unconfirmed", Omission: "retention_failed"}
			s.modelDiagnostic.CompareAndSwap(&observed, &failed)
			primary = modelprovider.FailureError(failed)
		}
		return stateReply{}, errors.Join(primary, err)
	}
	if failureCode != "" {
		return modelFailure(failureCode, effect), nil
	}
	charged := s.model.tokens - reserve + total
	if _, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "model.usage", Metadata: marshal(map[string]any{"operation_id": q.ID, "model_turns": s.model.turns, "model_tokens": charged, "actual_tokens": total})}, "", false); err != nil {
		return stateReply{}, err
	}
	s.model.tokens = charged
	if toolCalls > loop.Calls {
		if err := s.beginFinalization(); err != nil {
			return stateReply{}, err
		}
		return stateReply{Error: &attemptadapter.Fault{Code: "LIMIT_EXCEEDED", Message: "The model tool batch exceeds the campaign limit.", Effect: "known", Disposition: "gap-and-continue"}}, nil
	}
	return stateReply{Result: json.RawMessage(body)}, nil
}
func modelFailure(code, effect string) stateReply {
	return stateReply{Error: &attemptadapter.Fault{Code: code, Message: "Model execution cannot continue safely.", Effect: effect, Disposition: "terminate"}}
}
