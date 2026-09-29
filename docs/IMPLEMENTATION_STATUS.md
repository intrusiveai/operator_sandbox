# Current implementation status

Updated: 2026-09-28, following the installation and release-tooling stage.

Operator's core campaign runtime and Attack Harness's Python runtime are
implemented. Administrator workflows and the declarative HTTPS target adapter are
complete. Deterministic Go/Python process integration is covered over both transports.
Installation, explicit updates and release build/signing tooling are implemented.
Approved publication and external native qualification remain outstanding.
This page distinguishes implemented components from qualified releases;
requirements in the product spec remain mandatory even when their acceptance
criteria have not yet been demonstrated.

## Completed implementation

| Area | Completed scope |
|---|---|
| Shared contracts | Closed schemas, offline catalog, 13-operation registry, strict JSON/JCS, Go/Python validators, common fixtures, startup/manifest/identity/deadline rules and reproducible package build/check tools. |
| Inputs and preparation | Native capability verification/projection, compatible ScenarioBundle admission, offline submit/validate, immutable inputs, prompt construction, validated instruction-only skills and repeatable `--skill` selection. |
| Image and launch | Local Docker image selection, fixed HTTPS compatibility lookup/cache, stopped-image embedded-file inspection, pinned launch identity, private staging, Docker confinement settings and startup verification. |
| Transport and persistence | Linux FIFOs, macOS file spools, framing/ACKs, bounded queues, deadlines and spool size checks; durable journals, reservations, cumulative accounting, duplicate protection and terminal fencing. |
| Native campaign service | All 13 ordinary routes: artifact upload, model relay, attempt execution, bounded feedback reads, injection cleanup, state/snapshots, assessments, conclusions and stop acknowledgement. |
| Declarative HTTPS targets | Administrator-owned mappings, public capability projection, fixed destinations with DNS/IP and TLS checks, audited credentials, bounded JSON/text requests/responses, campaign lineage and duplicate suppression, filtered feedback, local-only recovery and observer-assurance reports. |
| Restore and harness loop | Healthy restores preserve the harness and its context, adopt the replacement native session/revision, retain cumulative charges and skip remaining calls in the old model-response batch. Attempt numbering and finite-loop/finalization rules are integrated. |
| Providers and secrets | Five codec families: Chat Completions, OpenAI Responses, Anthropic Messages, Bedrock Converse and Gemini GenerateContent. Host transports cover OpenAI, Azure OpenAI, LiteLLM, Anthropic, Bedrock and Gemini Developer/Vertex. Credential backends cover AWS Secrets Manager, Azure Key Vault, Google Secret Manager and Vault. |
| Supervised execution | Prepare/start/status/logs/wait, durable start requests/run links, permanent worker claims, systemd/LaunchAgent submission, signal handling and macOS caffeinate/lifetime handling. |
| Termination and recovery | Worker-independent termination using exact Docker identity; startup reconciliation for lost create replies, abandoned transient resources, native finalization and early attach failures. Recovery performs cleanup/reporting, never campaign resumption. |
| Retained evidence | Live finalization, explicit late collection with recorded retries, verified archive reuse and offline archive import with immutable adoption records. |
| Reports and exports | Deterministic, digest-bound report/export generations from verified journal prefixes and retained evidence; explicit coverage/uncertainty, portable exports and automatic worker reporting after execution completion. |
| Administrator workflows | Public capability export, environment/private-profile selectors, combined run with saved-request reuse, read-only inspect/doctor, frozen skill-set creation/selection, reversible skill removal and campaign credential-resolution audit. |
| Campaign retirement/purge | Durable start retirement, exact service deregistration, locked inventories, per-campaign/all-campaign deletion, managed-copy registration and retry after partial filesystem deletion. Independent exports and reusable installation data are preserved. |
| Installation/releases | Signed local archives, immutable versioned installations, Linux service-account provisioning, atomic activation and retries, explicit downgrades, retained configuration/evidence, compiled supported-contract pins, four-platform reproducible builds, SPDX/provenance and draft-publication preflight. |
| Process integration | Production Python entrypoint joined to the Go host over FIFO/spool: large startup inventories, prompt/skill selection, all campaign modes, HTTPS feedback, conclusions/stop, restore lineage and retained injection cleanup, process loss, spool pressure and large-history compaction. |
| Attack Harness | Python bootstrap/input loading, both transports, all five codecs, dispatch/loop accounting, artifacts, feedback, restore continuation and bounded completion. Both architecture image candidates have been built; they are not approved releases. |

Component guides provide the detailed behavior and validation boundaries:

- [Contracts](../contracts/README.md), [package tooling](CONTRACT_PUBLICATION.md)
  and [shared wire contract](../schemas/SHARED_CONTRACT.md).
- [Campaign start](CAMPAIGN_START.md), [launch](HOST_LAUNCH.md),
  [campaign service](CAMPAIGN_SERVICE.md) and [startup recovery](STARTUP_RECOVERY.md).
- [Administrator run/diagnostics](ADMIN_WORKFLOWS.md) and [submission selectors](SUBMISSION.md).
- [Host distribution and installation](HOST_DISTRIBUTION.md).
- [Go/Python process integration](PROCESS_INTEGRATION.md).
- [HTTPS targets](HTTPS_TARGETS.md) and [example private profile](../examples/https-target-profile.json).
- [Providers](MODEL_PROVIDERS.md), [credentials](CREDENTIALS.md) and [skills](SKILLS.md).
- [Evidence service](EVIDENCE_SERVICE.md), [late collection](LATE_EVIDENCE.md),
  [offline import](EVIDENCE_IMPORT.md) and [reporting/export](REPORTING.md) and [campaign purge](PURGE.md).
- [Attack Harness implementation status](../../attack_harness/IMPLEMENTATION_STATUS.md)
  and [acceptance matrix](../../attack_harness/ACCEPTANCE_MATRIX.md).

## Validation completed and its limits

The installation stage passed the full Go suite/vet, focused race tests and real
macOS ARM64 signed-package installation/update tests. All four host platforms built
twice with identical payloads. Generated contract packages passed both language
suites. [Release validation](RELEASE_VALIDATION.md) records exact source/digests and
distinguishes local installation tests from outstanding native qualification.


At the preceding process-integration boundary, the full Go suite passed with the optional Python process and
transport tests enabled. Attack Harness's 82 tests, the shared Python suite's 28
tests, repository-wide Go vet, schema generator checks and shared fixtures passed.
Focused race tests covered restore/failure handling, history compaction and the
Python FIFO/spool peers. Previous HTTPS-stage checks also built the CLI for Linux
AMD64/ARM64 and macOS AMD64/ARM64.

Process tests use the production Python entrypoint and Go service, real journals,
physical transports and a local TLS target. Models, native Interceptor, Docker and
image approval are controlled fixtures; a test-only adapter relocates Python's
fixed paths and replaces confinement installation. These tests do not qualify a
production image. The [integration guide](PROCESS_INTEGRATION.md) describes the
boundaries; Attack Harness's acceptance matrix records matching source/package pins.

A native macOS LaunchAgent retirement test also passed: it registered an unstarted
job, confirmed its removal and rejected a delayed real worker invocation.
Selected macOS ARM64 probes have exercised LaunchAgent execution and two-stage
confinement with a test image. These are limited evidence: cross-compilation,
component tests, candidate image builds and test-image probes do not qualify the
production Operator/Attack Harness combination on all four supported host tuples.
No complete live provider, secret-store or target qualification is claimed.

## Implementation stage boundary

The planned core implementation stages are complete. The next stage is native/live
qualification and approved release publication. Failures discovered during that
qualification still require correction before declaring the product release-ready.

## Remaining external qualification and publication

- Exercise every advertised provider route and all four secret stores with live
  infrastructure, credentials and failure cases.
- Qualify production images on native Linux x86_64/AArch64 and macOS x86_64/ARM64:
  confinement, transport, service lifetime, quotas and independent termination.
- Qualify real Interceptor and HTTPS target workflows, evidence, restore and
  interruption behavior against the complete acceptance criteria.
- Complete final acceptance review and publish approved contract, executable and
  image releases with compatibility records. See the
  [host runtime gates](../HOST_RUNTIME_PROFILES.md) and
  [product acceptance criteria](../OPERATOR_SANDBOX_SPEC.md#15-acceptance-criteria).

The next major stage is external qualification and approved publication. Detailed
historical commit boundaries remain available in Git history.
