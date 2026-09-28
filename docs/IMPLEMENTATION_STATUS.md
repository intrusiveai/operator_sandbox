# Current implementation status

Updated: 2026-09-28, following campaign retirement and purge.

Operator's core campaign runtime and Attack Harness's Python runtime are
implemented. Remaining administrative workflows, the HTTPS target adapter,
installation/release work and complete integration/native qualification are still
outstanding. This page distinguishes implemented components from qualified releases;
requirements in the product spec remain mandatory even when their acceptance
criteria have not yet been demonstrated.

## Completed implementation

| Area | Completed scope |
|---|---|
| Shared contracts | Closed schemas, offline catalog, 13-operation registry, strict JSON/JCS, Go/Python validators, common fixtures, startup/manifest/identity/deadline rules and reproducible package build/check tools. |
| Inputs and preparation | Native capability verification/projection, compatible ScenarioBundle admission, offline submit/validate, immutable inputs, prompt construction, signed instruction-only skills and repeatable `--skill` selection. |
| Image and launch | Local Docker image selection, fixed HTTPS compatibility lookup/cache, stopped-image embedded-file inspection, pinned launch identity, private staging, Docker confinement settings and startup verification. |
| Transport and persistence | Linux FIFOs, macOS file spools, framing/ACKs, bounded queues, deadlines and spool size checks; durable journals, reservations, cumulative accounting, duplicate protection and terminal fencing. |
| Native campaign service | All 13 ordinary routes: artifact upload, model relay, attempt execution, bounded feedback reads, injection cleanup, state/snapshots, assessments, conclusions and stop acknowledgement. |
| Restore and harness loop | Healthy restores preserve the harness and its context, adopt the replacement native session/revision, retain cumulative charges and skip remaining calls in the old model-response batch. Attempt numbering and finite-loop/finalization rules are integrated. |
| Providers and secrets | Five codec families: Chat Completions, OpenAI Responses, Anthropic Messages, Bedrock Converse and Gemini GenerateContent. Host transports cover OpenAI, Azure OpenAI, LiteLLM, Anthropic, Bedrock and Gemini Developer/Vertex. Credential backends cover AWS Secrets Manager, Azure Key Vault, Google Secret Manager and Vault. |
| Supervised execution | Prepare/start/status/logs/wait, durable start requests/run links, permanent worker claims, systemd/LaunchAgent submission, signal handling and macOS caffeinate/lifetime handling. |
| Termination and recovery | Worker-independent termination using exact Docker identity; startup reconciliation for lost create replies, abandoned transient resources, native finalization and early attach failures. Recovery performs cleanup/reporting, never campaign resumption. |
| Retained evidence | Live finalization, explicit late collection with recorded retries, verified archive reuse and offline archive import with immutable adoption records. |
| Reports and exports | Deterministic, digest-bound report/export generations from verified journal prefixes and retained evidence; explicit coverage/uncertainty, portable exports and automatic worker reporting after execution completion. |
| Campaign retirement/purge | Durable start retirement, exact service deregistration, locked inventories, per-campaign/all-campaign deletion, managed-copy registration and retry after partial filesystem deletion. Independent exports and reusable installation data are preserved. |
| Attack Harness | Python bootstrap/input loading, both transports, all five codecs, dispatch/loop accounting, artifacts, feedback, restore continuation and bounded completion. Both architecture image candidates have been built; they are not approved releases. |

Component guides provide the detailed behavior and validation boundaries:

- [Contracts](../contracts/README.md), [package tooling](CONTRACT_PUBLICATION.md)
  and [shared wire contract](../schemas/SHARED_CONTRACT.md).
- [Campaign start](CAMPAIGN_START.md), [launch](HOST_LAUNCH.md),
  [campaign service](CAMPAIGN_SERVICE.md) and [startup recovery](STARTUP_RECOVERY.md).
- [Providers](MODEL_PROVIDERS.md), [credentials](CREDENTIALS.md) and [skills](SKILLS.md).
- [Evidence service](EVIDENCE_SERVICE.md), [late collection](LATE_EVIDENCE.md),
  [offline import](EVIDENCE_IMPORT.md) and [reporting/export](REPORTING.md) and [campaign purge](PURGE.md).
- [Attack Harness implementation status](../../attack_harness/IMPLEMENTATION_STATUS.md)
  and [acceptance matrix](../../attack_harness/ACCEPTANCE_MATRIX.md).

## Validation completed and its limits

At the latest implementation boundary, the full Go suite, focused
purge/start-request/supervisor/persistence/CLI race tests, affected-package vet and Linux
AMD64/ARM64 CLI builds passed. Shared Go/Python fixtures and Attack Harness
component tests have passed in their implementation stages. Tests use real
journals, physical transports, native archive fixtures and scripted peers.

A native macOS LaunchAgent retirement test also passed: it registered an unstarted
job, confirmed its removal and rejected a delayed real worker invocation.
Selected macOS ARM64 probes have exercised LaunchAgent execution and two-stage
confinement with a test image. These are limited evidence: cross-compilation,
component tests, candidate image builds and test-image probes do not qualify the
production Operator/Attack Harness combination on all four supported host tuples.
No complete live provider, secret-store or target qualification is claimed.

## Remaining implementation stages

1. **Remaining administrative workflows.** Public capability export and
   environment/target-profile selection are implemented. Complete combined `operatorctl run`, read-only
   inspect/doctor workflows, frozen `--skill-set` selection, revocation handling
   and credential-resolution lifecycle audit.
2. **Declarative HTTPS target adapter.** Implement its constrained mapping,
   execution and evidence/report integration. Current native execution uses
   Interceptor; the HTTPS adapter remains a separate requirement.
3. **Complete Operator/Attack Harness integration tests.** Add the complete real
   Go/Python process exchange matrix over both transports, including startup,
   attempt/feedback, restore, conclusion/stop and failure/capacity cases. Preserve
   component tests as supporting evidence.
4. **Installation and release tooling.** Complete host installation/distribution,
   supported-package/build metadata, signed releases/update handling and release
   automation. Existing package build/check commands do not publish an approved
   `operator-contracts` 0.1.0 release or approve an image.

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

The next major stage is the remaining administrative workflows listed above. Detailed
historical commit boundaries remain available in Git history.
