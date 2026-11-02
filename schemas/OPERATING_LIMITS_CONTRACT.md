# Host-configured operating limits

Operator MUST freeze `operation_timeouts_ms` and `size_limits` in the verified
EngineContext before admission. Attack Harness MUST consume those values. Missing,
unknown, noninteger, nonpositive, or unsupported values MUST fail validation;
neither product may substitute defaults for missing wire fields. Operator applies
its installation defaults before constructing EngineContext. The existing exact
contract version/digest compatibility checks bind these semantics.

## Ordinary-operation waits

`operation_timeouts_ms` MUST have exactly one entry for each advertised operation.
The operation registry defines operations and framing, not operating durations.
Harness MUST use the supplied wait unless its caller explicitly requests a shorter
one. Operator MUST clamp execution to that wait, the submitted request deadline,
and the remaining campaign lifetime. A longer submitted wait grants no extra time.
Every ordinary operation uses this rule, including model generation, artifacts,
evidence reads, snapshots, restores and finish.

The target profile's `operation_timeout_ms` additionally limits the complete
attempt, including preparation, setup, invocation and observation collection.
It MUST NOT shorten unrelated operations. Operator MUST enforce the installation's
`target.max_operation_timeout_ms` ceiling before target attachment. Neither a
per-operation wait nor a target profile increases a campaign budget or a native
service's own execution limit. See [attempt deadlines](ATTEMPT_TIMEOUT_CONTRACT.md)
for bounded timeout reporting and cleanup.

## Operating sizes

| Field | Meaning | Fixed implementation ceiling |
|---|---|---|
| `direct_input_bytes` | Decoded UTF-8/base64 bytes or canonical JSON bytes per direct input | 1 MiB |
| `assessment_bytes` | Canonical assessment bytes (or exact retained artifact bytes) | 1 MiB |
| `artifact_bytes` | Declared bytes per uploaded artifact | 16 MiB |
| `artifact_part_bytes` | Decoded bytes per upload part | 1 MiB |
| `observation_chunk_bytes` | Decoded bytes per retained observation read | 1 MiB |
| `snapshot_page_items` | Requested/default snapshot page entries | 100 |

Values MUST be positive integers within these ceilings. Omitted snapshot page
length MUST use the configured page allowance. Explicit larger values MUST be
rejected, never silently clamped. Harness MUST choose upload/read chunks from the
supplied policy. Operator MUST independently validate requests before performing
requested effects. Artifact references MUST still resolve to retained, committed,
campaign-owned artifacts; raising direct-input limits bypasses none of those checks.
Target capability/input limits, permitted feedback and cumulative campaign budgets
remain independent and MUST still be enforced.

A policy rejection MUST use the existing argument diagnostic with reason
`configured_size_exceeded`, a field path, and correction guidance. No target dispatch
occurs. Reusing an operation ID with changed content remains an idempotency conflict;
a corrected submission uses a new ID.

## Why fixed ceilings remain

- Ordinary envelopes remain 4 MiB; control frames remain 64 KiB. FIFO framing,
  spool queues and both parsers allocate and account against these bounds.
- Direct decoding and transfer chunks are capped at 1 MiB, leaving room for
  base64 expansion, JSON escaping, descriptors and envelope fields. The encoded
  frame ceiling independently applies even when decoded input fits its allowance.
- Artifact integrity verification currently reads at most 16 MiB into memory;
  assessment normalization/reference validation is bounded to 1 MiB. Larger values
  require implementation changes, not just an administrator's policy choice.
- Snapshot response arrays remain bounded to 100 entries to bound metadata
  expansion in the current response schema. The operating page length can be smaller.
- JSON parser depth (32), direct JSON canonicalization depth (16), exact JSON-safe
  integers, bounded identifiers, diagnostic prose and structural collection counts
  remain shared validation guarantees. The startup tree's 64 MiB aggregate and
  prompt/skill bounds protect eager startup loading and validation in both products.
- Transport progress, bootstrap and independent cleanup timers are lifecycle safety
  mechanisms, not ordinary-operation waits. They do not restart attempt budgets.

These are compatibility/safety ceilings, not recommendations for campaign size.
Changes to host operating values within them MUST NOT require a new contract.
Changes to the ceilings or their interpretation MUST use normal contract publication.
