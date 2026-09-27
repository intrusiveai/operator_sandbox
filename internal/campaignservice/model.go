//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
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
	ProfileDigest string
	Tools         []byte
	// Qualified upper bound on native input tokens, including tools and message
	// framing. This is a host profile bound, never an estimate supplied by a guest.
	MaximumPromptTokens int64
}
type modelState struct {
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
		s.Stop(ErrService)
	}
	return response, nil
}
func (s *Service) modelOperation(ctx context.Context, q stateRequest) (stateReply, error) {
	value, err := s.target().Protocol().ValidateModelRequest(s.model.policy, q.Body)
	if err != nil {
		return stateDenied("POLICY_DENIED"), nil
	}
	native := value["request"].(map[string]any)
	completion, err := s.target().Protocol().ModelOutputLimit(q.Body)
	if err != nil {
		return stateDenied("POLICY_DENIED"), nil
	}
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
	request := marshal(native)
	meta := marshal(map[string]any{"operation_id": q.ID, "profile_digest": s.config.Model.ProfileDigest, "model_turns": s.model.turns + 1, "model_tokens": s.model.tokens + reserve, "reserved_tokens": reserve, "started_at": started.UTC().Format(time.RFC3339Nano)})
	if _, err = s.writer.AppendReserving(campaign.Entry{RunRevision: q.Revision, Kind: "model.intent", Metadata: meta, Content: []campaign.Content{{Role: "provider-request", MediaType: "application/json", Bytes: request}}}, reservation, campaign.MaxContentBytes+2*(campaign.MaxEventBytes+1)); err != nil {
		return stateReply{}, err
	}
	s.model.turns++
	s.model.tokens += reserve
	response, callErr := s.config.Model.Provider.Generate(ctx, request)
	// Record only native bytes; private transport errors can contain secrets.
	outcome := "response"
	if callErr != nil {
		outcome = "unknown"
	}
	if len(response) > campaign.MaxContentBytes {
		outcome = "invalid-response"
		response = nil
	}
	entry := campaign.Entry{RunRevision: q.Revision, Kind: "model.exchange", Metadata: marshal(map[string]any{"operation_id": q.ID, "outcome": outcome, "elapsed_ms": time.Since(started).Milliseconds(), "reserved_tokens": reserve})}
	if len(response) > 0 {
		entry.Content = []campaign.Content{{Role: "provider-response", MediaType: "application/json", Bytes: response}}
	}
	if _, err = s.writer.AppendStored(entry, reservation, true); err != nil {
		return stateReply{}, err
	}
	if callErr != nil {
		return modelFailure("OUTCOME_UNKNOWN", "unknown"), nil
	}
	if outcome != "response" || ctx.Err() != nil {
		return modelFailure("OUTCOME_UNKNOWN", "unknown"), nil
	}
	result := map[string]any{"codec_id": value["codec_id"], "profile_id": value["profile_id"], "profile_digest": value["profile_digest"], "receipt_id": "model-" + termination.NewRequestID(), "response": json.RawMessage(response)}
	body := marshal(result)
	_, err = s.target().Protocol().ValidateModelExchange(s.model.policy, q.Body, body)
	if err != nil {
		return modelFailure("INTERNAL_ERROR", "known"), nil
	}
	usage, err := s.target().Protocol().ModelUsage(body)
	if err != nil {
		return modelFailure("INTERNAL_ERROR", "known"), nil
	}
	if !usage.Known {
		return modelFailure("OUTCOME_UNKNOWN", "unknown"), nil
	}
	input, total := usage.InputTokens, usage.TotalTokens
	if input > s.config.Model.MaximumPromptTokens || total > reserve {
		return modelFailure("INTERNAL_ERROR", "known"), nil
	}
	charged := s.model.tokens - reserve + total
	if _, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "model.usage", Metadata: marshal(map[string]any{"operation_id": q.ID, "model_turns": s.model.turns, "model_tokens": charged, "actual_tokens": total})}, "", false); err != nil {
		return stateReply{}, err
	}
	s.model.tokens = charged
	if usage.ToolCalls > loop.Calls {
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
