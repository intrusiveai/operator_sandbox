# Implementation phase handoff

## Completed service stage: campaign artifact uploads

The [artifact service](ARTIFACT_SERVICE.md) adds begin/part/commit, durable
campaign-local receipts, contiguous chunks, complete-content verification and
attempt resolution of uploaded payloads/carriers. Upload reservations and receipts
survive healthy restore. One conclusion slot and up to 1 MiB remain protected
inside the campaign allowance. Large ordinary tool bodies use verified content
instead of inline journal metadata.

Ten ordinary routes are implemented. Model relay and typed assessment/completion
remain the next service stages. The implementation still rejects advertised routes
that have no handler; no full campaign-launch command exists yet.

## Earlier completed phase: snapshot lifecycle and healthy restore coordination

The [snapshot service](SNAPSHOT_SERVICE.md) now implements checkpoint create/list/
inspect and restore through the ordinary service gate. It uses the shared tool
namespace, immutable preparation, native receipt validators and durable journal.

- Snapshot admissions and canonical byte charges survive restore and replay.
- Public metadata preserves campaign/description fields and source/checkpoint handles.
- Verified replacement binding is committed before the correlated restore reply.
- Healthy restore preserves the harness, manifest, Docker identity and transport sequences.
- Known preflight rejection continues the original revision; uncertainty stays terminal.
- Checkpoint parent/turn inventory rolls back with target state. Campaign history
  remains available, with native-root rebasing when a requested parent is absent.
- Planned transitions do not trip the idle watcher; independent stop still works.

This is the completed lifecycle boundary within service integration. Seven ordinary
routes are now implemented. Artifact upload, model relay, assessment and conclusion/
stop routes remain the next service stage; no full campaign-launch command exists yet.
Shared wire contracts are unchanged and no sibling repository was edited.

Validation: full Go/Python suite and shared fixtures, race tests, go vet and service
builds for Linux amd64/arm64 and macOS amd64. Integration tests use real journals,
validators and physical spools with scripted native/Docker peers; runtime qualification
and live Interceptor/Attack Harness testing remain pending.

## Earlier completed phase: campaign preparation and service foundation

The [preparation layer](CAMPAIGN_PREPARATION.md) and
[campaign service](CAMPAIGN_SERVICE.md) now join immutable inputs to live ordinary
dispatch and independent termination.

| Area | Implemented |
| --- | --- |
| Administrator policy | Private TargetProfile file selected by host configuration; concrete native scopes, feedback ceiling and explicit target-stop permission. |
| Preparation | Verified package, live target and bundle compatibility; checked artifact bytes; frozen policy/context/model/release/manifest pins and retained provenance. |
| Admission | Full five-message startup and input validation, live target and exact saved Docker checks, durable admission record. |
| Dispatch | Concrete source/lineage/policy callbacks, fresh native mutation revisions, exact request/response audit and advertised-route enforcement. |
| Transport | Ordinary FIFO/spool queue integration with original deadlines, guest-relative lane directions and sequence zero; physical spool integration test. |
| Lifetime | Absolute campaign timer and idle native status watcher; terminal cancellation and Docker termination independent of the ordinary gate. |
| Terminal cleanup | Pre-reserved audit capacity, confirmed native closure, at most 64 confirmed injection IDs within 30 seconds, separate optional target-stop outcome. |

This initial foundation supported attempt execution, observation reads and retained
injection deletion. The lifecycle stage above expands that set to seven routes.
Construction still rejects unimplemented advertised routes. This completes
the preparation/admission/dispatch/termination foundation, not the full campaign
service: snapshot/restore is implemented above; artifact uploads, model relay and
conclusion/stop remain the next service stage. No new shared wire schema or sibling
repository change was required.

Validation: full Go/Python suite and shared fixtures, targeted race tests, `go vet`,
and preparation/service test builds for Linux amd64/arm64 and macOS amd64. Tests run
on macOS arm64 using real journals and spool files with scripted native/Docker
interfaces. Cross-compilation does not qualify Docker or a live Interceptor/Attack
Harness deployment. Campaign start CLI and Docker launch remain pending.

## Earlier completed phase: durable receipts and ordinary attempt broker

The [receipt publication layer](DURABLE_RECEIPTS.md) and
[attempt broker](ATTEMPT_BROKER.md) now connect typed execution to durable guest
results, disk-backed feedback reads and retained-injection deletion.

| Area | Implemented |
| --- | --- |
| Publication | Capacity reserved before dispatch; feedback and index staged before atomic final-result adoption; interrupted staging exposes no receipt. |
| Retention | Immutable original source/mappings and verified content chunks; confirmed arm/deletion records back injection handles. |
| Attempts | Observe before tactical rejection, charge admission once, execute the fixed native plan, persist results before correlated replies. |
| Reads | Current admission/visibility/range checks, complete-byte hash verification, explicit optional unavailability, cumulative count/byte reservation and settlement. |
| Cleanup | Same-campaign handles resolve to the current target; typed native deletion and durable receipts; confirmed absence distinguished from transport/storage errors. |
| Duplicates and restore | Shared operation namespace, serialized concurrent duplicates, old result replay, persistent counters and new cleanup against restored injections. |
| Failure | Uncertainty and journal loss close execution; no uncommitted reply or repeated native effect. |

The broker is a host library with installed source/admission callbacks. The service
foundation above now supplies those callbacks, transport integration, post-closure
cleanup and independent termination. Restore coordination is implemented in the lifecycle stage above.
Guest execution cannot use the ordinary broker after the terminal fence. Shared
wire schemas remain unchanged; no sibling repo was edited.

Validation passed: full Go/Python tests and shared fixtures, targeted race tests,
`go vet`, and broker test builds for Linux amd64/arm64 and macOS amd64. Tests execute
on macOS arm64 with real journals and a scripted native peer. This does not qualify
Docker or a live Interceptor/Attack Harness deployment.

## Earlier completed phase: typed attempt execution and feedback

The host [typed attempt adapter](TYPED_ATTEMPT_ADAPTER.md) now connects reviewed
scope decisions and checked bundle/live bindings to the durable native executor.
The [feedback projection](FEEDBACK_PROJECTION.md) supplies the corresponding
filtered, receipt-scoped observation view.

| Area | Implemented |
| --- | --- |
| Concrete policy | Native-capability checks for administrator-selected operations and injection routes, scope/placement/pointer/merge restrictions, optional caller identities and retention permission. |
| Pure translation | Shared schema plus artifact bytes/campaign/digest/media/JCS checks, scenario/release attribution, current native parent lineage and delivery-schema validation. |
| Native commands | Deterministic IDs, immutable native context digest/profile, declared injection setup, correct carrier handling, one invocation, exact-turn feedback and requested cleanup. |
| Durable execution | Original request/plan/target admission checks, native revision progression, strict typed response interpretation and committed results before continuation. |
| Feedback policy | All 17 shared selection vectors, native/effective profile separation, explicit empty-selection skip and fixed category summaries. |
| Feedback bytes | Original receipt identity/hash checks, visibility filtering, bounded chunks with complete-content hashing, typed diagnostic projection and immutable receipt reads. |
| Terminal results | Known failure versus uncertainty, no subsequent experimental dispatch, sanitized guest errors and confirmed-created injection handles for cleanup. |

Tests use real campaign journals and the native executor with a scripted native
peer. They cover multi-chunk output, retained actions, optional unavailable content,
empty selection, policy/lineage/artifact denials, target loss before invocation,
setup rejection, lost invocation replies, malformed receipts and journal loss after
invocation. Zero-byte output remains available with EOF; unavailable output does
not become a fake empty artifact. Native attempt/observation fixtures were captured
independently from Interceptor using a temporary Go overlay without repository edits.

This completes the host adapter boundary. `Run` returns schema-validated guest JSON,
frozen feedback and injection handles; the broker above now atomically retains them
before replying and implements retained-handle cleanup. The service foundation
above supplies the bounded terminal cleanup controller, concrete TargetProfile,
immutable campaign preparation and effective context publication. Existing Operator/Attack Harness
wire contracts are unchanged; no sibling repository was edited.

Validation: full Go/Python suite and shared fixtures, race tests, `go vet`, and
adapter/feedback test builds for Linux amd64/arm64 and macOS amd64. Native execution
tests run on macOS arm64. This validates host protocol/library behavior; it does
not qualify Docker, a live Interceptor deployment or the harness runtime.

## Earlier completed phase: durable native execution core

`internal/nativeexec` now connects admitted attempt steps to the Interceptor
client and campaign journal. The [native execution guide](NATIVE_EXECUTION.md)
describes its ordering, bounds, guard checks and reporting-only reconciliation.

| Area | Implemented |
| --- | --- |
| Per-step persistence | Exact native request/response envelopes, ordered bounded journal members, separate audit reservations and verified disk reads. |
| Parent integration | Steps require an admitted/dispatched attempt; one unresolved step at a time; pending/failed/unknown steps cannot produce successful attempt completion. |
| Duplicate protection | One durable dispatch grant; native command identity preserves command/deadline while excluding worker/revision attribution. |
| Live guard | Pinned Interceptor process/session/revision and the persisted exact Docker daemon/container/image/labels/lifecycle identity. |
| Terminal handling | Failure/uncertainty fences before journal locks; the fence cancels in-flight native calls; no automatic replay or renewed deadline. |
| Reporting | One separately recorded operation-status query from reserved capacity; fingerprints/records verified; results never reopen unknown execution. |
| Restore continuity | A verified replacement can rebind future work while preserving old step results, attribution, cumulative admissions and attempt numbering. |

The executor requires trusted adapter authorization and operation-specific result
interpretation. The completed typed adapter above now supplies these hooks. The
executor itself does not expose a generic execution tool or launch campaigns.

Tests join the real journal/attempt ledger to a scripted native peer and exercise
lost replies, completed-record reconciliation, malformed/foreign/missing records,
concurrency, live-binding failures, cancellation, verified restore adoption and the
independent Docker termination observer. A subprocess exits after native dispatch
without closing its writer; inspection retains unknown outcomes and reservations.
Storage-failure tests cover intent, dispatch, result and reconciliation writes.
These tests use synthetic operation bodies and installed test adapters; they do
not qualify operation-specific native semantics or a real Docker deployment.

Validation passed: `make test` (Go/Python and shared fixtures), `go vet ./...`,
race tests for campaign/client/Docker/executor packages, and executor test builds
for Linux amd64/arm64 and macOS amd64. Execution tests run on macOS arm64;
cross-compilation is build coverage, not runtime qualification.

## Earlier completed phase: target capability and bundle compatibility

The host `internal/capabilities` layer now verifies native capability provenance,
projects public exports, imports hash-named companions, validates bundle semantics
and checks compatibility against a ready live binding and trusted policy snapshot.
The [capability admission guide](CAPABILITY_ADMISSION.md) describes its API and
integration responsibilities.

| Area | Implemented |
| --- | --- |
| Native verification | Exact typed digest recipe, strict JSON/field shapes, supported vocabulary, duplicate identifiers and offline delivery contracts. |
| Public projection | Deterministic allowlisted records, native/public provenance hashes, full JCS projection hashing and safe companion import. |
| Bundle validation | Schema, unique IDs, internal links, coverage, narrative byte limits, artifact descriptor consistency and declared omissions. |
| Live binding | Ready process/session/revision, campaign identity, capability/environment/application hashes and advertised native profile. |
| Compatibility | Required dependencies, explicit optional gaps/disabled routes, source provenance, compatible updates and opt-in exact pins. |
| Policy handoff | Explicit host decisions and selectable action routes; preserved native profile, effective profile and allowed feedback kinds. |
| Frozen records | Unchanged bundle bytes plus authoring/live provenance, binding, policy digest and compatibility results. |

This is a host library boundary. It does not yet implement CLI export writing or
campaign startup. Later phases above supply TargetProfile route resolution,
per-attempt authorization and immutable preparation.
Artifact descriptors do not substitute for verification and staging of actual
artifact bytes. Compatibility records must be persisted with the immutable input
and installed contract-package pins by the campaign preparation layer.

## Earlier completed phase: native Interceptor client foundation

The host-only `internal/interceptor` layer now covers the current local API routes
and their transport/lifecycle envelopes. It is ready for the next host adapter and
campaign-broker phase. It does not itself authorize guest requests or run campaigns.

| Area | Implemented |
| --- | --- |
| Connection policy | Fixed IPv4 loopback endpoint; bounded JSON; no proxy, credentials, redirects, compression, connection reuse or automatic replay. |
| Target binding | Campaign attachment, active/retained session status, instance identity checks and native closure acknowledgements. |
| Native operations | Frozen v1alpha2 envelopes, exact body digests/deadlines, dispatch and operation-record reconciliation. |
| Lifecycle | Frozen restore/stop requests, replacement lineage checks and separate lifecycle-record reconciliation. |
| Snapshots | Explicit remaining byte allowance, creation receipts, native checkpoint hashes, bounded inventory and scoped inspection. |
| Evidence transport | Configurable local ceiling, pinned native ceiling, bounded streaming, size/digest checks and private temporary-file cleanup. |
| Archive structure | Native member-name restrictions, regular-file/entry/expanded-byte limits, framing/footer checks, blob hashes and read-only member access without extraction. |

The [client guide](INTERCEPTOR_CLIENT.md) records method contracts, limits and caller
responsibilities. Existing foundations also provide shared Go/Python contract
validation, host campaign persistence/terminal fencing, transport, Docker identity
and termination, local image/release validation, configuration and input staging.

## Validation achieved

- Copied native request/capability fixtures and independently generated native
  checkpoint fixtures pin relevant serialization behavior.
- Unit tests cover malformed frames, quotas, hashes, identities, failure states,
  deadlines, temporary storage and archive boundaries.
- A stateful HTTP test covers attach/status, snapshot create/list/inspect, restore
  with a lost reply, both record ledgers, cleanup closure, confirmed stop and retained
  source-session evidence download/inspection. It verifies that effects run once
  and that reads/cleanup address the correct old or replacement session.
- The lost-reply test never resumes target experiments after uncertainty. A late
  successful record is used to locate the replacement for cleanup/reporting only.

These are protocol tests against test servers. They do not qualify a real Docker
launcher, native Linux/macOS runtime, live Interceptor target or Attack Harness.
Cross-compilation establishes build compatibility only.

Additional validation for this phase compares the Go adapter against the existing
native hash preimage and public/JCS golden files, exercises live binding failures,
required/optional dependency changes and policy denials, and checks the
profile/allowed-kind portions of every shared feedback translation vector. Safe
companion loading rejects links, special files and mismatched bytes. The new
packages pass race tests; the full Go/Python suite, fixture runners and `go vet`
pass. Capability tests also compile for Linux amd64/arm64 and macOS amd64; they
execute on macOS arm64. These builds do not qualify Docker runtime behavior.

## Next major phase: artifact, model and completion service routes

1. Add artifact upload/reference, model relay and assessment/stop routes with the
   shared operation namespace, durable publication and campaign/loop budgets.
2. Coordinate structured conclusions and bounded graceful finalization with the
   independent control and Docker termination paths.
3. Expand the advertised operation set only as each route is implemented and tested.
   Preserve existing restore/duplicate behavior and lifetime counters.

Native evidence provenance validation remains a separate required phase: verify
manifest identities, event/state chains, execution records, references, restore
lineage and completeness before publication/reporting. Structural archive inspection
alone cannot establish these claims.

Operator campaign start/service orchestration, guest Docker launch/bootstrap and
runtime qualification follow these host integrations. The Attack Harness Python
dispatcher/model loop remains a subsequent component phase. No completed library
boundary substitutes for these pending service and runtime gates.
