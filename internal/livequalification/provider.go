//go:build linux || darwin

package livequalification

import (
	"context"
	"encoding/json"
)

type generator interface {
	Generate(context.Context, []byte) ([]byte, error)
}

func (e *execution) provider(ctx context.Context, g generator) error {
	c, err := newConversation(e.q)
	if err != nil {
		return e.failedCheck("configuration", "invalid_probe_configuration")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = e.emit("pre_canceled_generation", "started", "no_dispatch_expected"); err != nil {
		return err
	}
	if _, err = g.Generate(canceled, jsonBytes(c.request)); err == nil {
		return e.failedCheck("pre_canceled_generation", "cancellation_not_observed")
	}
	if err = e.emit("pre_canceled_generation", "passed", "canceled_before_dispatch"); err != nil {
		return err
	}
	for i, check := range []string{"text", "tool_call_after_text", "text_after_tool"} {
		request := jsonBytes(c.body)
		if _, err = c.protocol.ValidateModelRequest(c.policy, request); err != nil {
			return e.failedCheck(check, "invalid_native_request")
		}
		if ctx.Err() != nil {
			return e.failedCheck(check, "canceled_before_dispatch")
		}
		if err = e.emit(check, "started", "single_generation_no_retry"); err != nil {
			return err
		}
		raw, err := g.Generate(ctx, jsonBytes(c.request))
		if err != nil {
			if err = e.emit(check, "uncertain", "provider_call_failed_no_retry"); err != nil {
				return err
			}
			return ErrCheck
		}
		result := map[string]any{"codec_id": c.body["codec_id"], "profile_id": c.body["profile_id"], "profile_digest": c.body["profile_digest"], "receipt_id": "qualification-receipt", "response": json.RawMessage(raw)}
		resultRaw := jsonBytes(result)
		if _, err = c.protocol.ValidateModelExchange(c.policy, request, resultRaw); err != nil {
			return e.failedCheck(check, "native_exchange_rejected")
		}
		usage, err := c.protocol.ModelUsage(resultRaw)
		if err != nil || !usage.Known {
			return e.failedCheck(check, "usage_unavailable")
		}
		if usage.InputTokens > e.q.profile.Settings().MaximumPromptTokens {
			return e.failedCheck(check, "prompt_allowance_exceeded")
		}
		disposition, err := c.protocol.ModelDisposition(resultRaw)
		expected := "text"
		if i == 1 {
			expected = "tool-calls"
		}
		if err != nil || disposition != expected || (i == 1 && usage.ToolCalls != 1) {
			return e.failedCheck(check, "unexpected_model_disposition")
		}
		record := e.q.record(e.id, check, "passed", "native_exchange_and_usage_validated")
		record.Usage = &usage
		if err = e.sink(record); err != nil {
			return err
		}
		if i < 2 {
			segment, err := c.continuation(resultRaw)
			if err != nil {
				return e.failedCheck("continuation", "invalid_tool_or_native_history")
			}
			if err = c.extend(segment, i == 0); err != nil {
				return e.failedCheck("continuation", "invalid_native_history")
			}
		}
	}
	if err = e.emit("inflight_cancellation", "not_run", "requires_controlled_live_fault"); err != nil {
		return err
	}
	if err = e.emit("uncertain_outcome", "not_run", "requires_controlled_live_fault"); err != nil {
		return err
	}
	return e.emit("identity_mechanism", "not_run", "requires_runner_identity_attestation")
}
