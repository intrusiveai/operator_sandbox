# Model context contract

Shared package 0.5.0 requires matching host and Harness package identities through
the existing embedded compatibility record. Earlier packages are incompatible.
This contract does not add token counting services or change execution authority.

## Frozen policy and response accounting

`EngineContext.model.context_limits` contains `maximum_prompt_tokens`,
`maximum_completion_tokens` and `warning_threshold_tokens`. These are local
allowances, not a claim about the provider's context window. Operator resolves
`context_warning_percent` (integer 1–99, default 80) before fingerprinting the
profile. The threshold is `max(1, floor(prompt * percent / 100))`.
The completion allowance agrees with the installed codec settings.

Every successful `engine.model_generate` result requires `context_budget`:
validated input/output/total tokens, requested completion tokens, reserved tokens,
remaining prompt tokens, `near_prompt_limit`, and remaining campaign model tokens
and turns. The [result schema](engine-model-generate-result.schema.json) defines
the closed fields. Go/Python exchange validation binds native usage, request,
policy and budget. Operator derives these fields after native response validation;
the internal native-result validator alone does not admit a complete wire result.
Duplicate delivery returns the original accounting snapshot. Restores preserve
the frozen policy and cumulative campaign budgets.

Remaining prompt tokens describe a completed call; they do not predict the next
request. Unknown usage is not zero. Hard overflows remain terminal, retain their
numeric diagnostics and do not gain a retry path. Baseline admission has no exact
token preflight; local estimates only guide Harness conversation reduction.

## Target details and tool selection

`CompactTargetContext` and `compact_target_context` produce the same compact
target index and canonical resource bytes. The complete verified target manifest
remains the authority. Ordinary operation input contracts stay in the index;
service, endpoint, table and file details have `details_reference` handles.
Resource handles are `target-<raw-sha256-hex>`. Tool guidance uses
`tool-guide:<tool_name>`. Both use existing inventoried reference files at
`context/<raw-sha256-hex>.json`. Startup validators regenerate and verify these
bytes, paths and identities; arbitrary unbound references remain invalid.
Reading these references grants no additional authority.

`model.tool_selection.injection_surfaces` is a sorted, unique frozen array derived
from permitted target actions. Empty means no injection setup branches. Shared
Go/Python tool projection filters setup branches by those surfaces and admitted
operations; it preserves all applicable exploratory options. The resulting tools
are bound by the existing codec tool digest and do not change after restore.
The full catalog remains available for release inventory checks.

Provider projections omit repeated JSON expansion and selected regex constraints.
The authoritative runtime schemas retain all limits, reference checks and
cross-field rules. Provider schema acceptance is not dispatch authorization.
OpenAI declarations retain object roots without forbidden root combinators.

`reference_read` and `observation_read` default omitted model-facing `max_bytes`
to 4096. Invalid supplied values are rejected, never clamped. Host observation
requests still carry an explicit maximum. Model-facing results include faithful
UTF-8 text when a complete selected text/JSON chunk decodes; otherwise they retain
base64 with an explicit binary/incomplete-decoding label. Source, offset, length,
availability, EOF and truncation remain authoritative. Full evidence is unchanged.

## Conversation reduction

Harness completes an admitted tool batch before reacting to a warning. It uses
the warning, newly added bytes and a local estimate calibrated against the last
validated usage, alongside independent byte limits. It checks remaining campaign
tokens and turns before a compaction call. Compaction uses the same route and
allowances, suppresses tool execution and selects whole native call/result groups.
The immutable task and exact returned identity ledger survive; omitted groups are
explicit. Summaries are model interpretation, never source evidence.
If a bounded request cannot be constructed or funded, the existing honest
unavailable finish path preserves `context-limit` or `budget-limit`; no target or
provider operation is replayed.

## Retained size facts

`model.intent.request_sizes` reports native request JSON bytes before transport
adaptation: total, instructions, tool definitions, conversation and other bytes.
The basis is `native-json-values-with-framing-remainder`. Chat counts its first
message as instructions and the remaining individual messages as conversation.
Responses counts instructions/input/tools; Anthropic counts system/messages/tools;
Bedrock counts system/messages/toolConfig; Gemini counts
systemInstruction/contents/tools. Each selected JSON value is serialized once.
Other is the remainder, including field names, separators and other settings.
These partitions sum to total bytes, not tokens or final HTTP wire bytes.

Exports and reports explicitly project size facts, frozen allowances and validated
budget snapshots with journal sequence and model operation identity. They do not
copy request text into diagnostic metadata or treat reservation as consumption.

Shared fixtures `compact-target-context.json` and `selected-model-tools.json`
bind reference bytes and all five codec projections. Model codec fixtures bind
budget arithmetic. Matching process tests exercise warning → compaction → follow-up
attempt → finish over FIFO and spool, including independently generated reports.
