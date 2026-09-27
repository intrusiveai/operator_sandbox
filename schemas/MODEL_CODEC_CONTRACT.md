# Model relay and native codecs

Status: typed `engine.model_generate` exchanges, Go/Python validators and 151 shared
cases are implemented for `openai-chat-text-tools-v1` and
[`anthropic-messages-text-tools-v1`](ANTHROPIC_MODEL_CODEC_CONTRACT.md). The Chat
subset is described below. This is offline contract conformance, not provider,
model, credential, route or container qualification. No real model call occurs here.

The native field reference is [OpenAI's Create chat completion API](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create),
consulted 2026-09-23. It defines message roles, function calls, finish reasons and
usage fields. The closed subset and stricter bounds below are Operator choices;
they do not describe the entire provider API.

## Typed relay

The ordinary registry now includes `engine.model_generate`:

- admitted lifecycle only; denied during finalization;
- one model call, requiring durable intent/result recording;
- 120,000 ms operation ceiling, further bounded by the requested timeout and
  remaining campaign time;
- ordinary request/result envelope limits of 4 MiB each;
- common bounded operation errors, including `UNSUPPORTED_CAPABILITY`,
  `DEADLINE_EXCEEDED` and `OUTCOME_UNKNOWN`.

The [request body](engine-model-generate-request.schema.json) contains `codec_id`,
`profile_id`, `profile_digest` and `request`, the supported native request object.
The [result body](engine-model-generate-result.schema.json) echoes the three profile
fields, adds a host `receipt_id`, and contains `response`, the complete supported
native response. Ordinary envelope identities continue to bind campaign, launch,
revision, call and durable operation. This wrapper does not translate message
roles or invent a universal provider result.

The receipt refers to the host's durably retained exchange. Actual provider request
and response bytes, usage, timing, resolved route/model and known/unknown outcome
belong in the journal under existing retention rules. Host-added credentials and
private transport details are excluded from guest results. The validator checks
receipt syntax, not durable receipt existence or attribution.

Request `model` is an assertion of the host-selected model, not authority to select
a route. No endpoint, header, API key, region, project or deployment override is
accepted. The host obtains its endpoint and credentials independently from the
installed provider profile. SDK retries and hidden extra generations remain forbidden.

## Initial codec subset

The native schemas are [request](openai-chat-request.schema.json),
[response](openai-chat-response.schema.json) and
[common definitions](openai-chat-common.schema.json).

Requests contain one frozen leading `system` or `developer` instruction message,
then a user message and native conversation history. Content is text or assistant
null content; client function definitions/calls/results use their native shapes.
Only the leading message may use a privileged instruction role. Tool results
must follow all calls in the assistant batch, once each in their original order,
before another message. No pending tool call may remain when submitting a request.
Call IDs are unique within retained history and a new response cannot reuse them.

Required native request controls are `stream: false`, `store: false`, `n: 1`,
`parallel_tool_calls: true`, positive `max_completion_tokens`, `tools` and
`tool_choice` (`auto`, `none` or `required`). Multiple returned calls are still
dispatched serially by the harness. Compaction uses `tool_choice: none`.
An empty catalog cannot request required tool use. Function `strict` may be omitted
or false; this subset does not depend on provider strict-output support.

The codec caps messages at 4,096, declared functions at 128, and calls in a response
at 1,024. Text/argument/result strings have a 1,048,576-character bound in addition
to the complete encoded message ceiling. Function names are 1–64 ASCII letters,
digits, underscores or hyphens; package-owned projections map local tool names
to native names. These structural bounds do not replace smaller configured loop,
token, context or byte limits. In particular, a structurally valid batch above
the effective loop ceiling is rejected as a whole by loop accounting.

Function parameter schemas are opaque JSON objects in this codec. They are compared
to the package-owned native tool projection using `jcs-v1` identities; they are
never compiled, fetched or used to register handlers from a guest message. Actual
tool argument schema validation remains local to the trusted dispatcher. Native
argument **strings** remain unchanged, even when they contain malformed JSON or
reserved model arguments. Parse them only when the call is selected for dispatch,
using bounded strict decoding and the attempt allocator where applicable.

Responses preserve one choice at index 0, native assistant content/refusal/calls,
finish reason and token usage. Supported nonsemantic metadata includes nullable
`system_fingerprint`, `service_tier`, null `logprobs`, empty `annotations`, and null
legacy `function_call`/`audio`. Other fields or nonempty unsupported semantics fail
explicitly; no continuation/reasoning item is silently discarded to fit this codec.
Hosted tools, custom tools, streaming chunks, additional choices, multimodal data,
provider state and deprecated function-call execution are outside this profile.
Adding a provider feature requires a versioned codec change and fixtures.

## Trusted profile binding

When `EngineContext.model.codec_id` is `openai-chat-text-tools-v1`, its required
`codec_settings` contains `instruction_role`, `max_completion_tokens`, explicit
`response_models` and `tools_digest` (the `jcs-v1` digest of the ordered native
tool-definition array). These secret-free settings come from the host-selected
profile; the harness must not guess them from the opaque profile ID. They add no
endpoint or credential fields. Other codecs require their own versioned settings
contract before being implemented/advertised; this settings object is rejected
under a different codec ID.

`ModelPolicyFromContext(contextBytes, nativeToolsBytes, promptBytes)` /
`model_policy_from_context(...)` constructs identical canonical policy bytes in
both languages. It validates context, requires model generation in its operation
list, verifies the frozen prompt's raw digest/length and UTF-8 bound, and verifies
the installed native tool projection against `tools_digest`. Its caller must first
verify full launch/package/release identities and host authorization. The host
constructs its policy independently; it never accepts policy authority from the guest.

Go exposes `ValidateModelRequest(policyBytes, requestBodyBytes)` and
`ValidateModelExchange(policyBytes, requestBodyBytes, resultBodyBytes)` on Protocol.
Python exposes `validate_model_request` and `validate_model_exchange` on Protocol.

The [policy schema](model-codec-policy.schema.json) is a secret-free **host input to
validation**, not an accepted harness message. It contains the selected codec/profile
pin, exact request model, explicit accepted response model IDs, frozen instruction
role/prompt, output-token ceiling and ordered native tool definitions. The host
constructs it from installed configuration, verified release/package metadata and
the campaign's verified launch context. Guest-provided policy bytes grant no authority.
The profile digest is the independently verified profile pin, not a self-hash of
this helper's projection. The separate full host-private ModelProviderProfile
still owns endpoints, credentials and model-specific capability qualification.

Validation requires exact prompt bytes (also enforcing the 128 KiB UTF-8 prompt
bound), profile pin, request model and `jcs-v1` tool projection identity. Response
model IDs must be explicitly listed: a configured provider alias may resolve to
an allowed concrete model ID, but an arbitrary different model is not accepted.
The response completion-token count cannot exceed the request's bound. No fallback
model, route, alternate provider or prompt rewriting occurs.

Ordinary `ValidateRequest` / `ValidateResponse` also run codec conversation, profile
echo, usage consistency and historical call-ID checks. They do not possess installed
policy; **both ordinary validation and trusted profile binding are required**.

## Outcome interpretation

`ModelDisposition(resultBodyBytes)` / `model_disposition(...)` returns an internal
classification after structural and native consistency checks:

| Disposition | Handling |
|---|---|
| `tool-calls` | Complete `tool_calls` finish, unique IDs and known usage; eligible for subsequent receipt, batch-limit and individual tool validation. |
| `text` | Complete ordinary/empty text outcome; never an implicit campaign conclusion or a tool call parsed from prose. |
| `refusal` | Preserve the refusal and account for the model turn; do not escalate or select another model. |
| `truncated` | Native `length` finish, including partial argument strings; execute none of its calls. |
| `filtered` | Native `content_filter` finish; execute none of its calls. |
| `usage-unknown` | Missing/null native usage; preserve unknown accounting, retain reservations and terminate exploration for host reconciliation. Never substitute zero usage. |

A complete tool-call finish requires at least one call and no refusal; a normal
stop finish cannot contain calls. Total tokens must equal prompt plus completion
tokens. Cached/reasoning detail counts cannot exceed their corresponding totals.
Nonzero audio/prediction usage is outside the selected request subset. Missing
optional details remain missing; they do not change the authoritative top-level
usage. Partial/malformed usage objects fail validation.

Disposition is not dispatch authorization and is not a wire finish-reason enum.
The runtime must validate the complete exchange and durable receipt before using
it, enforce loop budgets before dispatch, and account unknown/fatal outcomes under
the existing terminal rules. The native response remains available for journaling,
including when no calls may execute. Actual provider transport failures use the
ordinary error path, not a fabricated native response.

## Native continuation segments

`ChatContinuation(resultBodyBytes, []ChatToolResult)` / `chat_continuation(result,
tool_results)` creates a complete assistant/tool message segment. Supply exactly
one `{tool_call_id, content}` result per native call, in original order. This includes
correctable invalid-call results and the existing structured
[`TARGET_REVISION_CHANGED` skip result](model-tool-not-executed-result.schema.json).
Keep the original assistant message and all its results together through compaction.

The helper requires a complete tool/text/refusal outcome with known usage and
rejects missing, extra or reordered result IDs. It preserves argument/result strings
exactly and emits identical bounded canonical JSON bytes in Go and Python. Native
response-only empty annotations/null audio/null legacy function fields are omitted
from the request projection; null `tool_calls` becomes an omitted optional field.
Nonempty unsupported versions of those fields were already rejected. It never
parses partial arguments, appends duplicate responses, or fabricates skipped results.

Use this only after bound exchange and receipt validation. The dispatcher supplies
validated local result content, adopts a successful restore binding once, skips
the remaining calls, appends the complete correlated segment and submits the next
request at the new revision when budgets allow. The helper checks correlation,
not the truth of tool result text or transition receipt authority.

## Verification and publication boundary

[model-codec.json](fixtures/model-codec.json) supplies 102 cases shared by Go/Python,
including native outcomes, usage uncertainty, route/profile/tool mutation rejection,
duplicate IDs, orphan/reordered histories and complete continuation bytes. A restore
continuation fixture carries the existing full restore result and structured skip
record. Startup cases verify complete codec settings, prompt/tool pins and identical
policy construction. Fixtures use synthetic model IDs and no provider credentials
or network calls. Three additional ordinary fixtures cover the registered model
exchange and terminal unknown-outcome error path.

Regenerate schemas with `make generate` and fixtures with
`python3 scripts/generate_model_fixtures.py`. Catalog/registry changes also require
regenerating canonical launch identity fixtures with
`python3 scripts/generate_identity_fixtures.py`. `make test` checks generated model
schemas and exercises these validators alongside the existing contract suites.

Before advertising this route, publish its semantic profile and native tool
projection with the pinned package, implement host credentials/provider I/O,
durable admission/usage/receipt handling and harness dispatch, and qualify real
text/tool/follow-up calls, refusals, cancellation and ambiguous failures. Other
provider families/routes require their own native schemas, codec implementation
and qualification; this codec is not evidence of their support. Package `0.1.0`
publication and Operator/Attack Harness runtime qualification remain pending.
