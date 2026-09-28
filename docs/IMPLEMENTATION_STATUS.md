# Implementation phase handoff

## Latest boundary: native attach reconciliation before preparation

Installed startup now writes a durable attach intent before native contact and
records the returned session and matching Interceptor instance before preparation.
Startup inventories attachment-only failures, preserves frozen stop permission,
and reuses bounded, non-replayable closure/stop recovery for verified identities.
A prepared campaign journal takes precedence. Lost replies or unsaved identities
produce explicit uncertainty without native contact or reattachment.

Tests cover dispatch ordering, publication failures, active writers, changed
identity/policy, partial preparation, journal handoff and interrupted cleanup.
The full Go suite, focused recovery race tests and affected-package vet pass.
Affected packages compile for Linux AMD64 and ARM64; these builds do not qualify
native execution on those hosts.
Late evidence collection is the next lifecycle boundary, followed by
reporting/export and campaign purge. Native runtime qualification remains separate.

## Completed boundary: native finalization after worker loss

Installed startup now performs a bounded, separately journaled native cleanup pass
after Docker and transient-resource reconciliation. It verifies retained target
and policy pins, uses only verified restore bindings, closes admission, deletes up
to 64 known injections and honors the frozen target-stop opt-in. A permanent claim
and audited request/result pairs prevent automatic replay after interruption.
Completed live finalization is reused, including its uncertainty. The recovery
result binds its step inventory; corruption cannot become a fresh dispatch grant.

Integration tests reuse real preparation/journals and a scripted native peer for
closure, injection provenance, restore, changed identity, policy checks, limits,
lost replies and startup ownership. Late evidence collection, reporting/export,
purge and native runtime qualification remain outstanding.
The full Go suite, focused recovery race tests and affected-package vet pass.
Affected packages compile for Linux AMD64 and ARM64; these builds do not qualify
native execution on those hosts.

## Completed boundary: abandoned transient filesystem cleanup

Startup now removes the fixed transport and generated-policy directories after
container absence, under the campaign writer lock. It preserves unarchived input
copies and only removes staged inputs after verified retention. Streaming bounded
traversal handles hostile spool leftovers without following links or reading file
contents. Separate intent/latest-result records preserve cleanup state. Filesystem,
race and startup integration tests pass; Linux compilation is checked separately
from runtime qualification. Native target finalization is the next boundary.

## Completed boundary: lost-create identity reconciliation

Startup now discovers a lost Docker create reply only from a complete verified
start intent and one matching full container identity on the original daemon.
It persists the binding under the campaign writer lock before exact-ID cleanup.
Ambiguous/missing candidates and damaged or partial evidence block admission;
cleanup failure retains the recovered identity. A complete ID returned alongside
a create error is retained without granting start permission. Journal contents
are preserved and campaigns never resume. See [startup recovery](STARTUP_RECOVERY.md).

Focused filesystem and scripted Docker tests cover discovery, changed identity,
active writers, interrupted publication, repeated cleanup and failure retention.
The full Go suite, focused campaign/Docker/startup race tests and affected-package
vet pass. Native lost-reply Docker qualification remains outstanding. Operator's next
lifecycle work is transient resource cleanup and native target finalization after
worker loss, followed by reporting/export and campaign purge. Attack Harness's
runtime is implemented separately; its remaining qualification and publication
gates are tracked in that repository's implementation status and acceptance matrix.

## Completed boundary: two-stage confinement startup feasibility

The real Distroless ARM64 confinement probe exposed a missing `openat2` allowance
in Docker's pre-Python startup path. The startup policy now permits it; the
Attack Harness live policy omits it. The corrected two-stage policy passed twelve
denial probes and permitted bounded file I/O in the pinned Python 3.13.5 runtime.
See Attack Harness's `native/README.md` for scope and remaining qualification.

## Completed boundary: public preparation and supervised startup

`campaign prepare` freezes an offline start request; `campaign start` submits its
fixed worker to systemd or launchd and waits for retained acceptance. Repeated
starts preserve identity, source changes conflict, and `--new-campaign` retains
history while selecting a fresh intent. Run-directory status/log/wait observers
include pre-journal preparation failures. Manager arguments, uncertainty,
observer-only timeout, and short publication races have focused tests.

See [campaign startup](CAMPAIGN_START.md). Live macOS LaunchAgent worker dispatch
has a passing opt-in test; Linux service qualification, pre-claim service-registration reconciliation, frozen skill-set selection,
and installation packaging remain outstanding. The Python runtime has since been
implemented in Attack Harness; external qualification remains outstanding.

## Completed boundary: run links and worker dispatch

Submitted runs now have private locked start links, explicit new-campaign
replacement and immutable history. A fixed `_worker` entrypoint claims a saved
request once, checks frozen input identity, prepares, records acceptance and runs
the composed session. Failures produce fixed-code completion records; changed
inputs and failed acceptance never reach execution. Tests cover claim replay,
preparation failure, cancellation ownership, link copying/purge behavior and safe
worker diagnostics. OS service installation and public start/wait wiring remain
next; no live Docker/Python or external-provider qualification is claimed.

## Completed boundary: durable start requests

Private typed start requests now bind frozen inputs to one immutable request key.
A durable one-time worker claim survives process loss; identical replays return
existing state and changed inputs conflict. Acceptance and fixed-code completion
records distinguish submitted/claimed/accepted/finished/failed states. Race and
child-process tests cover competing claims, lost-worker non-resumption and damaged
publication. The input fingerprint also binds effective installation defaults.
See [durable start requests](START_REQUESTS.md). CLI run links, supervisor submission
and worker dispatch remain the next integration.

## Completed boundary: installed campaign input loading

Offline input loading now verifies installed profiles and a submitted run and
freezes a fingerprint before online work. Tests cover changed target/model/secret
configuration and prompt bytes, missing credentials, target mismatch, changed
submission bytes, malformed host selections and single-use online preparation.
The online loader connects the existing exact-Docker/image/release/skill/native
and provider components to the composed session; durable start requests and
supervised CLI dispatch are the next integration. Real image/provider/secret-store
qualification remains outstanding.

## Current boundary: composed launch sessions

`internal/hostrun.Prepare` now transfers the startup lease into a single-use
session, builds production launch inputs, freezes journal storage policy, retains
all mounted bytes, persists target preparation, creates the OS transport and
constructs the worker before recording acceptance. Run owns finalization; cancel
before Run discards only never-started resources. Early worker failures now also
record terminal outcomes. Administrator journal defaults are 8 GiB total, 16 MiB
segments and a 256 MiB free-space floor. CLI/service submission ownership remains
the next integration; these library tests use a scripted guest and Docker peer.
See [composed launch sessions](LAUNCH_SESSION.md).

## Current boundary: startup container reconciliation

`internal/startup.Acquire` holds the installation worker lease while inspecting
prior campaign groups and checking saved Docker bindings. It rejects active
writers and missing identity after a start intent, independently terminates active
orphan containers, and removes confirmed stopped containers. Exact-ID absence is
verified against the saved daemon; inspect errors never imply absence. Damaged
journals remain retained and explicitly incomplete. The gate is ready for the
executable start composition. Lost-create discovery is implemented at the latest
boundary above. See [startup recovery](STARTUP_RECOVERY.md).

## Campaign observers

Live journal observation now verifies a captured committed prefix without taking
the writer lock. Concurrent head publication, pending tails, committed corruption
and cancellation are covered by filesystem tests; strict recovery inspection
continues to reject incomplete journals. The CLI now exposes `campaign status`, bounded `campaign logs` pages and
`campaign wait` by campaign ID; [observer semantics](CAMPAIGN_OBSERVATION.md)
distinguish recorded facts from process health and campaign success.

## Current boundary: retained launch inputs and worker serialization

Staged launch inputs can now be retained in bounded journal parts with a final
verified completion record. Corruption, cancellation and mismatched staging fence
the campaign. The installed state root has a private nonblocking worker lease;
cross-process tests confirm contention and automatic release on process loss.
The lease does not reconcile old Docker containers or authorize recovered execution.
CLI/service orchestration and startup recovery gates remain required.

## Current boundary: production launch-input assembly

`Target.BuildLaunch` now composes the verified image, embedded prompt/loader, private
provider settings, shared tool catalog, selected skills, submitted references and
resolved budgets into EngineContext, input manifests and RunManifest. Tests exercise
all five codecs through startup identity validation and real immutable staging,
including a locally signed custom skill. Checked embedded bytes remain bound to
the approved image/release and local daemon. CLI/service orchestration, retained
launch-file publication, Attack Harness runtime and live qualification remain.

## Current boundary: production limits and passive reference preparation

Administrator YAML now accepts closed campaign/harness overrides and resolves all
defaults. Bundle requests can only narrow them; snapshot zero remains disabled and
all wire counters use the approved JSON-safe bound. Passive submitted references
are revalidated, copied, canonically bound into EngineContext and retained in the
campaign journal separately from native execution artifacts. Tests cover malformed
configuration, overflow/widening, changed input bytes, omissions and shared-digest
metadata conflicts. Executable launch-input assembly remains the next integration.

## Current boundary: immutable image release-file inspection

Operator can inspect fixed files from an approved local image without running guest
code. Bounded in-memory archive reads reject links and extra content; exact Docker
identity and stopped-state checks govern cleanup. Embedded prompt/loader/catalog
bytes must match the closed manifest, installed package and all five native tool
projections. See [release file admission](../schemas/ENGINE_RELEASE_CONTRACT.md#5-embedded-image-files).
Executable startup wiring and actual image qualification remain outstanding.

## Current boundary: shared fixed model tools

The installed package now defines eleven model-facing tools with matching Go/Python
projections for all five codecs. Forty-eight shared fixtures cover exact catalog
digests, dependency selection, model arguments and reserved bookkeeping fields.
Native projections also pass full model-request validation. See the
[tool catalog](../schemas/MODEL_TOOL_CATALOG.md). Executable startup wiring and the
Attack Harness runtime remain outstanding.

## Current boundary: private provider options and public startup projection

Administrator profiles now require codec-specific `codec_options`, with shared
schema validation and explicit allowed response model IDs. `PublicModel` derives
startup caps/tool digests from the frozen host profile and trusted native catalog,
without exposing endpoints or credential selectors. All ten supported
provider/codec combinations match the shared startup fixtures. Settings accessors
return independent copies. See [Model providers](MODEL_PROVIDERS.md).

Full Go/Python suite, provider race tests and vet pass on macOS arm64. The next
work connects the fixed model-tool projection and executable campaign startup.

## Current boundary: stateless Responses contract

All five specified native codec families now have shared Go/Python validation:
Chat, Responses, Anthropic Messages, Bedrock Converse and Gemini GenerateContent.
Responses preserves encrypted reasoning, assistant phases and complete function
batches. Its 66 shared cases and host integration cover policy binding, continuation,
failed/incomplete results, cumulative usage and replay. See the
[Responses contract](../schemas/RESPONSES_MODEL_CODEC_CONTRACT.md).

Full Go/Python suite, targeted contract/model-service race tests and vet pass
on macOS arm64. Provider profiles now have explicit startup codec options and a safe projection
constructor; executable startup/tool-projection wiring remains. The
Attack Harness loop and live provider/runtime qualification remain outstanding.

## Current boundary: native Gemini contract and campaign accounting

The shared Go/Python contract now supports Gemini GenerateContent on the configured
Developer API or Vertex route. It preserves thought signatures and native content,
correlates calls without native IDs by part position, and accounts for thinking
and cached tokens without double charging. Sixty-seven shared fixtures cover
continuation, policy binding, incomplete batches, filtering and usage failures.
See the [Gemini contract](../schemas/GEMINI_MODEL_CODEC_CONTRACT.md).

Full Go/Python suite, targeted contract/model-service race tests and vet pass.
The bootstrap success test passed three isolated repeats after a load-sensitive
two-second timeout in the full run; its filesystem/attempt deadline is now ten
seconds while silent-peer coverage remains 100 ms. Executable
startup/profile integration and live provider qualification remain.

## Current boundary: native Bedrock contract and campaign accounting

The shared Go/Python contract now supports native Bedrock Converse requests,
reasoning/signature continuation, ordered tool batches and cache-inclusive usage.
Fifty shared cases cover policy, accounting and rejected unsupported semantics.
Host integration tests exercise replay, snapshot restore and terminal accounting
failures for both Bedrock and Anthropic. See the
[Bedrock contract](../schemas/BEDROCK_MODEL_CODEC_CONTRACT.md).

Validation: full Go/Python suite, affected contract/service race tests and vet
pass on macOS arm64. Real Bedrock/model credentials and runtime
qualification remain outstanding.

## Current boundary: native Anthropic contract and campaign accounting

The shared schemas and Go/Python validators now support Anthropic Messages,
including native tool-result batches, opaque thinking blocks, pinned thinking
settings, refusal/partial-result rules and cache-inclusive usage accounting.
Campaign model reservations/settlement use validated codec helpers. The 49 new
shared cases and host integration tests cover duplicate generation, cache charges
across restore, unknown usage and excessive cached input. See the
[codec contract](../schemas/ANTHROPIC_MODEL_CODEC_CONTRACT.md).
Validation: full Go/Python and fixture suites, targeted contract/model-service race
tests and affected-package `go vet` pass on macOS arm64.

Executable profile/startup integration and
real-provider qualification remain required. Transport coverage alone is not
campaign readiness for those routes.

## Current boundary: reproducible contract package tooling

`operatorctl contract build` packages the root schemas/catalog/registry, real
semantic-profile documents, matching Go/Python validator sources and shared
fixtures with an exact canonical inventory. The output is checked through the
production installed loader. Repeated builds produce the same identity; existing
outputs are not overwritten. See [Contract publication](CONTRACT_PUBLICATION.md).

The generated source snapshot's Go contract tests and all 26 Python test methods
pass independently, and the package still passes exact-inventory verification
after those tests. Publisher race tests and the full suite pass. This completes
the distribution tooling boundary; native codec completion and runtime/provider
qualification still gate a supported `0.1.0` release.

## Current boundary: signed instruction skills

`operatorctl skill keygen|build|import|check` and `internal/skills` now implement
explicit installation-local Ed25519 trust, bounded instruction-only ingestion,
canonical signed manifests, exact import verification and immutable explicit skill
selection. macOS ingestion allows only the automatically assigned provenance
attribute; source attributes are never copied. See [Skills](SKILLS.md).

Tests cover build/import/check, idempotent publication, key isolation, tampering,
normalization, inventory/path collisions, file/link/executable restrictions,
extended attributes and duplicate skill revisions. Campaign preparation/start
still needs to connect selection to the admitted image loader and staged inputs.
Full implementation continues with executable workflows, remaining model codecs,
administrative commands and the Attack Harness runtime.

## Current boundary: native provider transport and private profiles

`internal/modelprovider` loads private pinned profiles for OpenAI Chat/Responses,
Anthropic Messages, Bedrock Converse, Gemini Developer/Vertex, Azure OpenAI and
LiteLLM. Generation uses fixed HTTPS endpoints, host credentials, no replay or
redirects, bounded native JSON and sanitized failures. Bedrock uses AWS SDK
workload identity, endpoint resolution and SigV4 signing while preserving raw
HTTP bodies; no SDK response conversion drops native fields. See
[Model providers](MODEL_PROVIDERS.md).

The shared Go/Python model validators and host service implement the Chat,
Anthropic Messages, Bedrock Converse, Gemini GenerateContent and Responses codecs. Remaining native codecs, executable startup wiring and real provider
qualification MUST be finished before declaring every route campaign-ready.
The user approved trusted host-side skill validation/signing with a host-local
Ed25519 key; the spec now replaces the separate builder/publisher requirement.

Validation: full Go/Python suite and `go vet` pass; subsequent Bedrock raw-body
changes pass the affected provider/credential race tests. TLS tests exercise every
HTTP family, cancellation, redirects, byte limits, private-error exclusion and
native byte preservation; signed Bedrock tests use synthetic workload credentials.
No live cloud/model claim follows from those tests. Cross-platform builds are
checked separately; no Interceptor or Attack Harness files changed in this boundary.


## Current boundary: all four host secret stores

`internal/credentials` implements AWS Secrets Manager, Azure Key Vault, Google
Secret Manager and Vault, reusing Interceptor's backend/profile pattern. Host
configuration selects `credentials.file`; private parsing, approved locators,
workload identity, bounded resolution/cache, sink rotation/Proxy mode and redacted
metadata are implemented. See [Credentials](CREDENTIALS.md). Provider integration,
campaign resolution auditing and live cloud qualification remain outstanding.

Validation: full Go/Python suite, `go vet`, credential race/integration tests and
credential test builds for Linux amd64/arm64 and macOS amd64 pass. Tests use
synthetic SDK clients and a local TLS Vault server on macOS arm64. No Interceptor
files changed. Skill signing now has an approved host-local Ed25519 trust root;
the user approved trusted host-side skill parsing/validation/signing, replacing the separate builder/publisher requirement.


## Current boundary: offline bundle submission

`operatorctl submit --bundle --capabilities [--artifacts] --output` and
`operatorctl validate --run` now use the administrator-installed contract pin,
verify authoring provenance and required references, capture exact artifact
inventories, and persist reusable input snapshots. Validation rereads exact bytes;
no target/model/Docker contact or campaign authority is created. See
[Submission](SUBMISSION.md). Executable live preparation/start and service
installation remain in progress, followed by administrative reporting/retention,
all specified provider/secret-store integrations and the Attack Harness runtime.

Validation: full `make test`, `go vet ./...`, affected CLI/configuration/capability/staging race tests, and CLI builds for Linux amd64/arm64 and macOS amd64 pass. Tests execute on macOS arm64.

The user confirmed that all stated model providers and secret stores remain in
MVP scope. They initialized the separate Attack Harness Git repository.


## Completed boundary: Docker launch and live campaign worker

The [host worker](HOST_LAUNCH.md) now connects immutable input staging, local image
and release pins, exact Docker create/save/start, real bootstrap exchange, campaign
service admission/dispatch, power lifetime, event loss and terminal cleanup.
Startup and ordinary traffic are journaled; a lost start reply retains an explicit
unconfirmed outcome and its transient mounts. Confirmed shutdown removes the exact
stopped container and transport/input trees. Power leases remain held through
native cleanup and evidence collection.

The approved MVP filesystem contract uses verified 0444 files/0555 directories and
read-only nonrecursive private binds; writable tmpfs retains nodev/nosuid/noexec.
The installed startup syscall allowlist and real Python/live-filter composition
still require runtime qualification.

This completes the worker integration boundary within the host-service phase.
Executable start/preparation commands and service installation are the next boundary;
see the final section. The CLI currently remains configuration/contract checking
and independent termination. Attack Harness’s input-mount requirement was aligned
with the approved filesystem policy. That directory is not a Git repository, so
the one-line spec edit is outside Operator’s commits. Interceptor was not modified.

Validation: the full Go/Python suite and shared fixture runners, `go vet ./...`,
launch/transport/lifetime race tests and worker/bootstrap/stop integration race tests
pass. Tests use real local files with scripted Docker/native/power peers on macOS
arm64. The worker/service test binary also builds for Linux amd64/arm64 and macOS
amd64. These builds and tests do not qualify real Docker or Python confinement.

## Earlier completed phase: native evidence provenance and retained export

The native verifier now checks manifest/target identity, event and state chains,
execution records and journal binding, checkpoint/restore-source lineage,
registries, blob references, evaluation and completeness. Native exporter captures
cover complete, partial and restored archives; corrupted or inconsistent exports
are rejected before adoption.

Campaign finalization selects each verified source/replacement session, collects
bounded exports after cleanup, and records complete/partial/missing/invalid
outcomes. Docker termination remains independent. Verified archives are fsynced
into campaign-local storage before journal adoption; readers distinguish adoption
from orphaned files and recheck retained hashes. See [Evidence service](EVIDENCE_SERVICE.md).

Validation: full `make test`, `go vet ./...`, targeted evidence race tests, and
service test builds for Linux amd64/arm64 and macOS amd64 pass. Native exporter
captures and tests run on macOS arm64. Collection tests cover retained complete and
partial exports, restoration inventory, malformed/capacity/timeout failures,
corruption, failed adoption, temporary cleanup and independent Docker termination.
No sibling repository was modified; native captures were generated in a disposable
copy. Docker runtime qualification remains pending.


## Earlier completed phase: artifact, model and completion service routes

All 13 shared ordinary operations now have host service handlers. The new
[artifact](ARTIFACT_SERVICE.md), [model](MODEL_SERVICE.md) and
[completion](COMPLETION_SERVICE.md) services join the existing attempt, feedback,
cleanup and snapshot routes through one gate and durable operation namespace.

- Artifact chunks, immutable receipts and campaign reservations survive restore.
- Model requests bind exact prompt/tools/profile; usage is reserved before provider
  contact, settled from known usage, and preserved on ambiguous outcomes.
- Typed assessments resolve campaign records and feedback; assertions remain
  distinct from execution facts.
- Conclusions bind verified launch bytes and final revision through the shared
  completion validator. Stop acceptance is durable before acknowledgement.
- Finalization has a separate bounded allowance; five-second guest exit enforcement
  and immediate hard stops use the independent Docker termination path.

Validation: full `make test` (Go, 26 Python tests and shared fixture runners),
`go vet ./...`, affected-package race tests and service test builds for Linux
amd64/arm64 and macOS amd64. Tests execute on macOS arm64 with real journals/spools
and scripted native, provider and Docker peers. This completes the ordinary host service integration boundary;
production provider setup, the launcher and real runtime qualification remain open.
No shared wire schema or sibling repository change was needed.

## Earlier completed service stage: host-bound model relay

The [model service](MODEL_SERVICE.md) adds `engine.model_generate`, frozen
prompt/tool/profile validation, durable native exchanges, conservative token
reservations and known-usage settlement. Duplicate replay and healthy restore
preserve model charges. Unknown or malformed provider outcomes close execution;
provider cancellation never gates independent Docker termination.

Eleven ordinary routes are implemented. Provider transport/credential integration
is a trusted launcher dependency; tests use scripted providers. Typed assessment
and conclusion/stop handling remain the next service stage.

## Earlier completed service stage: campaign artifact uploads

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
Harness deployment. Campaign start CLI remains pending; Docker launch is now
implemented by the worker boundary described above.

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

## Next major phase: executable campaign preparation/start and host installation

Connect administrator configuration, installed contracts, local image/release
preparation, bundle/input/skill preparation and production model-provider settings
to `hostworker`. Expose the foreground/start workflow, accepted-start deduplication
and single-host campaign serialization; wire signals and systemd/LaunchAgent
installation without resuming execution from recovered journals.

Administrative evidence import/reporting/purge integration and the Attack Harness
Python dispatcher/model loop remain subsequent work. The real Python image,
startup/live confinement, Linux FIFO and Docker Desktop spool behavior still need
runtime qualification. Tests of the host worker and native Interceptor do not
qualify those components.
