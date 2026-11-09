package contracts

import (
	"errors"
	"strconv"
	"strings"
)

// ModelValidationError contains validation facts only, never response values or
// the schema library's error text. Paths are relative to the model result body.
// It changes diagnostics, not the set of admitted responses.
type ModelValidationError struct {
	Stage       string
	Reason      string
	FieldPath   string
	PathOmitted bool
	Cause       error
}

func (e *ModelValidationError) Error() string {
	return "model validation: " + e.Reason + " at " + e.FieldPath
}
func (e *ModelValidationError) Unwrap() error { return e.Cause }
func modelValidation(stage, reason, path string) error {
	safe, ok := SafeModelPath(path)
	return &ModelValidationError{Stage: stage, Reason: reason, FieldPath: safe, PathOmitted: !ok, Cause: ErrProtocol}
}

// SafeModelPath admits only published field names and bounded array indices.
// Dynamic tool argument keys, provider values and arbitrary unknown keys cannot
// become terminal diagnostic text.
func SafeModelPath(path string) (string, bool) {
	if len(path) > 256 || (path != "" && !strings.HasPrefix(path, "/")) {
		return "", false
	}
	if path == "" {
		return "", true
	}
	for _, field := range strings.Split(path[1:], "/") {
		if n, err := strconv.Atoi(field); err == nil && n >= 0 && n < 10000 {
			continue
		}
		if !strings.Contains(modelDiagnosticFields, " "+field+" ") {
			return "", false
		}
	}
	return path, true
}

func modelDecodeError(err error) error {
	reason, path := "invalid_json", ""
	var argument *ArgumentError
	if errors.As(err, &argument) && argument.Reason == "duplicate_json_key" {
		reason, path = argument.Reason, argument.FieldPath
	}
	if errors.Is(err, ErrLimit) {
		reason = "response_encoding_limit"
	}
	e := modelValidation("decoding", reason, path).(*ModelValidationError)
	e.Cause = err
	// Duplicate-key decoding may suppress a provider-specific path. Do not imply
	// that an empty path identifies the root when its original key was withheld.
	e.PathOmitted = path == "" && reason == "duplicate_json_key"
	return e
}
func modelSchemaError(err error, prefix string) error {
	e := modelValidation("schema", "unsupported_response_structure", prefix+conclusionSchemaPath(err)).(*ModelValidationError)
	e.Cause = ErrSchema
	return e
}

const modelDiagnosticFields = " request accepted_prediction_tokens access_programs additionalModelResponseFields annotations any args arguments async audio audio_tokens auto avgLogprobs background billing blockReason blockReasonMessage blocked budget_tokens cacheDetails cacheReadInputTokens cacheTokensDetails cacheWriteInputTokens cache_creation cache_creation_input_tokens cache_miss_reason cache_missed_input_tokens cache_read_input_tokens cache_write_tokens cachedContentTokenCount cached_tokens call_id caller candidates candidatesTokenCount candidatesTokensDetails canonicalization category check_offset checkpoint_id choices citationMetadata citationSources citations code codec_id comparison_response_id completed_at completion_tokens completion_tokens_details container content content_filter_offsets content_filter_raw content_filter_results content_filters context context_budget conversation createTime created created_at cyber data description detected diagnostics digest effort encrypted_content endIndex end_offset engine_tbt_ms engine_ttft_ms engine_ttlt_ms ephemeral_1h_input_tokens ephemeral_5m_input_tokens error explanation filtered finishMessage finishReason finish_reason format frequency_penalty function functionCall functionResponse function_call hate id image_gen image_tokens includeThoughts incomplete_details index indirect_attack inference_geo input inputSchema inputTokens input_schema input_tokens input_tokens_details instruction_role instructions is_error jailbreak json latency latencyMs latency_checkpoint license logprobs max_completion_tokens max_output_tokens max_tokens max_tool_calls media_type message metadata metrics modality mode model modelStage modelStatus modelVersion moderation name namespace near_prompt_limit num_requests object output outputTokens output_schema output_tokens output_tokens_details parallel_tool_calls parameters parts payer performanceConfig phase pre_inference_ms presence_penalty previous_response_id probability probabilityScore profanity profile_digest profile_id prompt promptFeedback promptTokenCount promptTokensDetails prompt_cache_diagnostics prompt_cache_key prompt_cache_options prompt_cache_retention prompt_filter_results prompt_index prompt_tokens prompt_tokens_details protected_material_code protected_material_text reason reasoning reasoningContent reasoningText reasoning_tokens receipt_id redactedContent refusal rejected_prediction_tokens remaining_model_tokens remaining_model_turns remaining_prompt_tokens requested_completion_tokens reserved_tokens response responseId response_models retirementTime role routing safetyRatings safety_identifier self_harm serverToolUsage server_tool_use serviceTier service_tbt_ms service_tier service_ttft_ms service_ttlt_ms serving_pipereplica severity severityScore sexual signature size_bytes source_session source_type startIndex start_offset status stopReason stop_details stop_reason stop_sequence store strict summary system_fingerprint temperature text text_tokens thinking thinkingBudget thinkingLevel thinking_config thinking_tokens thought thoughtSignature thoughtsTokenCount tokenCount toolChoice toolResult toolSpec toolUse toolUseId toolUsePromptTokenCount toolUsePromptTokensDetails tool_call_id tool_calls tool_choice tool_usage tool_use_id tools tools_digest top_logprobs top_p totalTokenCount totalTokens total_tokens trafficType truncation ttl type uri usage usageMetadata user user_visible_ttft_ms verbosity violence web_fetch_requests web_search web_search_requests "
