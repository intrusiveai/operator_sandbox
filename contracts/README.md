# Shared validation foundation

This is the validation and protocol foundation of `operator-contracts`. It validates the
56 schemas currently listed in [the catalog](../schemas/catalog.json) in Go and
Python. It is a development library, not the published `0.1.0` contract package.
The authoritative design remains [SHARED_CONTRACT.md](../schemas/SHARED_CONTRACT.md).

## Implemented

- Strict UTF-8 JSON decoding with duplicate-key, trailing-value, malformed Unicode,
  unsafe-number and nesting checks before structural validation.
- Caller-selected byte ceilings, with constants for 4 MiB ordinary messages and
  64 KiB control messages. Callers must bound transport reads before constructing
  the byte buffer passed to these functions.
- Offline Draft 2020-12 validation against an explicit installed schema catalog.
  Missing resources, unknown schema IDs and references outside the catalog fail.
- Go-embedded schemas and generated Go/Python schema-ID constants.
- One set of 154 accepted/rejected byte vectors used by both language runners,
  plus tests for unavailable external references and invalid catalog inventories.
- Typed ordinary envelopes generated from a 13-operation registry, spool ACK
  validation, and 182 shared protocol cases covering request/result correlation,
  artifact chunks, snapshot metadata, restore transitions, typed records, errors and graceful stop.
- Structured conclusions and completion-chain consistency checks, with 74 shared
  cases covering bindings, exact artifact bytes, record/stop receipts and explicit gaps.
- Startup/control schemas, transcript consistency and input/skill inventories,
  with 102 shared cases covering startup bindings, manifest bytes, portable paths
  and declared resource limits.
- Immutable EngineContext and prompt provenance, exact prompt composition and
  launch input-byte checks, with 90 shared cases.
- Canonical JSON/raw digest helpers, canonical launch identity checks and artifact
  content verification, with 306 canonicalization and 21 identity cases.
- Package manifest construction, exact payload verification and loading from frozen
  verified bytes, with 39 shared cases and real-catalog loader tests.
- Incremental FIFO framing, spool names/lane validation, launch-wide sequence
  tracking and cumulative consumption ACKs, with 73 shared cases.
- Campaign-wide attempt allocation and an in-memory admission ledger, with 40
  shared traces covering rejection, duplicates, gaps, quotas and unknown outcomes.
- Harness-loop configuration resolution and campaign accounting, read reservation
  and novelty tracking, restore batch skipping and bounded finalization, with 129
  shared cases.
- Typed model relay, a pinned native Chat Completions text/function codec, trusted
  profile binding, startup codec settings and complete conversation/tool continuations,
  with 102 shared cases. The full total is now 1,312 shared cases; the catalog has
  56 entries.

The [ordinary wire contract](../schemas/ORDINARY_WIRE_CONTRACT.md) documents the
exact implemented fields and distinguishes stateless validation from runtime gates.

Schemas use portable `\xHH` regex escapes for control characters. Constrained
single-line fields also reject CR/LF explicitly, avoiding differences in how
regex engines interpret a final `$` anchor. IDs, digests and path fields therefore
reject a trailing newline in both languages.

## Development

Use Go 1.26 and Python 3.12 or later. This stage was tested with Go 1.26.5 and
Python 3.14.6 on macOS ARM64; it does not qualify container/runtime support.

```sh
make setup
make test
```

`make setup` creates `.venv` and downloads dependencies. `make test` checks generated
schema IDs and Unicode path tables, runs the Go/Python shared fixture suites, and runs the existing cleanup, feedback
and capability fixture checks. Python runtime dependencies are pinned in
`python/requirements.lock`; Go dependencies are recorded in `go.mod`/`go.sum`.

After changing `schemas/catalog.json` or `schemas/operations.json`, run `make generate`. Both languages use
the catalog as the authority for schema identity; generated constants are a
convenience, not a second registry.

## Go usage

```go
catalog, err := contracts.LoadCatalog(schemas.Files)
if err != nil {
    return err
}
value, err := catalog.Validate(contracts.EngineObservationReadRequestSchema,
    rawBytes, contracts.OrdinaryLimit)
```

Import `contracts` and `schemas` from `github.com/intrusive-ai/operator-sandbox`.
Numbers in returned objects are `json.Number`; validate before converting them
to application types. Public errors are bounded categories, without submitted
values or native validator diagnostics.

## Python usage

Until package publication, run with `PYTHONPATH=contracts/python` and explicitly
provide the trusted local schema directory:

```python
from pathlib import Path
from operator_contracts import Catalog, ORDINARY_LIMIT
from operator_contracts.schema_ids import ENGINE_OBSERVATION_READ_REQUEST_SCHEMA

catalog = Catalog(Path("schemas"))
value = catalog.validate(ENGINE_OBSERVATION_READ_REQUEST_SCHEMA, raw_bytes, ORDINARY_LIMIT)
```

Decoded numbers retain exact values (`int` for exact integers and `Decimal` for
fractions, with zero normalized to integer zero). This prevents near-integer fractions from satisfying integer
schemas through floating-point rounding. Both decoders reject nonfinite values,
nonzero values that underflow binary64, and integer-valued binary64 numbers beyond
the safe integer range. Neither decoder performs canonicalization. Do not serialize
these values with a generic JSON encoder and assume the result is `jcs-v1`.
Use `Canonicalize` / `canonicalize` on original bytes after the relevant schema
checks; the helper applies the shared binary64/UTF-16 serialization rules.

For ordinary exchanges, use `contracts.LoadProtocol(schemas.Files)` in Go or
`Protocol(Path("schemas"))` in Python. Validate request bytes with `ValidateRequest`
or `validate_request`; validate correlated replies by passing request and response
bytes to `ValidateResponse` or `validate_response`. These entry points enforce
the additional consistency rules described in the ordinary wire contract. The
lower-level catalog API performs structural validation only.

Use `ValidateConclusion` / `validate_conclusion` for conclusion content and
`ValidateCompletion` / `validate_completion` for a retained artifact/record/stop
chain with the host's expected binding. See [the assessment contract](../schemas/ASSESSMENT_CONTRACT.md)
for fields, byte limits and the caller's required trusted receipt lookups. These
helpers do not persist records or accept stop.

Use `ValidateControl` / `validate_control` for individual control messages,
`ValidateStartup` / `validate_startup` for a successful five-message transcript,
and `ValidateStartupInputs` / `validate_startup_inputs` to check that transcript
against the complete input and skill manifests. Inventory validators are also
available separately. See [the startup/manifest contract](../schemas/STARTUP_MANIFEST_CONTRACT.md)
for fixed descriptor paths, the shared Unicode 15.0 path profile, bounds and
the distinction between checking inventory metadata and verifying actual files.

Use `ValidateEngineContext` / `validate_engine_context` for the launch context,
`ComposePrompt` / `inputs.compose_prompt` for exact frozen-source composition,
and `ValidateLaunchContent` / `validate_launch_content` to link startup and
manifests to actual context, bundle and effective prompt bytes. See
[the input contract](../schemas/ENGINE_INPUT_CONTRACT.md) for provenance fields,
reference/omission rules, safe model metadata and remaining runtime checks.

Use `ValidateLaunchIdentities` / `validate_launch_identities` to extend launch-content
checks with canonical manifest/context/skill/capability identities and installed
catalog/registry pins. `ValidateArtifactContent` / `validate_artifact_content` checks
complete upload bytes against their original artifact-begin request, including
already-canonical `jcs-v1` content. See the
[canonical identity contract](../schemas/CANONICAL_IDENTITY_CONTRACT.md) for exact
preimages, helper APIs, fixture regeneration and the remaining trust checks.

Use `BuildPackageManifest` / `build_package_manifest` to inventory explicit frozen
payloads and `VerifyPackage` / `verify_package` to check them against a trusted
expected version/digest. `LoadVerifiedProtocol` / `load_verified_protocol` verifies
before loading the catalog/registry and retains the package pin for launch checks.
See [package integrity](../schemas/PACKAGE_INTEGRITY_CONTRACT.md) for bounds, the
fixed payload layout and the distinction between byte integrity and release readiness.

Use `EncodeFrame` / `encode_frame` and `NewFrameDecoder` / `new_frame_decoder` for
bounded FIFO messages. `NewTransportState` / `new_transport_state` tracks lane
direction, campaign/launch sequences and consumption ACK positions across restores.
See the [transport codec contract](../schemas/TRANSPORT_CODEC_CONTRACT.md) for
spool filename helpers, API use and the required queue/deadline/runtime integration.

Use `NewAttemptAllocator` / `attempts.AttemptAllocator` for new dispatched attempt
numbers and `NewAttemptLedger` / `attempts.AttemptLedger` for submission, duplicate,
admission and result transitions. See the [attempt bookkeeping contract](../schemas/ATTEMPT_BOOKKEEPING_CONTRACT.md)
before integration: rejected allocations must be retained, duplicate results
never permit execution, and host transitions require durable journaling before
responses or native effects. These helpers perform no persistence or native calls.

Use `ResolveHarnessLimits` / `loop.resolve_harness_limits` to resolve administrator
defaults and policy caps, then `NewHarnessLoop` / `loop.HarnessLoop` for campaign
model/tool/read/progress accounting and its one-time finalization budget. See the
[loop accounting contract](../schemas/HARNESS_LOOP_ACCOUNTING_CONTRACT.md) for
event ordering, trusted progress sources and required runtime timer/admission gates.

Use `ModelPolicyFromContext` / `model_policy_from_context` to derive the same safe
policy from verified startup codec settings, frozen prompt bytes and a pinned
native tool projection. Use `ValidateModelRequest` / `validate_model_request` and `ValidateModelExchange` /
`validate_model_exchange` to bind native model bodies to an independently selected
host policy, in addition to ordinary envelope validation. `ModelDisposition` /
`model_disposition` classifies complete native outcomes; `ChatContinuation` /
`chat_continuation` builds a correlated assistant/tool segment. See the
[model codec contract](../schemas/MODEL_CODEC_CONTRACT.md) for required runtime
receipt checks, preserved argument strings and provider qualification limits.

## Remaining stages

Structural validation is one gate. Receipt lookup, operation identity, digests,
cross-field constraints, authorization, profile filtering and lifecycle rules
still require semantic validators. Schema `format` annotations are not a substitute
for those checks. This library is not yet sufficient to admit campaign execution.

Before publishing `0.1.0`, qualify the first native model route and package its
codec/tool projections; complete the operation registry,
native identity verification, typed message bindings, distribution/install tooling
and the full shared conformance suite. Package manifest/digest verification is
implemented, but publication remains gated on that work. Message framing and
sequence/ACK checks are implemented. The separate
[host transport layer](../docs/HOST_TRANSPORT.md) implements physical FIFO/spool
I/O, bounded queues and transport/startup/operation/campaign timers. Python peer
I/O and full fake-broker/fake-harness integration tests remain outstanding. No changes to
Interceptor's native API are needed for this foundation.
Attempt bookkeeping and finite-loop/progress/finalization accounting have shared
semantic traces. The separate [host persistence foundation](../docs/CAMPAIGN_PERSISTENCE.md)
now defines the host-private RunManifest and implements durable journal writes and
recovery inspection. The [durable attempt layer](../docs/DURABLE_ATTEMPT_ADMISSION.md)
connects attempt counters/results to that journal with audit-capacity reservations,
free-space checks and a terminal fence. Native dispatch, model/other-operation
accounting, emergency termination recording, event-source verification and
progress/finalization timer integration remain outstanding.
