# Attack Harness integration

Operator owns the shared contract. A Harness image must use the same package
version and digest as its Operator installation. The source release's
[contract lock](../release/contract-lock.json) identifies the exact package and
source revision; `operatorctl version` exposes the installed supported identity.
The [shared contract](../schemas/SHARED_CONTRACT.md) defines the wire protocol,
semantic rules and common Go/Python fixtures.

## Package and image compatibility

Build the package from the immutable source revision in the release lock using
the [package build and check commands](CONTRACT_PUBLICATION.md). Verify its version,
digest, catalog, registry and complete inventory against that lock. Harness uses
the matching Python validators and provider tool projections from this package.
Do not independently reimplement their rules or select validators from guest data.

The local Docker image contains
`/opt/operator/engine/share/compatibility.json`, an immutable regular UTF-8 JSON
file of at most 64 KiB. Its fields are:

| Field | Value |
|---|---|
| `api_version` | `operator.dev/harness-compatibility/v1alpha1` |
| `minimum_operator_version` | Stable SemVer required by the image |
| `contract_package_version` | Exact package version from the release lock |
| `contract_package_digest` | Exact package digest from the release lock |
| `runtime_profile` | `operator-container/v1` |
| `platform` | `linux/amd64` or `linux/arm64`, matching the image |

Duplicate or additional fields are rejected. The compatibility file must agree
with `engine-manifest.json` and the actual embedded contract, prompt, skill-loader
and tool-catalog bytes. It must not include its own digest or an image digest.
Operator independently pins the local Docker identity and reads the files through
a non-running inspection container. No publisher verification, registry access
or external compatibility lookup is performed.

`release_record_digest` is the raw SHA-256 of the compatibility file, including
whitespace. Preserve it from the frozen EngineContext when forming
`generator.release_digest`; do not substitute the image digest or hash reserialized
JSON. See [image preparation](IMAGE_PREPARATION.md) and the
[image compatibility contract](../schemas/ENGINE_RELEASE_CONTRACT.md).

## Startup and transport

Use the [container ABI](../GUEST_CONTAINER_SPEC.md) and
[startup contract](../schemas/STARTUP_MANIFEST_CONTRACT.md). Linux uses named FIFOs;
macOS uses regular-file spooling. Validate bootstrap identity, complete the
startup exchange, and consume verified read-only input and skill manifests before
ordinary requests. Sequencing, acknowledgements, deadlines, bounds and terminal
behavior follow the [transport contract](../schemas/TRANSPORT_CODEC_CONTRACT.md).
Do not negotiate an alternate contract or resume an interrupted campaign.

## Ordinary campaign workflow

1. Submit `engine.attempt_execute` with explicit `origin`, target `operation_id`
   and exactly one direct input or committed artifact receipt. Scenario origin
   requires a submitted-bundle `scenario_id`; exploratory origin forbids that
   field. Never infer or repair an omitted origin.
2. Inspect permitted feedback using the execution receipt and entry IDs returned
   by Operator. A rejected submission has no execution receipt. Retain receipt
   categories from runtime state rather than guessing from identifier prefixes.
3. Submit `engine.finish` with a finish reason and a bounded assessment, an
   assessment-draft artifact receipt, or an explicit assessment-unavailable reason.
   A minimal assessment contains a summary and objective outcomes. Hypotheses and
   historical records are optional; local hypotheses need no preregistration.

The [ordinary campaign contract](../schemas/SIMPLE_CAMPAIGN_CONTRACT.md) defines
precise fields, bounds and defaults. Small direct input does not require artifact
publication. Operator retains input before dispatch, assigns attempt/lineage
bookkeeping and validates capabilities, budgets, receipts and evidence visibility.
Omitted advanced options resolve only to their documented defaults.

The Harness owns outer request/call identities and transport sequencing.
A corrected submission uses a new operation identity. Repeated delivery of an
identical operation preserves its identity; uncertain effects never authorize
replay. Optional injection, snapshot, restore and historical-record operations use
the same registry. A successful restore preserves Harness context and skips
remaining calls in that model-response batch before adopting the new revision.

## Guidance and diagnostics

Trusted Harness guidance and tool descriptions explain direct input, explicit
origin, returned handle categories, evidence inspection and honest assessment.
Use the shared provider projections and validate the complete runtime arguments;
provider schema limitations do not remove semantic checks.

Reject duplicate JSON keys before decoding discards them. Local rejection uses
the existing operation-error format with `INVALID_ARGUMENTS`, `effect_state: none`,
`correct-and-resubmit`, and a `harness-local` diagnostic containing the failed rule,
field path and correction. Host rejection preserves the returned code and
diagnostic. Do not invent receipts, digests, evidence or successful outcomes.

For a stopping condition, use runtime-only `engine.diagnostic_report` while
transport is available and finish has not been accepted. Include the actual
condition, observed/limit counts and last rejected tool/call where applicable.
Operator retains selected safe facts and a digest of the report. No credentials,
unrestricted prompts or provider bodies belong in diagnostics.

## Finish and exit

Accepted finish closes admission and returns a durable stop receipt with
finalization pending. Send no further ordinary operations and exit within five
seconds. Expected transport closure must not turn healthy unwinding into a fatal
error. A corrected accepted assessment supersedes a rejection for completion
purposes; the rejection remains history.

Operator observes the exact container's exit status before removal. Accepted
finish plus exit 0 establishes successful Harness execution; it does not prove
objective achievement. Nonzero, unconfirmed or forced exits and cleanup failures
remain distinct. See [exit handling](HARNESS_EXIT.md).

Before a nonzero exit, emit a bounded, credential-safe causal diagnostic to stderr
and flush it. Preserve the original error through close/cleanup failures. Do not
print unrestricted exceptions, tracebacks, prompts, bodies, URLs or credentials.
Use stderr for failures before bootstrap or after finish acceptance, when an
ordinary diagnostic operation is unavailable. Operator retains bounded sanitized
stderr before removal; it cannot reconstruct an error that Harness never emitted.

## Integration checks

Run the shared Go/Python package fixtures and
[`make test-integration`](PROCESS_INTEGRATION.md) against matching source.
Exercise explicit scenario/exploratory origins, actionable correction without
duplicate dispatch, direct/artifact inputs, observation reads, local hypotheses,
accepted finish, exit 0, fatal exits and cleanup. Verify separate inspection and
export after container removal. Controlled process tests establish protocol
behavior; actual-image containment and live-provider checks test different facts.
