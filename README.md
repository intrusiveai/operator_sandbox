# Operator Sandbox — container design

Implementation specification with shared contracts, host persistence and transport,
and independent administrative termination.
The campaign service, Docker launch/bootstrap worker, executable preparation/start,
and systemd/launchd dispatch are implemented. Startup recovery verifies residual
container identity, reclaims transient files, and performs bounded native cleanup.
Installation packaging and external runtime qualification remain pending.

The [shared validation foundation](contracts/README.md) now provides Go/Python
strict JSON decoders, offline validation of the current schema catalog and shared
conformance vectors. The [ordinary wire contract](schemas/ORDINARY_WIRE_CONTRACT.md)
adds typed exchanges and stateless validation for 13 operations, plus spool ACKs. The [assessment contract](schemas/ASSESSMENT_CONTRACT.md)
adds typed records, structured conclusions and completion-chain checks.
The [startup/manifest contract](schemas/STARTUP_MANIFEST_CONTRACT.md) adds control
messages, five-message transcript checks and bounded input/skill inventories.
The [input-content contract](schemas/ENGINE_INPUT_CONTRACT.md) adds immutable
EngineContext, prompt composition/provenance and launch input-byte validation.
The [canonical identity contract](schemas/CANONICAL_IDENTITY_CONTRACT.md) adds
matching Go/Python canonical JSON, launch digest and artifact-content checks.
The [package integrity contract](schemas/PACKAGE_INTEGRITY_CONTRACT.md) adds manifest
construction, exact payload verification and loading against a trusted package pin.
The [transport codec contract](schemas/TRANSPORT_CODEC_CONTRACT.md) adds bounded
FIFO framing, spool names, lane sequence tracking and consumption ACK checks.
The [attempt bookkeeping contract](schemas/ATTEMPT_BOOKKEEPING_CONTRACT.md) adds
campaign attempt allocation, duplicate/admission tracking and unknown-outcome closure.
The [harness loop accounting contract](schemas/HARNESS_LOOP_ACCOUNTING_CONTRACT.md)
adds finite-loop limits, read reservations, progress tracking and bounded finalization.
The [model codec contract](schemas/MODEL_CODEC_CONTRACT.md) adds typed model relay
exchanges, a pinned Chat Completions subset and correlated tool continuation checks.
The [host persistence foundation](docs/CAMPAIGN_PERSISTENCE.md) adds immutable run
manifests, exact Docker bindings, durable campaign journals and recovery inspection.
The [durable attempt layer](docs/DURABLE_ATTEMPT_ADMISSION.md) connects submission,
admission and result bookkeeping to that journal, reserves future audit capacity,
and exposes a terminal signal independent of the writer lock.
The [host transport layer](docs/HOST_TRANSPORT.md) adds physical FIFO/spool I/O,
bounded queues, startup/transfer/operation deadlines, spool size checks and cleanup
after confirmed container exit.
The [Docker termination stage](docs/DOCKER_TERMINATION.md) adds the initial
`operatorctl campaign terminate` command, exact daemon/container verification,
a terminal-fence observer and bounded emergency evidence independent of the journal.
The [image preparation stage](docs/IMAGE_PREPARATION.md) resolves native local
image IDs, validates fixed-origin HTTPS release records and supports private cached
approval with current compatibility checks.
The [host configuration stage](docs/HOST_CONFIGURATION.md) adds strict private YAML
loading, OS-specific local Docker/state defaults, `operatorctl config check` and
configuration-aware termination with explicit recovery overrides.
The [installed contract loader](docs/INSTALLED_CONTRACT.md) verifies a bounded
filesystem inventory against an independent package pin, compiles the verified
schemas offline and provides `operatorctl contract check`.
The [immutable input staging layer](docs/INPUT_STAGING.md) copies exact campaign
input/skill inventories into service-owned read-only trees, checks their bytes and
layout, and provides verification and cleanup before Docker integration.
The [native Interceptor client](docs/INTERCEPTOR_CLIENT.md) adds fixed-loopback
attachment/status, exact v1alpha2 request encoding and closure confirmation,
with bounded responses and explicit transport uncertainty. It also prepares
restore/stop lifecycle requests, validates replacement bindings and decodes the
separate lifecycle/native operation records for reconciliation without automatic
replay. Snapshot helpers carry the remaining campaign byte allowance, verify native
checkpoint integrity and provide bounded inventory pages and scoped inspection.
Evidence download helpers stream into private temporary files, enforce configured
archive limits and verify transfer length/digest before native archive validation.
Archive inspection checks native member names, framing, regular-file/size limits
and blob digests without extracting files. Native provenance checks and retained
campaign export collection are implemented by the [evidence service](docs/EVIDENCE_SERVICE.md).
The [capability admission layer](docs/CAPABILITY_ADMISSION.md) verifies native
capability hashes and delivery contracts, projects/imports public exports, and
checks bundle dependencies against live bindings and explicit host policy. It
retains authoring/live provenance, optional gaps and separate native/effective
feedback profiles.
The [durable native executor](docs/NATIVE_EXECUTION.md) connects admitted steps to
per-step journal reservations, pinned live target/Docker checks, one-time dispatch,
terminal cancellation and reporting-only reconciliation.
The [typed attempt adapter](docs/TYPED_ATTEMPT_ADAPTER.md) resolves concrete host
scopes, validates artifact bytes/lineage/input contracts, compiles deterministic
native commands and executes them through that journal with typed receipt checks.
The [feedback projection](docs/FEEDBACK_PROJECTION.md) preserves native/effective
profiles, filters permitted observations and provides bounded receipt-scoped reads.
The [durable receipts](docs/DURABLE_RECEIPTS.md) and [attempt broker](docs/ATTEMPT_BROKER.md)
retain feedback before reply, dispatch typed attempts and retained-injection cleanup,
and enforce cumulative receipt-read budgets across restores.
The [campaign preparation](docs/CAMPAIGN_PREPARATION.md) and
[service foundation](docs/CAMPAIGN_SERVICE.md) add private target profiles, frozen
inputs, verified startup admission, live source callbacks, physical spool dispatch
and independent termination with bounded post-closure cleanup.
The [snapshot service](docs/SNAPSHOT_SERVICE.md) adds cumulative checkpoint accounting,
discovery and verified restore on the same harness and transport.
The [implementation phase handoff](docs/IMPLEMENTATION_STATUS.md) describes these
completed foundations, including artifact/model/completion service routes.
The [host launch worker](docs/HOST_LAUNCH.md) adds fixed Docker policy, live bootstrap,
host power/event lifetime and confirmed container/transport cleanup.
Run `make setup` then `make test`. See [startup recovery](docs/STARTUP_RECOVERY.md)
for cleanup and uncertainty rules, including native attach failures before a
campaign journal exists. Operator reporting/export/purge, late evidence
collection, contract publication and external runtime qualification remain pending.
Attack Harness tracks its implemented runtime and remaining qualification gates
in its own repository.

Operator accepts structured objectives and optional scenarios from users or external
generators, runs a custom Python harness in a network-disabled container, brokers
its permitted operations, and retains host journals, evidence and local reports.

- [Objectives and scenarios submission](schemas/SCENARIO_BUNDLE_CONTRACT.md): public
  input fields, validation, immutable staging and user-authored examples.
- [Public capability export](schemas/CAPABILITY_EXPORT_CONTRACT.md): copyable typed
  references, compatibility-based admission, optional exact pins and tested fixtures.
- [Accepted shared host/harness contract](schemas/SHARED_CONTRACT.md): authoritative wire, startup,
  manifest and completion rules; package publication and full conformance remain pending.
- [Product specification](OPERATOR_SANDBOX_SPEC.md): requirements, interfaces,
  lifecycle, journaling and acceptance criteria.
- [Container guest contract](GUEST_CONTAINER_SPEC.md): proposed Python image,
  filesystem, Linux FIFO/macOS spool transport and startup restrictions.
- [Attack Harness specification](../attack_harness/GUEST_ARTIFACT_LAYOUT_SPEC.md):
  companion Python harness and container image design.

Interceptor provides the
[local MVP integration contract](../interceptor_sandbox/docs/local-api.md);
Operator runtime implementation follows the shared-contract foundation.

## Docker MVP deployment

Administrators install the matching Linux Attack Harness image in local Docker Engine (Linux)
or Docker Desktop (macOS) and configure
Operator's `engine.image`. Operator resolves the full local Docker image ID and
checks/caches the
[HTTPS release compatibility record](schemas/ENGINE_RELEASE_CONTRACT.md) before launch.
Linux hosts use private named FIFOs; macOS hosts use regular-file spools for
ordinary/control traffic. Input/skill/manifests stay read-only. Host support targets
x86_64 and ARM64/AArch64 on both OSes. Journaling remains mandatory; administrative
termination calls Docker directly without campaign-worker cooperation. Runtime
qualification is pending.

See [host runtime profiles](HOST_RUNTIME_PROFILES.md) for the support matrix and remaining decisions.

The MVP uses Docker mounts/permissions and two-stage seccomp. Additional filesystem
policy and separate user-namespace remapping are optional. Confirmed supported
OS/Docker versions are informational; startup checks required runtime capabilities.

Retained injections can be explicitly removed through the
[typed cleanup contract](schemas/INJECTION_CLEANUP_CONTRACT.md), including after a
healthy restore, without rolling back the target. The host broker and initial
service route and healthy campaign restore coordination are implemented.
