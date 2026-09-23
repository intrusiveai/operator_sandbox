# Live attempt feedback contract

Status: agreed container contract; native/effective profile mapping updated
2026-09-21. Operator and engine runtime work
remains unimplemented. The current attempt request/result schemas define this
integration.
`catalog.json` maps their absolute schema URNs to local files for offline resolution.
Closed schemas validate structure; the semantic rules below also require validators.

## Selection and profiles

`EngineAttemptRequest.observation_selection` is optional; omission means
`{"mode":"all-permitted"}`. A selected subset uses
`{"mode":"selected","kinds":["target_output","injection_delivery"]}`.
The four closed kinds are `target_output`, `operation_error`, `injection_delivery`
and `oracle_outcome`. Reject unknown/duplicate kinds and empty selected lists
before any target contact. No mode chooses a different feedback profile.
Selection is part of canonical attempt content and duplicate-effect identity;
changing it requires a new request/attempt, not mutation of an accepted attempt.
Operator translates selection into Interceptor AttemptContext using the rules below.

| Immutable target profile | Permitted feedback |
|---|---|
| `black-box` | Natural target output plus basic invocation status. |
| `diagnostic` | Black-box plus normalized operation errors and recorded injection-delivery facts. |
| `oracle-assisted` | Diagnostic feedback plus normalized, attributed outcomes from supported oracle detectors. |

The bundle's requested profile and trusted host policy may narrow this set. Operator never broadens it or
returns raw diagnostics, oracle definitions, fixture/canary values, secrets,
provider-boundary records or protected event logs. All-permitted means all
available permitted kinds, subject to explicit capture/entry/byte limits; it does
not promise unlimited data. A fixed category summary accounts for withheld and
not-requested kinds without revealing protected detector IDs or hidden values.

### Native profile and effective harness policy

Operator MUST retain two distinct values. The **native session profile** is the
immutable profile reported by Interceptor for the bound session. Every native
AttemptContext uses that exact value in `feedback_profile`, including its native
digest. The **effective harness profile** is the least permissive of the native
profile, bundle `feedback.requested_profile`, and any host profile ceiling, ordered
`black-box < diagnostic < oracle-assisted`. Omitted host narrowing leaves the
other ceilings unchanged. A host kind allowlist may narrow that profile further;
an explicit empty allowlist permits no feedback kinds. Neither per-attempt selection
nor a narrower bundle changes Interceptor's profile or requires a new session.

Define `allowed_kinds` as the effective profile's kinds intersected with the host
kind allowlist, if configured. Disclose the effective profile and allowed kinds
in EngineContext before admission, and persist both with each attempt receipt.
`feedback.profile` in the guest result reports the effective profile; it is a
ceiling, not a promise that every kind under that profile is available. Required
bundle capabilities that cannot be met still fail input validation. Optional
feedback restrictions are explicit gaps, not native profile overrides.

For each accepted attempt:

1. Interpret omitted or `all-permitted` selection as all four known kinds; otherwise
   use the validated selected kinds. Intersect those kinds with `allowed_kinds`.
2. If the intersection is nonempty, register native AttemptContext with the native
   profile and `{"mode":"selected","kinds":[...]}` containing that intersection
   in the table's order (`target_output`, `operation_error`, `injection_delivery`,
   `oracle_outcome`). Collect the exact invocation's native observation view.
3. If the intersection is empty, omit native `observation_selection` at registration
   and record a host-side decision to skip native `observation.read` and content
   reads for this attempt. Omission is a valid native registration default; it is
   NOT permission for the adapter to collect all-permitted feedback. Do not send
   a selected-empty list, reject an otherwise valid attempt, or invent a native
   receipt. Return a host receipt and a complete manifest with no entries and the
   category states below. Basic invocation disposition still comes from execution.
4. Construct all four host category summaries independently of native summaries.
   A kind outside `allowed_kinds` is `withheld`, with reason
   `effective_feedback_policy`, even if also unselected. An allowed but unselected
   kind is `not_requested`, with reason `selection`. For allowed selected kinds,
   use validated native coverage/availability, marking missing feedback explicitly
   unavailable/partial. Never turn withheld/unavailable into successful empty data.

For example, a diagnostic session with a black-box bundle registers a diagnostic
AttemptContext selecting only `target_output`. Its native view remains diagnostic;
Operator publishes a black-box manifest and withholds the other three categories.
The native view's `not_requested` states do not override host policy withholding.

### Projection and subsequent reads

Filtering uses the closed kind/profile table and typed fields, not model judgment
or keyword scanning of target text. Validate native version, identity and receipt
integrity, then construct the guest result from allowlisted fields. A native
response is never forwarded wholesale, even when selection was correctly narrowed.

- Publish entries only for allowed, selected kinds and permitted visibility.
  Normalize metadata through the supported adapter projection; map native IDs to
  host handles and expose only descriptors for the permitted stored bytes.
- Apply the same rule to legacy `target_output` and `observations` aliases, inline
  values, nested artifacts and any model-context summaries. Aliases, when present,
  must refer only to entries in the filtered manifest. Withheld kinds have no
  entries, hidden counts, native identifiers, descriptors, excerpts or read handles.
- Basic execution/invocation/cleanup dispositions and bounded product-authored
  errors remain available. Native diagnostic text, stack traces and error details
  must not leak through `errors[].message` or category reasons. Detailed normalized
  operation errors are feedback of kind `operation_error` and require permission.
- Derive completeness from requested permitted categories only. A missing hidden
  category cannot make the guest view partial; missing permitted feedback must not
  be concealed. Unknown kinds/versions or invalid receipt identity fail protocol
  validation rather than becoming guessed feedback.
- Persist the filtered receipt-to-entry map. Every `engine.observation_read` checks
  that map and the recorded effective policy before accessing stored/native bytes;
  native permission alone is insufficient. Filtered-out entries are never added
  later. Apply any current tighter host restriction as well. Restore preserves
  original source bindings and does not enlarge an old receipt's visibility.

The machine-readable [translation vectors](fixtures/feedback-translation.json)
cover all nine native/requested profile combinations, host narrowing, selection
and empty intersections. They are adapter conformance inputs/expectations, not
additional wire fields. `validate_feedback_fixtures.py` checks their consistency
and existing selection/manifest schemas; production adapter/read-path tests remain
required with Operator implementation.
Run `python3 schemas/validate_feedback_fixtures.py` from the Operator repository
with the `jsonschema` package installed.

## Collection and interpretation

An attempt result retains separate execution, target-contact, invocation and
cleanup dispositions. `completed` is not proof of attack success. Collect feedback
for the exact invocation returned by the target, before cleanup and before another
attempt or restore. The host commits the filtered view and receipt mapping before
delivering the attempt result. Freeze that view: reading it never invokes the
target, reruns evaluation against changed state, or adds later asynchronous facts.
For targets with asynchronous effects, this initial profile has no wait/polling
window beyond invocation completion. Report that limitation; post-run assessment
may observe later effects without reopening experimental execution.

Bind the result and every entry to the campaign, attempt, original target session,
invocation and campaign revision in host records. Native receipt IDs and session
routes stay host-side. Guest worker/revision provenance is not an access gate;
previous same-campaign receipts remain readable while the harness is admitted.
A target restore neither redirects old receipts to the new session nor erases them.
Terminal teardown remains terminal; feedback reads do not reopen a failed campaign.

`feedback` in the attempt result contains profile, collection state, fixed category
summaries and up to 64 entries. Categories have states `available`, `empty`,
`withheld`, `not_requested`, `unavailable` or `partial`, plus a bounded reason.
Each entry has an opaque `entry_id`, kind, visibility, source, assurance,
availability, truncation flag, original size if known, and an artifact descriptor
when readable. The artifact's size/digest describe the permitted stored bytes,
not unredacted or uncaptured originals. Never expose a protected-original digest.
An actually empty output has an available zero-byte artifact and its correct hash.
Unavailable output has no fake empty artifact. Entry overflow marks the collection
and affected category partial; never silently discard references.

Delivery observations identify the submitted action (mapped from native injection
ID) and distinguish `applied`, `failed`, `not_matched` when positively established,
and `unknown`. They describe available evidence, not guessed target reasoning.
Current Interceptor records can establish application/failure; absence of a
matching event yields unknown, not not-matched or resisted. Disclose whether the
native evidence is scoped to the selected turn or only the attempt. Preserve
multiple recorded delivery events rather than letting a later failure erase an
earlier application. Parent/session attribution is kept through restore.

Oracle-assisted results may report supported attributed detector outcomes. The
current Interceptor projection reports positive event-backed outcomes; absence
of a positive result is not proof of failure. State-only, unsupported or
unattributed outcomes are explicit limitations. A target's self-report remains
text, not independent verification. Basic invocation success, injection delivery,
observed behavior and objective achievement remain separate facts.

## `engine.observation_read`

The host tool catalog and compatible harness expose this fixed, read-only operation.
Request fields are `receipt_id` (the original attempt-result receipt), `entry_id`,
nonnegative byte `offset` and positive `max_bytes` (at most 262,144).
There is no path, URL, bare digest, campaign override or arbitrary artifact lookup.
The host verifies its campaign-to-receipt-to-entry mapping, permitted visibility
and active channel admission. A receipt is a lookup handle, not an ownership token.

A successful response repeats receipt/entry IDs, availability, actual offset,
base64 `content`, `raw_length`, `eof`, stored artifact descriptor when available,
truncation and bounded reason. Missing/corrupt/evicted content is explicitly
unavailable; it is never successful empty content. Invalid/foreign handles and
invalid ranges are typed errors. Offset equal to stored size returns zero bytes
with EOF; offset beyond it is a range error. Unavailability returns no bytes and
EOF false. Only a valid response from the still-live host can describe optional
unavailability; transport loss/protocol corruption retains terminal semantics.

Bound ordinary frames to 4 MiB and raw chunks to 256 KiB. Initial campaign read
ceilings are 1 GiB of returned raw bytes and 8,192 read requests, narrowed by policy
and disclosed in EngineContext/remaining limits. Charge repeated reads as reads,
never as new target attempts; do not reset counters on target restore. Limits
produce explicit feedback-budget errors, not silent truncation or guessed bytes.
Host journal/response durability rules apply before delivery. No bulk content goes
on control pipes, and a missing optional read cannot replay an experiment.

Use these bounded error codes in the ordinary correlated error envelope:
`INVALID_OBSERVATION_READ` (invalid fields), `OBSERVATION_NOT_FOUND` (unknown
receipt/entry), `OBSERVATION_NOT_PERMITTED` (campaign/visibility denial),
`OBSERVATION_RANGE_INVALID` (offset past stored content),
`FEEDBACK_BUDGET_EXCEEDED` (read count/bytes), and
`OBSERVATION_INTEGRITY_FAILED` (inconsistent receipt/descriptor). Correctable
read errors have no target effects; do not retry the experiment. Missing content
behind a valid receipt uses the successful response's unavailable state instead.
Integrity/protocol conflicts are terminal; optional absence/budget exhaustion is
an explicit feedback gap within the remaining campaign policy.

The guest verifies identity, descriptor consistency, offsets and raw lengths;
assembles bounded chunks; verifies the full raw digest only after reading the
entire stored artifact; and decodes UTF-8 incrementally across byte boundaries.
EOF proves the stored artifact ended, not that original capture or all feedback
was complete. Do not execute/render active content. Unsupported media stays opaque.
The model receives decoded permitted content with source, assurance, coverage and
truncation notes, not base64 alone. Reserve model context for results, preserve
correlated excerpts and references, and mark summaries and omitted ranges. Bytes
retrieved by the harness are not automatically bytes seen by the model.

## Interceptor mapping and compatibility

Native `observation.read` now returns `observation-view/v1alpha2`. Its request
selects `turn_id`; omission works only when exactly one invocation exists for the
attempt. `receipt_id` instead retrieves an already fixed view. Do not select the
latest turn implicitly. Native `observation.content.read` takes receipt, native
entry ID, offset and max bytes, with the original session/attempt/context digest
in the v1alpha2 operation envelope. Operator persists the mapping from its opaque
attempt receipt and entries to those native references.

Retention ends on explicit administrative purge of the owning campaign/session
group. Each group owns its required content bytes; matching digests in another group
do not authorize fallback reads. Operator and Interceptor purge their own stores
independently, so a previously committed Operator copy can survive Interceptor purge.

Retained reads address the source session and use the same campaign, including
after restore/stop. Interceptor validates source campaign/attempt/digest and
stored receipt integrity; it never restarts a target for reads. New receipt
collection is unavailable after the session stops, so Operator must collect and
commit it before transition/teardown. Retention loss is reported, not reconstructed
from a different invocation. Matching host and rebuilt supervisor versions are
required. Capabilities advertise the new content-read operation and feedback
contract; incompatible profiles fail preparation rather than losing byte access.

The generic native `artifact.read` remains an administrative/adapter operation;
it is not exposed as a general guest tool. Guest reads use the receipt-scoped path.
This contract resolves HC-02/HC-03 design choices. SDK generation, broker/harness
implementation and end-to-end qualification remain HC-01/HC-06 deliverables.
