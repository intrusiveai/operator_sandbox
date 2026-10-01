# Native OpenAI Responses codec

`openai-responses-text-tools-v1` MUST use the shared relay envelope and closed
`openai-responses-{common,request,response}` schemas. Native continuation follows
the [reasoning guide](https://developers.openai.com/api/docs/guides/reasoning);
field definitions follow the [official SDK](https://github.com/openai/openai-python/tree/main/src/openai/types/responses),
consulted 2026-09-27. Admission and bounds below are Operator's contract.

## Startup and request binding

EngineContext settings MUST contain `max_output_tokens`, `reasoning` (null or a
pinned configuration), explicit `response_models`, and the canonical ordered
native `tools_digest`. Model-specific reasoning controls MUST be qualified with
the selected route. The harness MUST NOT change them between requests.

Requests MUST carry the exact frozen `instructions`, selected `model`, native
`input` array, positive bounded `max_output_tokens`, `store: false`, `stream: false`,
`parallel_tool_calls: true`, `truncation: disabled` and plain-text format.
`include` MUST contain only `reasoning.encrypted_content`, ensuring compatibility
with stateless reasoning continuation. Remote conversation/previous-response
references and background execution MUST NOT be used.

Tools MUST be the exact package-owned native function projection with unique
names and explicit `strict: false`. Tool choice MUST be `auto`, `none` or
`required`; required use MUST have a nonempty catalog. Compaction MUST use `none`.
Hosted tools, programmatic callers, asynchronous calls, namespaces and additional
guest transport/model controls MUST fail explicitly.

## History and continuation

History MUST begin with a user message and end with a user message or completed
function-call result batch. Privileged instructions MUST appear only in the
pinned top-level field. Output messages, reasoning and function calls MUST retain
their native shape, order and IDs. Every historical function call MUST have one
correlated output, in order, before a new user turn or another model turn after
result consumption begins. Item IDs and call IDs MUST each be unique in retained
history. A refusal in an earlier completed turn MUST NOT prohibit later calls.

`ResponsesContinuation` / `responses_continuation` MUST retain every output item,
including encrypted reasoning, reasoning summaries/content and assistant `phase`.
Completed reasoning items MUST have nonempty encrypted content for stateless
replay. The helper MUST append one native `function_call_output` for each supplied
`{tool_call_id, content}` result, preserving any supported direct-caller field.
Wrong, missing, duplicate or reordered result IDs MUST fail. Argument strings
MUST remain unchanged even when malformed; dispatch MUST parse them separately.
Restore-skipped calls MUST receive the existing `TARGET_REVISION_CHANGED` result.

Text/refusal-only output MUST require an explicit next user turn before generation.
No model message or `final_answer` phase implicitly concludes a campaign. History
retention MUST keep reasoning/output items and their result batches together.

## Results and accounting

Results MUST preserve all supported native fields. A completed response MUST NOT
contain incomplete items, an error or incomplete details. Failed results MUST
carry their native error; incomplete results MUST carry incomplete details.
Refusals and actionable calls MUST NOT coexist in one response. Returned IDs
MUST NOT reuse history IDs; suppressed-tool requests MUST NOT return calls.
Supplied prompt, tool catalog, choice and output-ceiling echoes MUST match the
request. Explicit reasoning settings echoed by the provider MUST match as well;
additional supported resolved defaults MAY be retained. Echoed function declarations
MAY add `output_schema: null`; comparison MUST ignore only that null field, using
copies without changing retained native response bytes. Requests MUST NOT add it;
nonnull output schemas or any changed declaration MUST fail.

Response-only `frequency_penalty` and `presence_penalty` MUST be zero when present.
Optional `billing` MUST contain only a bounded nonempty `payer` string and MUST NOT
change usage accounting or authentication. Optional `tool_usage` MAY contain only
the closed image-generation token counters/details and web-search request counter;
every counter MUST be integer zero. Nonzero hosted-tool usage and unknown fields
MUST fail. These metadata shapes reflect the 2026-09-30 live qualification capture;
they do not enable hosted tools or new request controls.

Input/output token totals MUST use native `input_tokens` and `output_tokens`;
their sum MUST equal `total_tokens`. Cache-read/write detail counts MUST each fit
input, and reasoning details MUST fit output. Detail counts MUST NOT be added to
their already-inclusive totals. Output MUST fit the requested cap. Partial usage
objects MUST fail; missing/null whole usage MUST remain unknown and retain the
reservation, ending exploration under the existing host rules.

Known-usage completed responses MUST yield `text`, `tool-calls` or `refusal`.
Incomplete content-filter results MUST yield `filtered`; other incomplete,
failed or cancelled results MUST yield `truncated`, with no dispatch. Native
status/error details MUST remain available for journaling; this internal
classification MUST NOT replace them or authorize automatic provider retries.

Limits MUST remain 4 MiB per model body/envelope, 4,096 input items, 128 functions,
1,024 output items or message content blocks, and 1,048,576 characters per
text/argument/result string, further narrowed by campaign limits. Unsupported
fields and semantics MUST fail explicitly without silently losing continuation.

## Verification

Both validators MUST run `fixtures/responses-model-codec.json`, generated
independently of production implementations. Coverage MUST include startup pins,
native metadata, reasoning/phase continuation, malformed arguments, skipped calls,
history IDs, refusal, incomplete/failed results, forbidden remote state and usage.
Host integration MUST test replay protection, restore accounting, unknown usage
and excessive input. Live provider/model/credential and runtime qualification
remain separate from these synthetic tests.
