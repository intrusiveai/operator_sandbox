# Operator / Attack Harness / Interceptor communication review

Date: 2026-09-21. Scope: current working-tree specifications, schema files and
Interceptor implementation. External scenario-generation services are excluded.
This review changes no product requirements, runtime code or existing contracts.
At the time of this review, Operator and Attack Harness were not implemented.
The original findings and test observations below retain that historical context.

**Current status (2026-09-28):** the core Operator and Attack Harness runtimes are
implemented. C1–C5 have code/contract resolutions; complete live integration
qualification remains outstanding. See [current implementation status](../docs/IMPLEMENTATION_STATUS.md)
for the remaining workflows and release gates.

## Summary

The closure shape, feedback translation and public capability/bundle contracts are
resolved. Operator implements their native adapters, status polling and evidence
collection/import. Shared schemas/validators/fixtures are implemented. Full process
conformance and live target qualification remain outstanding; the historical
static review and component tests below do not establish an operational
end-to-end deployment.

## C1. Closure response changes shape when the target stops

**Resolution (2026-09-21):** implemented in Interceptor. Retained closure now
returns the same direct ExecutionOwner body as live closure. The regression test
`TestLiveAndRetainedClosureUseSameResponse` uses a journal-backed execution store
and the same strict decoder for live, stopped and error-session responses. It
failed before the fix and passes afterward; gatewayhost, adaptive and localapi
package tests pass. Wire documentation and the integration spec now require this
uniform response. The original finding is preserved below for context.

**Confirmed native protocol conflict.** Live `session.owner` returns the
ExecutionOwner directly as the response body. The retained-session path returns
`{"owner": <ExecutionOwner>}` instead. Outer status/revision framing is the same.
A target that stops before Operator's closure request is handled therefore changes
the expected body schema. A strict decoder using the live response shape rejects
the valid retained response and cannot consume the closure confirmation normally.
This does not reopen execution; it breaks confirmation/reporting on that path.

Evidence:

- [Live response](../../interceptor_sandbox/internal/adaptive/execution.go#L407).
- [Retained response](../../interceptor_sandbox/internal/gatewayhost/executor.go#L95).
- [Existing strict owner decoder during restore](../../interceptor_sandbox/internal/localapi/service.go#L613).

A temporary probe exercised the retained implementation and decoded it as the live
ExecutionOwner type: `json: unknown field "owner"`. The restore handler's healthy
path normally uses the live response; the probe does not claim that healthy restore
always fails.

**Recommendation:** return the same direct owner body for live and retained closure,
and add a common response fixture covering both paths, including a target exit race.

## C2. Requested feedback narrowing needs an explicit native mapping

**Resolution (2026-09-21):** the shared feedback contract now separates native and
effective profiles, defines deterministic selection translation (including empty
intersections), and requires independent field/receipt-read filtering. Operator,
Attack Harness and Interceptor integration documents agree on this mapping. All 17 shared
translation fixtures pass, covering all nine native/requested combinations plus
host/selection narrowing. Interceptor's `TestFeedback*` tests pass, including the
new diagnostic-session narrowing and retained-read regression test. No native
runtime change was needed. Operator now implements adapter projection/read
enforcement; complete live qualification remains outstanding. The original finding follows.

**Adapter contract gap, with native rejection reproduced.** The submitted bundle
can request black-box feedback against a target configured for diagnostic feedback.
Operator should honor the narrower view. However, native AttemptContext must carry
the exact immutable Interceptor session profile. Passing the resolved black-box
profile into that native field rejects `attempt.register` before invocation.
The specs allow host narrowing but do not explicitly separate these two profile
identities and the associated selection/result mapping.

Evidence:

- [Bundle profile resolution](../schemas/SCENARIO_BUNDLE_CONTRACT.md#2-top-level-fields).
- [Exact native comparison](../../interceptor_sandbox/internal/adaptive/attempts.go#L92).
- [Native profile/selection rules](../../interceptor_sandbox/docs/feedback.md#profiles-and-selection).
- [Host feedback projection](../schemas/FEEDBACK_CONTRACT.md#selection-and-profiles).

A temporary registry probe reproduced the rejection for black-box in a diagnostic
session; the same valid attempt registered when the native field was diagnostic.
This is not a defect in Interceptor's immutable-profile check.

**Recommendation:** retain a native session profile and a separate effective
harness profile. Use the native value in AttemptContext; compute explicit native
observation selection from the permitted intersection, then filter/label the host
view under its effective policy. Define empty/disallowed intersections and category
states. Test all requested/native combinations and same-campaign retained reads.
Do not restart a target merely to narrow the harness's view.

## C3. No required Operator observation loop for independent target failure

**Implementation update (2026-09-28):** the [campaign service](../docs/CAMPAIGN_SERVICE.md)
implements independent one-second native status polling and terminal fencing,
with scripted-peer tests covering status loss during blocked ordinary work.
Live target qualification remains outstanding. The original finding follows.

**Lifecycle communication gap.** Interceptor updates its local status when a native
limit, connector failure or target exit occurs. It exposes that information through
`POST /v1/status`; it does not push notifications to Operator. Operator specifies
what an error/closed status means, but not when it must obtain that status during
execution or a long model call.

An implementation that queries status only when dispatching target operations can
leave Attack Harness working on a dead target until the next call. Interceptor's own 500 ms
health check does not notify a separate process automatically.

Evidence:

- [Operator status interpretation](../OPERATOR_SANDBOX_SPEC.md#82-interceptor-integration).
- [Interceptor failure observation](../../interceptor_sandbox/docs/local-api.md#execution-failure-status).
- [Pull-only status handler](../../interceptor_sandbox/internal/localapi/service.go#L688).

**Recommendation:** specify one bounded host-side status poll, for example every
second, independent of ordinary harness/model waits. Define timeout/unreachable
behavior, terminal fencing, source-session attribution, and how delayed status
responses are reconciled with an in-progress restore. This is target lifecycle
observation, not reinstating guest syscall/process monitoring. Test target failure
while Attack Harness is waiting for a model result and during restore.

## C4. The public capability export/reference namespace is incomplete

**Resolution (2026-09-21):** published closed TargetCapabilityManifest and
ScenarioBundle schemas, catalog entries and a normative Interceptor mapping.
Source digests retain verified authoring provenance; admission checks live
compatibility by default, with an optional exact projection pin. Requirement
lists may be empty, and unavailable optional dependencies/guidance produce gaps.
The reference-chain fixture uses the native delivery-example export verified by
Interceptor's own test, its actual Go digest preimage, a public projection and a
submitted bundle. All 40 capability/bundle checks pass, including namespace,
digest, required/optional and compatible-change cases. Component docs agree.
Native production adapters and general JCS/Go/Python validators are now
implemented. Public capability-export CLI and complete live qualification remain
outstanding. The original finding follows.

**Input compatibility gap exposed by standalone bundle submission.** Bundle authors
must provide target/source identity, required capability IDs, action IDs and
capability-advertised evidence classes. The public TargetCapabilityManifest is
still descriptive: there is no closed export schema or complete native-to-public
mapping defining those IDs and their namespaces. Interceptor exports operations,
services, injection surfaces, oracle types and a native digest; that object is not
the same as Operator's public projection.

Two independent implementations can produce a structurally reasonable bundle and
export that disagree about whether a string names an application operation, setup
action or evidence capability, or which source/projection digest it binds.
The new bundle examples use placeholder identities and cannot exercise this join.

Evidence:

- [Bundle fields and references](../schemas/SCENARIO_BUNDLE_CONTRACT.md#2-top-level-fields).
- [Capability binding](../schemas/SCENARIO_BUNDLE_CONTRACT.md#4-capability-and-identity-validation).
- [Operator projection requirement](../OPERATOR_SANDBOX_SPEC.md#81-capability-driven-execution).
- [Native capability structure](../../interceptor_sandbox/internal/adaptive/capability.go#L23).

**Recommendation:** publish the public capability-export schema alongside
ScenarioBundle, with unambiguous typed/namespaced capability references, native
source identity, static projection digest and deterministic mapping rules. Include
one real Interceptor export → public projection → user bundle → validation fixture,
plus an objectives-only case and a changed-live-target rejection case.
This completes the input portion of shared contract publication; it does not call
for a new guest protocol or an execution-approval model.

## C5. Valid campaign evidence can exceed the available export channel

**Resolution (2026-09-21):** Interceptor API and CLI export now use a configurable
per-session archive limit, default 4 GiB. Attach/status advertise the API limit.
The exporter enforces the limit while writing, including tar padding/trailers;
413 responses carry the limit and confirm retained source evidence. Failed export
removes temporary output and preserves previous exports, journals and blobs.
Administrative CLI export can use a higher limit for the same retained session.
Operator's spec defines a separate configurable evidence acceptance limit,
streamed size/hash verification, explicit collection gaps and later retained import.
The example native client now streams/verifies downloads instead of buffering them.
Evidence/localapi/hostrun/CLI package tests and example-client tests pass, including
an archive above the old ceiling, exact boundaries, cleanup and successful later
export. Tests use native fixtures/fakes, not a Docker campaign. Operator now implements runtime collection, explicit late collection and retained
archive import; complete live target qualification remains outstanding. The original finding follows for context.

**Capacity gap to decide, not a malformed-response bug.** Interceptor's single
`/v1/evidence` archive has a hard 256 MiB ceiling. Above that size the API returns
413 before streaming; requesting a range cannot bypass that initial check. The
export includes journal-referenced blobs, registered artifacts and retained file
versions. Neither ordinary native storage nor Operator's 1 GiB campaign artifact
and snapshot allowances guarantees that the resulting archive stays under 256 MiB.
For example, distinct retained payload/file versions can cross the archive ceiling
while each individual object and the campaign remain within their limits.

Evidence:

- [Whole-archive rejection](../../interceptor_sandbox/internal/localapi/service.go#L732).
- [Archive content inventory](../../interceptor_sandbox/cmd/interceptor/main.go#L706).
- [Operator campaign limits](../OPERATOR_SANDBOX_SPEC.md#4-container-execution-baseline).
- [Stopped-session evidence contract](../../interceptor_sandbox/docs/local-api.md#stop-and-evidence).

A temporary handler probe confirmed that a 256 MiB + 1 byte export returns
`413 evidence_limit_exceeded`. The probe used a sparse temporary file to exercise
only the transfer-size branch; it was not a Docker campaign qualification test.

The data can remain available for administrator CLI export, and current specs
permit explicitly incomplete reporting. Thus this does not stop all campaigns;
it prevents automatic evidence delivery for otherwise admitted larger runs.

**Recommendation:** choose an explicit supported campaign/export capacity policy.
For an MVP, make the total export ceiling configurable and state how it relates to
admitted campaign storage; keep streamed bounded reads and disk checks. Treat
protected native archives separately from the 16 MiB guest-artifact limit, and
surface a specific evidence-capacity outcome instead of a generic retryable failure.

## Prerequisites recorded at the original review

- Complete the shared package: envelope/control/startup, manifests, ScenarioBundle,
  capability projection, registry, conclusion/stop schemas, Go/Python validators and
  shared fixtures. Existing operation schemas are only part of the package.
- Run Operator/Attack Harness runtime feasibility tests during implementation: both non-network
  transports, actual Python bootstrap, confinement and independent Docker termination.
- Qualify the real adapter transformations; native responses intentionally are not
  forwarded unchanged as guest responses. Keep native body-byte digests separate
  from Operator canonical object digests and native state revision separate from
  campaign run revision.

## Checks performed and limits

Passed current Interceptor package tests for `internal/adaptive`, `internal/localapi`,
`internal/gatewayhost`, `internal/bridge` and `internal/sandboxd`. Supervisor tests
initially could not bind Unix sockets inside the execution sandbox; their approved
rerun outside it passed. Added diagnostic probes only to a temporary repository copy
under `/tmp/interceptor-communications-review-20260921`; original code was not edited.

Static comparison covered Linux FIFO direction/rendezvous, macOS spool names/ACKs/
queues, five-message startup, operation/call IDs, deadlines, feedback byte reads,
restore revision adoption and skipped model calls, retained injection cleanup,
snapshot allowance/metadata, campaign completion and native lifecycle routing.
Existing native-parent-after-rollback/new-root rules are already documented and are
not reported as a new gap. No Docker or complete Operator–Attack Harness end-to-end run was
performed during the original review; those components were not yet implemented
at that time. Their current status is recorded above.
