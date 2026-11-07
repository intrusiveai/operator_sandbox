# Shared model-tool restructuring

## Outcome and scope

Operator and Attack Harness MUST support a model-facing workflow of direct input
→ inspect permitted evidence → one finish without requiring the model to assemble
compound resource identities or repeat derived execution receipt lists. This
specification defines Operator's responsibilities for the coordinated change in
the [Harness companion specification](../../attack_harness/specs/model-tool-restructuring.md)
(`attack_harness`, branch `feat/model-tool-restructuring`). That sibling-workspace
link identifies the paired design, not an installed runtime dependency.

The shared package produced by Operator MUST be the authoritative implementation
contract for both products. These two specifications MUST be reconciled before
implementation if either changes. Harness MUST NOT introduce an independent
model schema, compatibility mechanism, or interpretation of assessment semantics.

The existing 11 tool names, native provider transports, execution services,
journal, artifact storage, admission, budgets, deadlines, and cleanup MUST remain.
Do not introduce another planning service, model, handle-resolution service,
runtime schema negotiation, hypothesis-registration prerequisite, or mandatory
assessment upload/record/stop sequence. JSON remains the provider calling format;
the change reduces its bookkeeping and nesting rather than eliminating it.

## Baseline and responsibility boundary

This specification starts from Operator `candidate-v0.1.4` at
`8b7147a66353dec58814bfd8392e716f9d0cd135`. Its locked shared package is `0.5.0`,
digest `sha256:9db8d03e9d759eba0c002444c9c96dbbe77991d47f97e2e9fd1b4b182bec74c3`,
built from Operator source `485042787cc75e1776acaac11023ba9d210cc1f3`. This is the
comparison baseline, not the identity to use for the revised interface.

| Owner | Required responsibility |
| --- | --- |
| Operator shared package | Define argument and result semantics, provider projections, deterministic normalization, validation, diagnostic paths, and common fixtures. Provide matching Go/Python behavior. |
| Harness | Keep bounded typed handles in existing runtime state; resolve them against verified resources; use shared normalization; preserve call identity, scenario delivery, evidence inspection, and model feedback. |
| Operator host | Independently authorize and validate expanded requests against retained state; retain input before dispatch; execute; retain assessment; close admission; clean up; preserve diagnostic and export facts. |

Model arguments and ordinary host-operation messages MUST be treated as different
representations. The host SHOULD keep its existing canonical identities and wire
shapes where translation suffices. It MUST NOT accept an opaque Harness handle as
authorization or maintain a second handle registry. Unchanged host messages do
not imply compatibility with an old model catalog.

In particular, Operator's claim-level observation/receipt consistency check MUST
remain. Shared normalization MUST populate each claim's receipt list from the
observations selected by the model; it MUST NOT weaken validation by allowing an
unlinked observation. Model-authored outcomes, confidence, assurance, and claims
MUST NOT be inferred from successful tool execution.

## 1. Authoritative model interface

Operator MUST update `scripts/generate_tool_catalog.py` and regenerate the model
schema/projections rather than hand-edit generated declarations. The generator
currently derives model inputs largely from host schemas; it MUST explicitly
construct the revised presentation where the shapes differ, while reusing shared
semantic field definitions. It MUST NOT change host schemas merely to make that
generation easier.

The following table fixes the model interface agreed with Harness. `?` denotes an
optional field. Unlisted arguments MUST be rejected. Content, assessment, and
record rules below are part of the interface, not merely descriptive examples.

| Tool | Revised arguments | Required translation/result behavior |
| --- | --- | --- |
| `attempt_execute` | `origin`, `operation_id`, one of `json`/`text`/`base64`/`artifact`; `scenario_id` for scenario origin; `media_type?`, `parent?`, `caller_principal_id?`, `rationale?`, `observations?`, `setup?`, `injection?`, `retain_injections?`, `strategy_provenance_ref?`, `technique_refs?` | Reconstruct input/setup envelopes, resolve parent/artifact handles, generate setup action IDs. Return execution, observation, and applicable injection handles with authoritative statuses. |
| `observation_read` | `observation`, `offset?`, `max_bytes?` | Resolve receipt/entry/range; return verified content, observation/range handle, availability, truncation, and continuation metadata. |
| `finish` | `finish_reason` and exactly one of inline assessment fields, `artifact`, or `unavailable_reason` | Normalize one assessment/alternative into the existing finish message. Preserve accepted/pending finalization and timely exit semantics. |
| `artifact_publish` | `purpose`, `media_type`, one of `json`/`text`/`base64` | Keep chunking, serialization, hashing, descriptors, and commit protocol in product code. Return committed artifact handle and bounded metadata. |
| `reference_read` | `reference`, `offset?`, `max_bytes?` | Resolve a verified task/resource/scenario/tool-guide reference; preserve complete active-scenario loading and its execution gate. |
| `injection_delete` | `injection` | Resolve the action/execution pair; report actual removal/effect state, not reversal of committed mutations. |
| `record_append` | `kind` and the flattened kind-specific fields below | Reconstruct the optional historical record; return record and applicable hypothesis handles. |
| `snapshot_request` | `label?`, `description?` | Keep current arguments and quota checks; return checkpoint handle when an identity exists, with status distinct from readiness. |
| `snapshot_list` | `session?`, `offset?`, `limit?` | Resolve an authorized session handle; keep current default scope and pagination; return checkpoint/session handles and continuation metadata. |
| `snapshot_inspect` | `snapshot` | Resolve source session and checkpoint identity; inspect under existing ownership/availability checks. |
| `restore_request` | `snapshot` | Resolve the same pair; preserve restore binding changes, remaining-call skipping, and non-replay rules. |

Read offsets default to zero. Omitted read/page sizes use the existing bounded
defaults within the admitted policy. `{}` remains valid for snapshot request/list.
Do not invent new limits, unbounded reads, or a larger default search scope.

### Content and setup

Exactly one content field MUST be present, determined by presence, not truthiness:
`json` is a native JSON value, `text` a UTF-8 string, `base64` canonical padded
base64, and `artifact` a committed artifact handle where allowed. JSON null is a
supplied value, subject to the target contract. Native objects MUST NOT be encoded
as JSON strings to avoid schema work; legitimate string-valued JSON stays valid.

JSON uses `application/json`; any explicit JSON media type MUST equal that value.
Text defaults to `text/plain`; binary requires `media_type`; artifact handles
forbid overriding media type. Artifact publication requires explicit `media_type`
for every choice and cannot publish another artifact handle as content. Existing
artifact purpose, canonicalization, ownership, configured size, and depth checks
MUST remain. Omitted values MUST NOT be guessed from target prose.

Origin remains mandatory. `scenario` requires the exact submitted `scenario_id`;
`exploratory` forbids it. A sole scenario, parent receipt, or final objective
association MUST NOT supply a default origin. `parent` resolves to a verified
execution receipt; host-owned thread/index/generation bookkeeping stays internal.

Omitted `observations` means all permitted feedback. Supplied `observations` is a
nonempty array of admitted kinds; normalize it to the existing selected-feedback
form. Invalid/empty supplied values MUST NOT be treated as omission.

`setup` is a bounded array of items with `surface`, `mode`, `scope`, `selector`,
and `placement` directly, removing model-authored action IDs and the fixed action
type wrapper. Product code MUST supply stable IDs and the fixed type. Preserve
all five admitted injection surfaces and their restrictions. Selectors and
placements remain real structured decisions with exact pointer, predicate, index,
merge-field, policy, and simulation/fidelity validation.

Database environment-state row `key` input MUST be a native JSON object. Shared
normalization MUST validate the exact permitted non-null scalar key columns and
serialize the object into the existing host representation. This MUST NOT require
an Interceptor protocol change or relax native selector validation.

`injection` is one content-choice object with applicable media type. Nonempty
setup requires it; injection content and `retain_injections` require nonempty
setup. Omitted setup means none; omitted retention means false. The host's existing
canonical JSON injection-content requirement still applies. A malformed carrier
choice or incompatible content MUST fail before target dispatch.

### Inline finish

`finish_reason`, `summary`, `objectives`, and optional assessment collections are
root fields: there is no model-facing `assessment` wrapper. `summary` and
`objectives` are required for the inline path. Optional fields are `status`,
`hypotheses`, `claims`, `coverage`, `uncertainties`, and `records`. Status defaults
to `partial`; optional collections default to empty. Preserve existing outcome,
status, confidence, interpretation, finish-reason, and uncertainty-kind values.

| Collection | Required fields | Optional fields |
| --- | --- | --- |
| `objectives` | `objective_id`, `outcome`, `summary` | `observations`, `attempts`, `records` |
| `claims` | `statement`, `interpretation`, `assurance`, `confidence` | `observations`, `attempts`, `uncertainties` |
| `hypotheses` | `origin`, `outcome`, `summary`; `scenario_id` for scenario origin | `label`, `parent`, `objective_refs`, `observations`, `attempts`, `records` |
| `coverage` | `objective_id`, `status`, `reason` | `hypothesis`, `alternative`, `observations`, `attempts` |
| `uncertainties` | `kind`, `description` | `label`, `observations`, `inputs`, `records` |

Resource-reference collections contain typed handles. `inputs` selects verified
input/reference entries or ranges, not arbitrary artifacts. Product code assigns
claim, gap, and new hypothesis IDs. An optional local `label` is required only
when another assessment item refers to that hypothesis or uncertainty. Resolve
all local declarations before validating references, regardless of order.
Hypothesis `parent` and coverage `hypothesis` select local labels or known
hypothesis handles. Reject ambiguity, duplicates, unknown references, self-parenting,
and cycles. Do not require prior hypothesis records.

For each item, expand selected observation handles into authoritative references
and derive a stable deduplicated union of their execution receipts plus any
explicitly selected `attempts`. For host items without observation-reference
fields, validate inspection and retain the model's selection through existing
local diagnostic evidence, then populate the existing receipt field. Do not
manufacture additional claims to carry these selections. Record handles expand
only into accepted historical-record receipts.

The alternative `artifact` path resolves a committed conclusion artifact meeting
the existing authoritative assessment and binding rules. It requires no separate
conclusion-registration call. The alternative `unavailable_reason` maps to
`unavailable.reason` and retains the existing allowed reasons. Neither alternative
may be combined with inline fields or the other alternative. An unavailable finish
MUST NOT fabricate an assessment or imply that one was committed.

### Optional historical records

| `kind` | Required fields | Optional fields |
| --- | --- | --- |
| `hypothesis` | `origin`, `statement`; `scenario_id` for scenario origin | `parent`, `objective_refs`, `assumptions`, `predicted_observations`, `surface_ref`, `records` |
| `progress` | `summary` | `decision`, `objective_refs`, `hypotheses`, `attempts`, `observations`, `records`, `interpretation`, `confidence`, `assurance`, `assessment_summary`, `gaps` |
| `lineage` | `hypothesis`, `attempt`, `mutation_summary` | `parent`, `records` |
| `coverage` | `entries` | `records` |

Hypothesis records get generated IDs and return hypothesis handles. A hypothesis
parent is a hypothesis handle; a lineage parent is an execution handle. Coverage
entries use the inline coverage shape. Reference/list omissions become empty
collections where allowed by the paired spec. Preserve the existing bounded types
for assumptions, predicted observations, assurance, and gaps.

Progress `interpretation`, `confidence`, `assurance`, and `assessment_summary`
must all be present or all absent. With them, omitted `gaps` becomes empty; without
them, `gaps` is invalid. Normalize the supplied fields into the existing optional
progress assessment. Derive observation receipts as for finish. No record tool
becomes a required assessment-building step.

## 2. Shared normalization and trusted handle resolution

Go and Python MUST expose the same normalization behavior through existing shared
contract facilities, with common fixtures for expanded output and error paths.
Use small deterministic helpers as necessary; do not build a new workflow engine.
Separate structural validation from handle resolution and host validation:

1. Decode/validate the revised tool arguments, preserving duplicate-key failures
   when raw provider JSON is available. Apply only documented omission defaults.
2. Harness resolves typed references from verified runtime state. Shared routines
   receive explicit trusted resolution data and stable generated identities, not
   a model-supplied dictionary claiming which receipts a handle represents.
3. Normalize content, setup, assessment/record fields, and reference lists into
   existing host messages; retain a bounded model-path-to-host-path mapping.
4. Validate expanded messages and effective size limits before sending them.
5. Operator independently validates authoritative membership, purpose, visibility,
   integrity, lineage, availability, and budgets. Only then may execution proceed.

The authoritative implementation API is documented in
[`MODEL_TOOL_NORMALIZATION.md`](../schemas/MODEL_TOOL_NORMALIZATION.md). Reads through
range handles MUST use offsets within the original entry and remain inside the
range. Observation selections without canonical item fields MUST be retained in
bounded batches on the existing diagnostic-report operation before submission.

The shared contract MUST define resource kinds, stable mapping semantics, result
status meanings, and required inspection checks. Harness owns the bounded
in-process map. It MUST survive compaction without placing the complete map in
the model context. Handles MUST originate only from verified inputs/results,
never model text or JSON embedded in observations. Rejected submissions produce
no execution handle. A handle for an uncertain execution retains that uncertainty.

Observation handles MUST bind receipt, entry, and any selected byte range. A
listed entry is not yet inspected. Partial reads return exact verified-range
handles; positive claims cannot cite unseen bytes through a whole-entry handle.
Unavailable/unread references remain usable for describing gaps. Preserve
historical evidence across restore and revalidate live resources using existing
rules. Do not revive stale handles or replay uncertain effects to rebuild state.

Generated action/claim/gap/hypothesis IDs and expansion MUST be stable for repeated
delivery of the same native call. Changed arguments under the same call identity
MUST trigger conflict handling, not silent reuse. Duplicate finish MUST retain
existing idempotency and interrupted-finalization behavior. No new general resume
or replay mechanism is part of this change.

### Reference expansion fixture

Given a verified, inspected mapping from `obs-1` to execution `receipt-123`, entry
`entry-456`, the model supplies an optional claim such as:

```json
{
  "statement": "The target returned the requested marker.",
  "interpretation": "supported",
  "assurance": "Observed in the complete response.",
  "confidence": "high",
  "observations": ["obs-1"]
}
```

The shared normalization fixture MUST expect both host reference fields:

```json
{
  "attempt_receipt_refs": ["receipt-123"],
  "observation_refs": [{
    "attempt_receipt_id": "receipt-123",
    "entry_id": "entry-456"
  }]
}
```

This fragment omits the unchanged analytical fields and generated claim ID for
illustration. Tests MUST compare the entire normalized claim. The same fixture
MUST reject an unregistered, wrong-kind, unavailable, or uninspected handle where
it is used to claim observed content; no prefix-based authorization is sufficient.

## 3. Host integrity, diagnostics, and reporting

Existing retention-before-dispatch, artifact/target compatibility, capability,
budget, deadline, ownership, range, and cleanup rules MUST remain. The simplified
presentation MUST NOT turn a removed caller obligation into an unchecked host
assumption. Preserve the distinction between execution success, observed effect,
objective achievement, assessment acceptance, and completed cleanup.

Shared errors MUST expose stable reason, bounded relevant field path, and an
actionable correction. Harness MUST map expanded host paths back to model fields
without replacing the host code/cause. For example a claim receipt/reference
error MUST point to `/claims/0/observations/0`, not instruct the model to populate
an `attempt_receipt_refs` field absent from its interface. Preserve origin errors,
duplicate keys, invalid media/content choices, bad selectors, and wrong handle
categories rather than collapsing them into generic structure failures.

Each model diagnostic MUST distinguish local rejection from host rejection and
identify tool/call, execution/effect state, and resubmission safety. Operator MUST
retain these through the existing `engine.diagnostic_report` path and carry the
primary cause into journal, export, inspection/report, and campaign outcome.
Cleanup failures remain secondary to the original failure. Do not add a new
model-facing diagnostic tool or log credentials/unrestricted request bodies.

The QA regression is `aws-openai-gpt-5-mini-001`: four completed target invocations
followed by duplicate summary, three missing claim-level receipt lists, and an
incorrectly nested finish reason exhausted five invalid calls. Controlled tests
MUST retain that stopping diagnosis when invalid calls truly exhaust the limit.
Accepted automatic unavailable finalization and Harness exit zero MUST NOT imply
a committed assessment or successful campaign. Accepted normal finish still
requires prompt Harness exit; cleanup completion remains a later Operator fact.

## 4. Provider projections and context cost

Update Go `ModelTools`/`ModelToolsSelected` and Python `model_tools` together,
including their shape-dependent injection filters. They currently navigate
`pre_actions.items.parameters`; the new `setup` structure MUST be filtered
correctly. Preserve frozen admitted capabilities, all five injection surfaces,
and Go/Python parity. Scenario wording MUST NOT hide legitimate capabilities.

Test OpenAI Chat, OpenAI Responses, Anthropic Messages, Bedrock Converse, and
Gemini. Provider projections MUST follow their existing structural restrictions;
cross-field rules that cannot be expressed natively MUST remain enforced by the
shared validator and concisely described. Strict mode is not evidence of valid
references or authority. Do not switch it on without projection compatibility
tests. Compact arbitrary JSON declarations without removing runtime bounds.

The paired context-cost requirements apply to Operator's generated catalog and
Harness's complete model requests. Measure per-tool/catalog bytes, descriptions,
combined prompt/task/tool cost, required fields, envelope depth, total history,
peak context, retries, calls, and compaction behavior against the locked `0.5.0`
baseline. Use identical capabilities and workloads, including all surfaces,
partial reads, long histories, and ordinary-only/full catalogs.

For each codec/configuration, complete catalog bytes and combined initial
instruction/catalog cost MUST NOT exceed baseline; comparable token measurements
MUST also not increase. The ordinary-tool schema reduction target is at least
25% (SHOULD); explain a shortfall with measured tradeoffs. Any exception to the
non-growth requirements requires an explicit user decision, not silent capability
removal. Exact local token counts SHOULD be used where available; otherwise
label reproducible estimates and their method/version. No paid provider calls
are authorized merely to collect these measurements.

An equivalent ordinary workflow MUST NOT gain required model/tool turns. Existing
context accounting and compaction remain authoritative; no entire handle map or
expanded descriptors belong in every prompt. Complete active-scenario guidance,
evidence limits, and essential safety instructions MUST remain intact.

## 5. Files, compatibility, and delivery

| Area | Required work |
| --- | --- |
| `scripts/generate_tool_catalog.py`, `schemas/model-tool-arguments.schema.json` | Generate the revised authoritative arguments and concise provider projections; preserve unchanged semantic types and runtime limits. |
| `contracts/model_tools.go`, `contracts/python/operator_contracts/model_tools.py` | Update validation/defaults, shape-dependent capability filtering, tool guidance, and Go/Python parity. |
| `contracts/simple.go`, `contracts/conclusion.go`, `contracts/python/operator_contracts/simple.py`, `assessment.py` | Supply shared normalization/reference semantics; preserve canonical assessment validation and deterministic error behavior. |
| `schemas/fixtures/`, `scripts/generate_simple_fixtures.py`, Go/Python contract tests | Regenerate affected fixtures and add revised argument, normalized output, handle-resolution-input, diagnostic, and projection cases. |
| `internal/campaignservice/`, `internal/preparation/`, campaign/reporting tests | Verify unchanged host safeguards, retain translated diagnostics, and run the matching real Harness process against local targets. Change host behavior only where a demonstrated incompatibility requires it. |
| `schemas/MODEL_TOOL_CATALOG.md`, `SIMPLE_CAMPAIGN_CONTRACT.md`, `ASSESSMENT_CONTRACT.md`, `MODEL_CONTEXT_CONTRACT.md`, applicable integration docs | Document the installed interface and responsibility split; distinguish model handles from canonical host receipts. Remove obsolete caller obligations, not host integrity checks. |
| `release/contract-lock.json`, existing package/compatibility generation and `internal/imagerelease/` tests | Bind the revised package identity and reject incompatible Harness combinations before execution through the existing mechanism. |

Operator MUST build a new exact shared package and provide its source revision,
version, package digest, catalog digest, and operation digest for Harness's lock.
The new version MUST be chosen through the existing versioning convention; do not
invent a future digest or reuse `0.5.0` for changed bytes. Product and contract
versions remain distinct. Both products MUST bind the same package bytes and
compatible runtime identity, including existing embedded-image digest bindings.

No old-model-argument fallback or parallel compatibility service is permitted.
Building a local contract package is authorized implementation work; publishing
a product release is a separate authorized action. This specification itself
does not update package locks or publish artifacts. Keep work on the feature
branch targeting `candidate-v0.1.4`. Follow repository signing and dependency-pin
instructions when delivery is requested. There is no anticipated Interceptor
protocol change; if one becomes necessary, its existing qualification dependency
rules apply rather than being bypassed for this refactor.

## 6. Coordinated acceptance

Shared validator tests alone are insufficient. Operator and Harness MUST verify:

1. **Ordinary process campaign:** production Harness entrypoint and matching
   Operator service, controlled model responses and a local target; explicit
   scenario execution, observation inspection, one accepted inline finish, exit
   zero, completed cleanup, and exported assessment/evidence with correct scenario
   attribution. No artifact publication or historical record is required.
2. **QA correction:** four actual controlled invocations and handle-based claims
   normalize to correct receipt lists. A duplicate summary is rejected with a
   correctable path, then a valid finish succeeds without another target dispatch.
   The obsolete nested assessment shape receives actionable feedback.
3. **Limit exhaustion:** five consecutive invalid calls in the fixture preserve
   count/limit, last error, dispatch facts, unavailable finalization, assessment
   absence, and final cleanup state in model history and Operator export/report.
4. **Integrity:** wrong-kind/fabricated/stale/cross-campaign handles, uninspected
   or unavailable evidence, partial ranges, corrupt bytes, invalid selectors,
   origin errors, bad encodings, target mismatches, and retention failure. No
   rejected or unretained attempt may dispatch or become an execution receipt.
5. **Optional capabilities:** artifact reuse and size fallback, all record kinds,
   new hypotheses without preregistration, forward/historical references and
   invalid cycles, all admitted injection surfaces, deletion, snapshots, pagination,
   inspection, and restore. Preserve lifecycle, ownership, lineage, and cleanup.
6. **Honest outcomes:** zero-attempt, incomplete scenario, unavailable feedback,
   and uncertain-effect campaigns can finish honestly without invented claims.
   Invocation success alone does not establish objective or scenario coverage.
7. **Delivery uncertainty:** same-call duplicates, conflicting reuse of a call
   identity, interrupted finish, and restore during a batch preserve idempotency,
   non-replay, skipping of remaining calls, and acceptance-versus-cleanup status.
8. **Context/provider parity:** all five codecs accept their declarations and
   enforce equivalent runtime semantics. Go/Python catalogs agree. Long histories,
   compaction, oversized scenarios, and partial reads satisfy measured non-growth
   requirements without losing required active-scenario content or handles.
9. **Compatibility:** matching package/runtime/image identities pass the process
   workflow and existing image checks; old/new mismatches fail before execution.
   Coordinate both Harness image architectures through its existing build and
   inspection workflow, without weakening inspection pins.

The delivery report MUST state exact identities, controlled workflows exercised,
context/formatting measurements, and unverified boundaries. Controlled fixtures
demonstrate plumbing and invariants, not live-model reliability or native-container
containment. Live provider/cloud campaigns and release publication require
separate authorization.

## Correction behavior

Implementation MUST follow the [shared correction contract](../docs/TOOL_CORRECTIONS.md).
The Go/Python schema formatters MUST cover the constraint families in the tool
catalog without copying submitted values or validator prose. Shared malformed and
corrected fixtures MUST cover each advertised tool and normalization/reference
rules. Production Harness dispatch and a controlled Operator process exchange MUST
verify pagination, assessment repair without target replay, retained schema facts,
no-progress termination, and separate report/export. Compaction MUST preserve
bounded recent unresolved corrections alongside exact returned handles.
