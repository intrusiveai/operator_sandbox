# Native Anthropic Messages codec

`anthropic-messages-text-tools-v1` MUST use the shared model relay envelope and
the closed `anthropic-messages-{common,request,response}` schemas. Its native
message layout follows the [Messages API](https://platform.claude.com/docs/en/api/messages/create),
consulted 2026-09-27. The limits and admission rules below are Operator's contract.

## Request and startup binding

The host-selected EngineContext settings MUST contain `max_tokens`, `thinking`,
explicit `response_models` and the canonical ordered native `tools_digest`.
`thinking` MUST be a fixed disabled/adaptive/enabled configuration; enabled mode
MUST use a budget of at least 1,024 tokens below the request's output ceiling.
The harness MUST NOT change the selected thinking configuration between requests.

Native requests MUST preserve `model`, the exact frozen top-level `system` string,
`messages`, positive `max_tokens`, `stream: false`, `thinking`, `tools` and
`tool_choice`. Tools MUST use native `name`, optional `description` and
`input_schema`; optional tool `type` MUST equal `custom`. Tool choice MUST be
`auto`, `any` or `none`. Required tool use MUST have a nonempty catalog and MUST
NOT be combined with enabled/adaptive thinking. Compaction MUST select `none`.

Messages MUST begin with a user turn. Assistant content MUST use native text,
thinking, redacted-thinking or client tool-use blocks. User content MUST be text
or native text/tool-result blocks. Every assistant tool call MUST have one
correlated result in the immediately following user batch, in original order,
before any ordinary user text. IDs MUST be unique across retained history.
A generation request MUST end in a user turn with no unfinished tool calls;
completed assistant output MUST NOT become an implicit partial prefill.

Tool definitions MUST match the host projection exactly and have unique names.
Tool-use `input` MUST remain a native JSON object; schema/argument mistakes and
unknown local tool names remain correctable dispatcher errors. No hosted tools,
images, remote containers or guest transport overrides are admitted by this codec.
Unknown fields or unsupported content semantics MUST fail explicitly.

## Results, continuation and accounting

The result MUST preserve its native message and content-block order. Native tool
calls MUST be distinct and MUST NOT reuse a historical ID. `tool_use` completion
MUST contain calls; normal end-turn/refusal results MUST NOT contain calls. A
suppressed-tool request MUST NOT return any calls.

`AnthropicContinuation` / `anthropic_continuation` MUST preserve the complete
assistant content, including opaque thinking signatures and redacted data, then
append one user batch containing exactly the supplied correlated tool results.
Wrong, missing, duplicated or reordered result IDs MUST fail. A text-only
continuation contains the assistant message; the harness MUST supply an explicit
next user turn before requesting another generation. Restore-skipped tool calls
MUST receive the existing structured `TARGET_REVISION_CHANGED` result.

Missing/null whole usage MUST remain unknown. With usage present, charged input
MUST include native `input_tokens`, cache-creation tokens and cache-read tokens;
absent/null optional cache counts contribute zero. Output MUST use `output_tokens`.
The aggregate MUST fit the shared safe integer range. Supplied cache-creation
breakdowns MUST match their total; supplied thinking-token details MUST fit output
usage. Output MUST not exceed the request cap. Server-tool usage MUST be absent,
null or zero. Full native usage and diagnostic fields MUST be retained.

Shared `ModelOutputLimit` / `model_output_limit` and `ModelUsage` / `model_usage`
helpers MUST validate native bodies before deriving accounting. Their internal
metrics MUST NOT replace the native wire response. Unknown usage MUST retain the
host reservation and terminate exploration, as defined by the campaign relay.
Snapshot restore MUST preserve charged model turns and tokens, including caches.

Disposition MUST be `tool-calls`, `text`, `refusal`, `truncated` or `usage-unknown`.
`max_tokens`, `pause_turn` and `model_context_window_exceeded` MUST be truncated
outcomes whose calls cannot execute. Refusal stop details MUST remain a refusal.
No response implicitly completes a campaign.

Limits MUST remain 4 MiB per model body/envelope, 4,096 history messages, 128 tools,
1,024 blocks per message and 1,048,576 characters per content/argument-result string,
further narrowed by the configured campaign limits.

## Verification

Both language runners MUST execute `fixtures/anthropic-model-codec.json`.
The fixture generator MUST not import validator implementations. Cases cover
startup policy, native exchange preservation, cache accounting, signed thinking
continuation, profile/prompt/tool changes, incomplete batches and terminal results.
Host integration MUST cover idempotent generation, cached-token charges across
restore, unknown usage and excessive input. These tests use synthetic provider
responses; real model/route/credential qualification remains required.
