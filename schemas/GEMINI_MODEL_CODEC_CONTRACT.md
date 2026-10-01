# Native Gemini GenerateContent codec

`gemini-text-tools-v1` MUST use the closed `gemini-{common,request,response}`
schemas and shared relay envelope. This codec serves the configured Gemini
Developer API or Vertex route; the host MUST select the endpoint and credentials.
Native field semantics follow the [GenerateContent reference](https://ai.google.dev/api/generate-content),
consulted 2026-09-27. The limits and validation rules below are Operator's contract.

## Startup and requests

EngineContext settings MUST contain `max_output_tokens`, `thinking_config`, an
explicit `response_models` list and the ordered native `tools_digest`. Thinking
configuration MUST remain pinned, including when empty to use model defaults.
It MAY select a thinking budget or level, but MUST NOT select both. Model-specific
supported values MUST be qualified with the selected route.

Requests MUST contain the exact frozen prompt in `systemInstruction.parts[0].text`,
native `contents`, one candidate, a bounded `maxOutputTokens`, pinned
`thinkingConfig`, and native tools/tool choice. The catalog MUST contain zero or
one function-declaration group with unique names and package-owned
`parametersJsonSchema` definitions. Modes MUST be `AUTO`, `ANY` or `NONE`; `ANY`
MUST require a nonempty catalog. Compaction MUST use `NONE`. Requests MUST NOT
select another model, remote cache, hosted tool, safety override or endpoint.

History MUST start and end with a user turn. Every model function-call batch MUST
have all results in the immediately following user turn, once each in original
order, before ordinary text. Results MUST match both the function name and the
presence/value of any native ID. Explicit IDs MUST be unique across retained
history. Missing native argument objects MUST remain absent in retained native
bytes and MUST mean an empty argument object when selected for local dispatch.
Unknown local names and invalid arguments MUST produce correctable local errors.

## Native continuation

The complete model content MUST be preserved, including part order, thought
markers and opaque signatures. Signature-only parts MUST be retained. The codec
MUST NOT fabricate signatures or convert thought text into tool calls. A
`functionCall` explicitly marked as a thought MUST fail validation.

`GeminiContinuation` / `gemini_continuation` MUST receive exactly one local
`{part_index, content}` result per function-call part. `part_index` MUST be the
zero-based position in that response's complete native parts array, including
text/thought/signature parts in the indexing. Selection MUST also be scoped to
the validated response receipt by the caller; a part index is not an operation ID.
This keeps repeated same-name calls without native IDs unambiguous. Wrong,
missing, extra or reordered positions MUST fail.

The helper MUST append the unchanged model content followed by one user batch
of native `functionResponse` parts. Each result MUST preserve the native name
and ID if present, and carry the supplied result string as `response.output`.
It MUST NOT insert a native ID when none was supplied. Restore-skipped calls MUST
receive the existing structured `TARGET_REVISION_CHANGED` result string.
Text-only continuation MUST require an explicit next user turn before generation.

## Results and usage

The codec MUST accept one candidate, or a prompt-block response with no candidate.
An unexplained empty response MUST fail. Completed candidates MUST contain model
content and a `modelVersion` from the pinned allowlist. A prompt-block response
MAY omit `modelVersion`; its route remains fixed by the host. Returned explicit
IDs MUST NOT reuse history IDs. Tool-free requests MUST NOT return function calls.

Known input MUST use `promptTokenCount`, which already includes cached input;
cached counts MUST NOT be charged again and MUST fit input. Known output MUST
include `candidatesTokenCount + thoughtsTokenCount`. These optional protobuf
counts MAY be omitted only as zero; their sum with input MUST equal the supplied
`totalTokenCount`. The output sum MUST fit the request cap. Present text modality
breakdowns MUST match their totals. Hosted-tool usage MUST be absent or zero.
All totals MUST fit the shared safe integer range. Optional
`usageMetadata.trafficType` MUST be one of `TRAFFIC_TYPE_UNSPECIFIED`, `ON_DEMAND`,
`PROVISIONED_THROUGHPUT`, `ON_DEMAND_PRIORITY` or `ON_DEMAND_FLEX`, as defined by
[Vertex UsageMetadata](https://docs.cloud.google.com/php/docs/reference/cloud-ai-platform/latest/V1.GenerateContentResponse.UsageMetadata.TrafficType).
It MUST remain metadata and MUST NOT change token accounting. Null, numeric and
unknown traffic types MUST fail.

Missing/null whole usage MUST remain unknown, retain reservations and terminate
exploration. Prompt blocking and safety/recitation outcomes MUST yield `filtered`;
partial, malformed or other incomplete outcomes MUST yield `truncated`. Only
known-usage `STOP` results may yield `text` or `tool-calls`. No completion implicitly
concludes the campaign. Native metadata MUST be preserved for journaling.

Limits MUST remain 4 MiB per model body/envelope, 4,096 history messages, 128
functions, 1,024 parts per message and 1,048,576 characters per text/result string,
further narrowed by campaign limits. Unsupported media, hosted actions and unknown
fields MUST fail explicitly. Signatures and tool results MUST stay with their
original model batch during history retention and compaction.

## Verification

Both language runners MUST execute `fixtures/gemini-model-codec.json`. Its
generator MUST be independent of validator implementations. Tests MUST cover
missing native IDs, repeated names, signature-only parts, ordered continuation,
startup pins, suppression, blocked prompts, cache/thought accounting and invalid
usage. Host integration MUST verify accounting across restore, replay protection
and terminal handling of unknown/excessive usage. Live route/model/credential
and container qualification remain separate from synthetic contract tests.
