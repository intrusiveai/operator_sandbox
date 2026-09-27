# Native Bedrock Converse codec

`bedrock-converse-text-tools-v1` MUST use the shared relay envelope and closed
`bedrock-converse-{common,request,response}` schemas. Native fields follow the
[Converse API](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html),
consulted 2026-09-27. The admission rules below are Operator's contract.

## Binding and history

EngineContext settings MUST contain `max_tokens` and the canonical ordered native
`tools_digest`. The host profile MUST fix the regional route and model independently
of the request. Converse has no body model selector or response model echo.

Requests MUST contain the exact frozen prompt as a single `system` text block,
`messages` and `inferenceConfig.maxTokens` within the host cap. Enabled tools MUST
use `toolConfig.tools` with native `toolSpec` definitions and `toolChoice` of
`auto` or `any`. Definitions MUST match the host projection and have unique names.
Tool-free requests MUST omit `toolConfig` and MUST NOT contain historical native
tool blocks; compaction MUST encode the history to summarize as text.

Messages MUST begin and end with a user turn. Assistant content MAY contain text,
native `toolUse` objects and native `reasoningContent`. Every tool-use batch MUST
have all correlated `toolResult` blocks in the immediately following user turn,
once each and in order, before ordinary text. IDs MUST be unique across retained
history. Tool input MUST remain a JSON object. Unknown tool names and malformed
local arguments MUST remain correctable dispatcher errors.

Reasoning text, optional signatures and redacted content MUST be preserved in
order. Redacted content MUST be canonical padded base64. Unknown fields, hosted
tools, media, streaming and additional inference parameters MUST fail explicitly.

## Results and accounting

Results MUST retain the native output message, stop reason, usage and supported
metadata. `tool_use` MUST contain calls; `end_turn` and `stop_sequence` MUST NOT.
Returned calls MUST NOT reuse history IDs or appear after a tool-free request.
Filtered, truncated, malformed and context-limit results MUST NOT dispatch calls.
Missing/null whole usage MUST yield `usage-unknown`, retain reservations and end
exploration. No model completion implicitly concludes the campaign.

Charged input MUST equal `inputTokens + cacheReadInputTokens + cacheWriteInputTokens`
with omitted cache counts treated as zero; charged output MUST equal `outputTokens`.
AWS documents the separate cache counts in its
[prompt caching guide](https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html).
Native `totalTokens` MUST match either uncached input plus output or the full
cache-inclusive total. Operator MUST always charge the cache-inclusive total
exactly once. This tolerance is a validation rule, not a claim about a qualified
live model's behavior. Supplied cache-write details MUST have unique TTLs in
`1h`, `5m` order and sum to `cacheWriteInputTokens`. Aggregates MUST fit the shared
safe integer range; output MUST stay within the request cap.

`BedrockContinuation` / `bedrock_continuation` MUST preserve the full native
assistant message and append one user batch with exactly the supplied correlated
text results. Missing, extra, reordered or mismatched IDs MUST fail. Restore-skipped
calls MUST receive the existing `TARGET_REVISION_CHANGED` result. Text-only output
MUST require an explicit next user turn before another generation.

Limits MUST remain 4 MiB per model body/envelope, 4,096 history messages, 128 tools,
1,024 blocks per message and 1,048,576 characters per content/result string, further
narrowed by campaign limits. Native response bytes MUST remain available for
journaling; derived metrics MUST NOT replace them.

## Verification

Go and Python MUST execute `fixtures/bedrock-model-codec.json`; its generator MUST
remain independent of validator implementations. Fixtures cover policy binding,
history correlation, cache accounting, unknown usage, reasoning preservation,
continuation and rejected unsupported semantics. Host integration MUST verify
single execution on replay, cumulative cache-inclusive charging across restore,
and termination after unknown usage or excessive input. Synthetic tests do not
qualify real model routes, credentials or the container runtime.
