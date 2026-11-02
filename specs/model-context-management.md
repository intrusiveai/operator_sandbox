# Model context management

## Outcome and scope

Operator and Attack Harness MUST support longer campaigns by exposing the prompt
allowance before execution, reporting validated usage after each model response,
reducing initial target context and repeated tool-description/schema overhead,
and keeping reference/evidence reads deliberate and bounded. Harness MUST be able
to compact before crossing a hard allowance,
then continue the same campaign without replaying target work.

The ordinary workflow remains direct input → inspect permitted evidence → one
finish. Operator owns admission, model access, budgets, durable evidence and
cleanup. Harness owns conversation construction, selection and compaction. This
work MUST reuse those components, transports and artifact/observation mechanisms;
it MUST NOT add a context service, another planning agent or a summarizing model
outside the existing campaign model route.

Implementation MUST demonstrate a controlled campaign approaching its allowance,
compacting, continuing, and finishing with inspectable evidence. Validators and
schema changes alone do not establish completion.

## Authoritative sources and implementation locations

Read the implementation with these contracts before editing:

- [Model relay and diagnostics](../docs/MODEL_SERVICE.md),
  [model providers](../docs/MODEL_PROVIDERS.md), and
  [campaign startup](../docs/CAMPAIGN_START.md).
- [Shared contract](../schemas/SHARED_CONTRACT.md),
  [target capability export](../schemas/CAPABILITY_EXPORT_CONTRACT.md),
  [model-tool catalog](../schemas/MODEL_TOOL_CATALOG.md),
  [feedback](../schemas/FEEDBACK_CONTRACT.md), and
  [Harness execution rules](../schemas/HARNESS_EXECUTION_RULES.md).
- Operator: `internal/modelprovider/profile.go`, `public.go`,
  `internal/preparation/launch.go`, `internal/campaignservice/model.go`,
  `internal/reporting/`, `internal/workerjob/`, and `cmd/operatorctl/`.
- Target descriptions and reference delivery: `internal/capabilities/`,
  `internal/preparation/target.go`, and the existing startup reference manifest
  and `reference_read` implementation.
- Shared Go/Python model policy, usage, tool-projection and loop implementations
  under `contracts/`; generators under `scripts/`; schemas/fixtures under
  `schemas/`. Generated files MUST be changed through their generators.
- Harness: `../attack_harness/src/attack_harness/adaptive.py`, `composition.py`,
  `handlers.py`, `observations.py`, and its package/build locks and trusted prompt.

Operator MUST define and publish the shared schemas, semantics and common fixtures
before the Harness implementation consumes them. Changes to closed startup,
generation-result or error schemas require a new exact shared package identity.
Both releases MUST pin matching package bytes through the existing embedded-image
compatibility mechanism. No alternate negotiation or silent old-protocol fallback
is permitted. Product versions and shared package versions remain independent.

## 1. Frozen context allowances

Extend the installed model profile with optional `context_warning_percent`, an
integer from 1 through 99, defaulting to 80. Resolve this default before computing
the profile digest. Existing explicit `maximum_prompt_tokens` and
`maximum_completion_tokens` remain the hard policy settings.

`EngineContext.model.context_limits` MUST carry these resolved fields:

| Field | Meaning |
| --- | --- |
| `maximum_prompt_tokens` | Operator's hard per-request input allowance. |
| `maximum_completion_tokens` | Maximum permitted completion request for the selected route. |
| `warning_threshold_tokens` | `max(1, floor(maximum_prompt_tokens × context_warning_percent / 100))`. |

Use checked integer arithmetic and the shared JSON-safe integer bounds. Duplicated
completion values in native codec settings MUST agree. Host policy validation,
Go/Python startup validators, profile fingerprints and fixtures MUST bind these
values; the Harness/model MUST NOT override them. Healthy target restores MUST
preserve them. Administrators change them for a new campaign, not mid-execution.

These fields describe Operator's allowance, not the provider model's actual
context-window capacity. The products MUST NOT infer that capacity from the model
name or from the sum of the two allowances. Unknown provider capacity MUST remain
unknown; no online model catalog or new startup lookup is required. The prompt
allowance already applies to input: completion tokens MUST NOT be subtracted from
it a second time. Existing total reservation remains prompt ceiling plus requested
completion allowance.

## 2. Usage and early warnings after model responses

Every successful `engine.model_generate` result MUST retain its existing native
`response` and add a host-owned `context_budget` object with these fields:

| Field | Meaning |
| --- | --- |
| `input_tokens`, `output_tokens`, `total_tokens` | Validated normalized counts from the existing codec accounting. |
| `requested_completion_tokens` | Actual output allowance in this request, which can be below the profile maximum. |
| `reserved_tokens` | Actual reservation made for this call. |
| `remaining_prompt_tokens` | Frozen prompt allowance minus this response's validated input tokens. |
| `near_prompt_limit` | Input tokens are greater than or equal to the frozen warning threshold. |
| `remaining_model_tokens`, `remaining_model_turns` | Campaign-wide remaining budgets after settling this call. |

The response receipt and operation identity bind this object to the completed
request. Duplicate delivery MUST return the original committed result and budget
snapshot, not recalculate it from later campaign state. Go and Python MUST validate
consistency with native usage and the frozen policy. Preserve existing cache and
thinking-token accounting; cached input is not free context capacity.

Validation MUST first establish the native exchange and derive its normalized
metrics, then construct and validate the host budget snapshot. Do not create a
circular requirement that budget fields already exist before deriving usage, or
accept guest-supplied accounting to satisfy the result schema.

For example, an input allowance of 32,768 and the default 80% threshold resolves
to 26,214 warning tokens. A successful call reporting 32,668 input tokens has
`remaining_prompt_tokens: 100` and `near_prompt_limit: true`. It requires early
context reduction, even if the provider supports a larger context window.

`remaining_prompt_tokens` describes that completed request. It MUST NOT be labeled
as an exact prediction of space available in the next request: new observations,
tool calls/results and provider framing can change input size. Unknown or malformed
usage MUST retain existing failure behavior, not become zeros or an accepted
result. Hard overflows MUST retain `usage_exceeds_reservation`, numeric diagnostics
and terminal behavior. A warning below the hard allowance does not reject a valid
response or discard executable model tool calls.

Harness MUST use `near_prompt_limit` to schedule compaction before its next model
generation, after completing the current admitted tool batch. Stop/restore batch
boundaries retain precedence. It MUST retain the warning in runtime state and
MUST NOT rely on the model noticing a prose instruction. New context added after
a response MUST also be considered; the previous usage is not a safe upper bound.

Operator MUST journal the normalized budget snapshot with the model exchange
before returning it and explicitly project it through export/report allowlists.
Keep reservations, validated consumption, and campaign-wide remaining budgets
distinct. Existing post-dispatch failure diagnostics MUST remain primary when
later Harness shutdown or cleanup fails.

## 3. Compact target context and tool definitions

### 3.1 Target index with details available on demand

Operator MUST retain the complete verified public capability description for host
validation and Harness reference access. Harness MUST initially present a compact
index to the model instead of automatically expanding all service, database and
file details. This is a presentation change, not deletion of capabilities or a
change to the native Interceptor capability contract.

The initial context MUST identify permitted target operations and enough of their
input contract to perform an ordinary direct invocation without a documentation
read. It MUST also identify the effective feedback policy and provide a compact
index of available injection surfaces, services, database table policies and file
namespaces. Index entries MUST preserve exact logical identifiers, short roles or
purposes where supplied, applicable action/resource relationships, and verified
reference handles for details. Do not manufacture descriptions missing from the
source. The index MUST distinguish capability availability from authorization.

Detailed service endpoint/input/output schemas, MCP contracts, database key columns
and writable fields, creation rules, file limits, extensive examples and advanced
injection guidance MUST remain retrievable through existing `reference_read`
mechanisms. Reuse verified startup references and immutable content; implementation
MUST ensure the referenced material is actually readable, not merely advertise a
handle. Use resource-level references or an index into existing bounded references
so requesting one resource does not require loading the entire manifest. Any new
reference metadata MUST use the shared contract and existing integrity checks;
do not add a discovery service, tool or parallel capability store.

The reference index MUST make clear which details were deferred and where to read
them. Harness guidance MUST direct the model to retrieve the relevant resource
contract before constructing an advanced action whose selectors or payload depend
on it. For example, replacing an MCP result requires its service/tool selector and
result structure; database mutation requires its table policy, keys and writable
fields; file mutation requires its namespace and applicable restrictions. Generic
injection-family descriptions alone MUST NOT be treated as sufficient resource
contracts. Explicitly supplied scenario details can avoid a redundant read when
sufficient, but MUST NOT override the verified resource contract.

Repeated service-level injection descriptions SHOULD be consolidated into shared
surface descriptions plus resource associations. Preserve every unique selector,
constraint and supported action. Scenario selection MUST NOT hide resources needed
for permitted exploratory work. Retrieved details MUST remain untrusted target
data, obey the existing read/visibility limits, and keep source identity when
compacted. Missing or partial details MUST be explicit; neither product may guess
resource identifiers or silently relax validation.

### 3.2 Concise descriptions with optional advanced guidance

Each tool description MUST retain its purpose, when to use it, essential argument
relationships, the source and meaning of valid handles, and consequential effects.
In particular, restore changes target state and finish acceptance closes admission
without establishing completed cleanup. Descriptions and schemas together MUST
make the ordinary execute → inspect evidence → finish workflow self-contained;
do not require a documentation read before every basic invocation.

Move extensive examples, uncommon combinations and detailed injection instructions
to verified reference material accessible through the same mechanism as target
details. Compact descriptions MUST point to discoverable guidance when needed.
Do not repeat long explanations in the trusted prompt, each tool and target
descriptions. Keep the short product-owned rules about explicit origin, direct
input, exact returned handles, evidence inspection and honest assessment.

For example, `attempt_execute` can explain that it invokes a permitted operation,
accepts direct input or a committed artifact receipt, requires a scenario ID for
scenario origin and forbids one for exploratory origin, and returns an execution
receipt only for dispatched attempts. Its declaration MUST retain the corresponding
fields and relationships; prose alone is not a replacement for validation.

### 3.3 Smaller, capability-appropriate schemas

Measure generated declarations for every supported codec before changing them.
The full Bedrock projection inspected for this design was 41,415 JSON bytes,
including 20,479 for `attempt_execute`, 8,945 for `finish`, and 6,936 for
`record_append`. These are byte measurements, not token counts or provider limits.

Implement the following in the existing shared generators and Go/Python projection
functions, rather than maintaining independent Harness schemas:

1. Compact repeated arbitrary-JSON expansions and repeated assessment/record
   substructures where a concise provider declaration can describe the accepted
   form. Preserve clear field types, required fields, explicit origin, direct
   versus artifact input choices, reference categories and correction guidance.
   Start with injection payload and selector expansions. Keep explicit typed
   action/surface, destination selector, placement and payload fields using the
   existing names and accepted forms. Target-dependent content MAY use a bounded
   JSON value in the provider projection instead of recursively enumerating every
   possible structure. Fixed selector fields MUST remain explicit; do not replace
   a whole action with an undocumented arbitrary object. Where provider schemas
   cannot express the JSON union or limits, describe them concisely and enforce
   them in the shared runtime validator. Preserve byte, nesting and target limits.
2. Keep every omitted declarative constraint enforced by the authoritative runtime
   validator before effects. Compact descriptions MUST name applicable bounds or
   cross-field rules; they MUST NOT imply arbitrary values are accepted. Unknown
   fields, duplicate keys and invalid references remain errors.
   Both Harness validation and Operator admission MUST use the authoritative
   contract, with Operator enforcing selected-resource rules before effects.
   Rejections MUST retain the existing actionable field/rule/correction and
   no-dispatch/safe-resubmission diagnostics; no new error envelope is required.
3. Derive a minimal frozen projection from admitted operations, target capabilities
   and installed route permissions. Include the available injection surfaces and
   applicable setup alternatives; omit unavailable ones. Do not duplicate whole
   capability manifests or enumerate large collections of resource IDs into tools.
   A target without database support MUST omit database-specific branches. Keep
   operation-specific payload schemas in target references, not copied into every
   execution tool. The tool MUST still specify how to select the operation and
   encode direct input or supply an artifact receipt unambiguously.
4. Preserve valid exploratory, artifact, history, injection cleanup and snapshot
   workflows. Scenario wording alone MUST NOT remove a capability. Removing setup
   alternatives MUST NOT remove cleanup needed for already retained injections.
5. Publish the minimal projection inputs in the startup model context and bind the
   resulting tool digest to the frozen model policy. Harness MUST derive identical
   tools from those verified inputs. Go and Python MUST use the same deterministic
   derivation and fixtures. Tools MUST NOT change per turn or after target restore.
   Reading resource details MUST NOT change the catalog. Freeze only the tool
   projection; this does not freeze conversation content or current target state.

Operator MUST continue authorizing actual operations against complete host policy;
provider declarations are guidance, not authorization. Preserve supported provider
schema restrictions, including OpenAI object roots without forbidden combinators.
Do not enable strict mode as a substitute for runtime validation.

Tests MUST establish reduced initial target-context bytes and serialized tool bytes
for representative full-capability and restricted targets, plus byte-identical
Go/Python projections. Record target index versus full-description size, per-tool
description and schema sizes, and total before/after measurements for each codec.
Include the cost and number of reference reads in representative workflows;
deferring a large description only to reread it in full every turn is not a useful
reduction. Keep byte measurements distinct from measured or estimated tokens.
Maintain explicit fixture size budgets after reviewing the resulting projections.
A reduction that breaks legitimate calls, weakens runtime enforcement or simply
shifts essential instructions into repeated validation failures is not acceptable.

## 4. Bounded evidence entering model context

Use the existing `observation_read` and `reference_read` tools. Their model-facing
`max_bytes` argument SHOULD become optional with a shared default of 4,096 bytes;
Harness MUST resolve omission to that exact value before issuing an explicit host
read. Supplied invalid values MUST remain rejected, not clamped or repaired.
Existing hard per-read limits, cumulative read budgets and range rules remain.

Tools and trusted guidance MUST explain reading a small first chunk, inspecting
its availability/size/end-of-file metadata, and retrieving additional ranges when
needed. Requests for larger permitted chunks remain supported. Defaults MUST NOT
hide missing data or imply a partial read is the complete observation.

Complete evidence MUST remain retained and addressable through original receipts.
Do not introduce a parallel preview store or a host-side model summarizer. If a
text excerpt is presented to the model, the Harness MUST decode it faithfully,
retain source receipt/range identity and mark incomplete decoding or truncation.
Base64 transport content MUST NOT be treated as a token-efficient model encoding;
wire encoding and model presentation are separate concerns owned by the existing
implementations. Binary content requires an explicit bounded representation.

Preserve visibility, campaign ownership, integrity, byte-range validation and
immutable historical receipts. Content remains untrusted data. Reducing context
MUST NOT invent an observation or change an oracle verdict, including the distinction
between positive, empty, partial and unavailable evidence.

## 5. Compaction and finalization headroom

Harness MUST replace the byte-only compaction trigger with a policy using the
published token allowance, last validated usage, soft warning and newly added
material. Existing byte/frame bounds remain independent hard constraints. Token
estimates MUST be marked as estimates and SHOULD be conservative; a constant
bytes-per-token ratio MUST NOT be described as exact across models or content.

The same model route, prompt allowance and campaign budgets apply to compaction.
Harness MUST compact early enough to include its summarization instructions and
expected output. It MUST NOT wrap an already oversized full history in another
request and assume that request fits. Bounded local selection MUST preserve
required authority, active task, receipt identities, unresolved work and coherent
tool-call/result pairs. Omitted details remain accessible through retained handles;
summaries are interpretation, not replacement source evidence.

Before attempting a model-based compaction, Harness MUST check that published
remaining campaign tokens/turns can fund its normal reservation. It MUST NOT raise
limits or exempt compaction from accounting. If sufficient reduction or a model
call is impossible, it MUST use the existing honest finish/unavailable path and
retain the specific cause. Deadline enforcement and owned cleanup MUST not depend
on obtaining a model-authored assessment. Existing finish acceptance versus completed
cleanup semantics remain unchanged.

## 6. Pre-dispatch context checks and safe correction

Implement this as a second increment after the baseline allowance/usage and schema
work. Use the existing model admission path, not a new endpoint or service.

### Counter qualification

Before enabling a rejecting preflight for a route, implementation MUST select and
document a reliable model/codec-specific counting method. It MUST account for the
actual instructions, tools, messages, tool results and relevant provider framing.
Bind its implementation/version and supported model identity to installed host
configuration. A local tokenizer that counts only message strings is insufficient.

Counter results MUST distinguish `exact`, `estimated`, and `unavailable`. Only a
qualified exact result may reject a request as exceeding the token allowance.
Estimates may guide earlier Harness compaction; they MUST NOT invent provider
usage or establish a hard overflow. Unsupported routes or failed counting remain
explicitly unavailable and keep the existing dispatch and post-response validation
path. The baseline improvement MUST work on all routes without a counting service.

When preflight is implemented, add a closed `context_budget.preflight` object to
successful results: `accuracy`, a bounded host-defined `method_id` when a method
was selected, and `input_tokens` only for exact/estimated counts. Unavailable
counts MUST omit `input_tokens`. These are pre-dispatch measurements, distinct
from validated provider usage; bind them to the same request and retain them in
the existing model event projection. Never substitute estimates for usage when
settling the campaign budget.

Reuse an existing qualified local counter if available. Selecting remote counting,
new dependencies or additional credentials requires explicit review before that
integration is implemented. Do not call a generative model to count tokens. Local
fixture tests do not qualify provider-specific framing accuracy; record the
supported counter/model combinations and evidence separately from implementation.

### Rejection and resubmission

An exact input count greater than `maximum_prompt_tokens` MUST be rejected before
provider dispatch, credential resolution for generation, model-turn charging or
reservation. Equality MUST be admitted subject to the other existing checks.
Validate the request and frozen policy before counting. Cancellation/deadline and
independent errors retain their existing behavior.

Use the existing operation-error envelope with `code: INVALID_ARGUMENTS`,
`effect_state: none`, `disposition: correct-and-resubmit`, and `/request` as the
field path. Its bounded diagnostic MUST identify source `operator`, stage
`model-admission`, reason `prompt_allowance_exceeded_before_dispatch`, and explain
that no model request was dispatched and context reduction is safe. Extend the
existing shared diagnostic schema with a closed numeric detail object where needed
to carry the counted input, method/accuracy and allowance; do not encode counts
only in prose or introduce a parallel error envelope.

Journal the rejection and retain normal operation idempotency. A duplicate request
ID MUST return the same rejection. Corrected content requires a new operation ID;
it MUST NOT reuse or replay an accepted/uncertain provider operation. Counter work
MUST be bounded by the request/campaign deadline and MUST NOT contact the target.

Harness MUST explicitly handle this pre-dispatch rejection rather than treating
every model-operation error as terminal. It MUST reduce context before submitting
a corrected request and MUST NOT automatically resend unchanged bytes. Extend the
shared loop accounting so a non-dispatched generation does not count as a completed
model turn, while each failed correction consumes the existing no-progress bound.
No-progress limits MUST NOT reset merely because a request was rejected. Define
this transition in the shared Go/Python fixtures; do not create an unbounded retry
loop or count this as an invalid model-authored tool call.

This recoverable path MUST NOT apply to post-dispatch overflows, HTTP rejections,
timeouts, unknown outcomes or invalid responses. Their existing terminal behavior,
reservation retention and numeric diagnostics remain authoritative.

## 7. Explain context consumption without exposing content

Extend existing model journal events and report/export projections with bounded
request-size facts: total native request JSON bytes and instruction, tool-definition,
conversation/tool-result, and other/framing byte measurements. Label their basis
as the validated native request representation before transport adaptation.

Define codec-specific partition rules in shared fixtures. Instructions embedded
in message arrays MUST not also count as ordinary conversation. If independent
subvalue serialization adds framing, document that accounting explicitly instead
of claiming component sizes sum to the transmitted body. Do not imply JSON bytes
equal provider token counts or that component token counts are known.

Include validated input/output/total tokens when available, the effective allowance,
warning threshold, counting accuracy when used, and the operation/receipt identity.
Reports SHOULD identify large fixed tool overhead, requests approaching the prompt
allowance, pre-dispatch corrections, and terminal usage rejections separately.
Comparison of successive calls MUST remain attributed to their own request IDs.

Measurements MUST contain no prompt excerpts, SQL, observation content, credentials,
provider bodies or unrestricted errors. Use explicit metadata allowlists, bounded
integers and fixed category names. Existing protected request retention remains
separate from these diagnostics. Unknown data MUST remain unknown, and older
journals without these fields MUST remain readable.

## 8. Implementation and commit boundaries

Use these boundaries; each MUST include relevant tests and current behavior docs:

1. **Shared context contract and usage feedback.** Define resolved configuration,
   startup/result schemas, validation, warning arithmetic and common fixtures.
   Implement Operator publication and usage retention; publish a reviewed exact
   package for the matching Harness implementation. Update existing authoritative
   contract documents so old assumptions do not conflict with these semantics.
2. **Compact target context, tools and bounded read defaults.** Implement the
   resource index and verified detail references, deterministic capability-filtered
   tool projection, concise descriptions, smaller schemas, exact runtime validation
   and shared read defaults. Update all provider codecs, build digests and shared
   fixtures together. Keep complete host capability validation intact.
3. **Harness integration and observability.** Consume the exact package, schedule
   early compaction, keep headroom/receipt semantics, and add the journal/export/
   report size facts. Demonstrate the baseline complete workflow before treating
   it as delivered. Coordinate Harness work through the shared package, not copies
   of this document interpreted independently.
4. **Qualified preflight and correction.** Resolve counter qualification first;
   implement bounded pre-dispatch checks and the shared correction transition.
   Verify no-dispatch/no-charge behavior and finite failed correction. Do not claim
   exact-count protection on routes for which a counter has not been qualified.

The first three boundaries form the first delivery increment. The fourth MUST NOT
be a prerequisite for benefiting from published allowances, warnings and smaller
tools. Product documentation MUST explain actual behavior and configuration; it
MUST NOT describe unqualified counter support as available.

## 9. Verification and delivery

Add shared Go/Python fixtures and focused integration tests covering:

- Default and explicit warning thresholds, checked arithmetic, invalid config,
  completion-setting consistency and rejection of incompatible package identities.
- Usage below/at the soft threshold and exactly at the hard allowance; normalized
  cache/thinking usage; unknown/malformed usage; prompt and total overflow
  diagnostics. Keep the earlier output-limit validation intact.
- Correct remaining campaign budgets after settlement, duplicate results returning
  their original budget snapshot, and unchanged counters/limits across restore.
- All five codec projections: reduced size, identical Go/Python output, provider
  declaration validity, legitimate advanced/exploratory calls and runtime rejection
  of constraints compacted out of declarations. No capability granted by prose.
- Compact target indexes preserve exact resource identifiers, associations and
  readable detail handles. Exercise MCP/service-result injection, database-state
  mutation and file mutation after bounded detail reads. Verify unchanged selector,
  writable-field, creation, size and authorization enforcement. Missing, truncated
  or unavailable references cannot become guessed contracts or dispatched actions.
- Unavailable capability branches are absent; permitted exploratory, retained
  injection cleanup, snapshot/restore and optional history remain usable. Tool
  declarations and their digest remain unchanged after detail reads and restores.
- The basic direct-input/evidence/finish workflow needs neither advanced guidance
  reads nor artifact publication for small inputs. A controlled invalid advanced
  call receives a precise correction and succeeds on corrected submission without
  duplicate execution. Runtime validation rejects invalid values even when their
  full constraints were intentionally compacted out of the provider declaration.
- Small default reads, explicit larger reads, truncation/availability indicators,
  exact receipt/range references, visibility denial and unchanged retained bytes.
- A large fixed prompt/tool set and rapidly growing observations: Harness compacts
  before its next ordinary model call, preserves tool-call/result pairs and valid
  receipts, then continues. Compaction itself stays within the same allowances.
- Insufficient budget for compaction, ineffective reduction and deadline expiry:
  bounded stopping, honest finish/unavailable reason and owned cleanup.
- Exact preflight rejection with no provider invocation, credential lookup for
  generation, reservation or model-turn charge; corrected input succeeds once;
  duplicate IDs do not dispatch; estimates/unavailable counters are not exact.
- Input/measurement failures and repeated unsuccessful corrections cannot bypass
  no-progress limits or reopen an uncertain/closed campaign.
- Journal → independent inspection/report/export preserves numeric facts and
  accuracy labels. Synthetic secrets and rejected response bodies remain absent
  from diagnostics; later forced termination does not replace the original cause.

The representative workflow MUST use matching Operator and production Harness
processes, their real transport, controlled model responses and a local target.
It MUST execute an explicit scenario, inspect evidence, approach the allowance,
compact or correct before dispatch, execute a follow-up without replay, accept
one finish, observe Harness exit 0, complete owned cleanup and generate exported
evidence plus a readable report. Include a failure variant and both supported
transport paths. Add an advanced variant that discovers a resource in the compact
index, reads only its relevant details, performs a permitted injection, inspects
the resulting evidence and cleans up. Test all resource families above with local
fixtures and identify which have complete process coverage. Measure total model
request bytes and reference-read counts for basic and advanced workflows, alongside
the initial-context and fixed-tool reductions. Separately demonstrate preflight using an instrumented counter
and provider; label synthetic counting separately from a qualified real counter.

Report exact component/contract identities, before/after target-context, description
and schema sizes, reference-read costs, relevant usage measurements, observed
dispatch counts, preserved evidence and any unqualified
counter/provider combinations. Controlled tests establish behavior, not live
provider reliability. Live model/counting calls, cloud writes and release
publication require separate authorization.
