# Implementation phase handoff

## Completed phase: durable receipts and ordinary attempt broker

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

The broker is a host library with installed source/admission callbacks. It does not
start a campaign or run a transport service. The next phase supplies those concrete
callbacks, owns the full restore transition and wires post-closure cleanup and
independent termination. Guest execution cannot use the ordinary broker after the
terminal fence. Shared wire schemas remain unchanged; no sibling repo was edited.

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
before replying and implements retained-handle cleanup. A terminal cleanup
controller must use confirmed handles after execution is fenced. Concrete
TargetProfile configuration, immutable campaign preparation and effective context
publication remain service integration work. Existing Operator/Attack Harness
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

This is a host library boundary. It does not yet implement CLI export writing,
TargetProfile route resolution, per-attempt authorization or campaign startup.
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

## Next major phase: campaign preparation and service integration

1. Wire concrete TargetProfile configuration, live bundle compatibility, verified
   artifact/lineage sources and installed contract pins into immutable campaign
   preparation and effective EngineContext publication. Supply the broker's trusted
   callbacks, including current native revision and policy checks.
2. Connect transport admission/envelope audit and the ordinary broker to service
   dispatch; coordinate snapshot/restore and remaining operation routes through the
   same drain/transition gate. Keep model/reference-read and finalization accounting.
3. Add bounded host-owned terminal cleanup using confirmed handles and audit
   capacity reserved before effects. An uncertain outcome cannot reopen guest
   execution. Retain closure, cleanup and reporting outcomes independently.
4. Arm the independent Docker observer and campaign/operation timers in the service;
   wire native target closure and lifecycle cleanup. Recheck immutable preparation
   before launch and preserve the harness across healthy target restores.

Native evidence provenance validation remains a separate required phase: verify
manifest identities, event/state chains, execution records, references, restore
lineage and completeness before publication/reporting. Structural archive inspection
alone cannot establish these claims.

Operator campaign start/service orchestration, guest Docker launch/bootstrap and
runtime qualification follow these host integrations. The Attack Harness Python
dispatcher/model loop remains a subsequent component phase. No completed library
boundary substitutes for these pending service and runtime gates.
