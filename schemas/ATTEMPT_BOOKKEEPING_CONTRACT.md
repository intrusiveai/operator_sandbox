# Attempt bookkeeping implementation

Status: matching Go/Python in-memory helpers and 40 shared traces implement the
numbering and admission transitions from [Harness execution rules](HARNESS_EXECUTION_RULES.md#1-campaign-wide-attempt-numbering).
This contract adds no wire fields or Interceptor changes. The host's
[durable attempt layer](../docs/DURABLE_ATTEMPT_ADMISSION.md) now connects these
helpers to the campaign journal, result reservations and terminal failure signal.
Native dispatch, lineage verification and host/harness loop integration are
implemented. Complete process/native qualification remains outstanding.

## Host allocation

Operator assigns fresh attempt IDs, indices and lineage while resolving a simple
request, before durable admission or dispatch. Harness assigns only the outer
operation/call correlation. The host retains the original canonical request and
resolved internal plan. Exact duplicate delivery reuses the same plan; changed
content conflicts. Host rejection may consume an index; local rejection does not.
No external allocation RPC is used. Parent input is an execution receipt whose
lineage is resolved by Operator, never a model-authored generation or thread.

## Operator admission ledger

Go exposes `NewAttemptLedger(highWatermark, maximumAdmissions)`; Python exposes
`AttemptLedger(high_watermark=0, maximum_admissions=100)`. Go callers supply the
resolved campaign admission limit explicitly (default 100). Positive limits and
nonnegative initial watermarks must be safe integers.

The ledger belongs to one campaign and one serialized dispatcher. It accepts
`AttemptSubmission`: request ID, attempt ID, index and `identity_digest`. The digest
is **computed by the host** from the complete duplicate identity required by
[SHARED_CONTRACT section 3](SHARED_CONTRACT.md#3-digest-and-duplicate-rules): campaign,
operation, durable operation ID, canonical body and original effect target. This
helper validates digest syntax and compares pins; it does not compute that
projection, trust guest digests, validate the full request, or resolve live bindings.
Attribution and transport sequence changes do not create a new operation.

| Call | Result |
|---|---|
| `Observe` / `observe` | New identifiable submission becomes `observed`, advances high water and reserves its attempt ID. Exact replay returns the saved record and `replay=true` before monotonicity/closure checks. Changed identity or tuple conflicts. |
| `Admit` / `admit` | Only an observed submission can become newly admitted. Charges one admission; returns `true` once. Duplicate admitted/completed requests return `false` and cannot execute again. Rejected requests cannot be admitted. |
| `Resolve` / `resolve` | Observed → `rejected`; admitted → `succeeded`, `failed` or `unknown`. Pins the caller's retained result digest. Identical resolution is idempotent; changed results and invalid transitions fail. |
| `Lookup` / `lookup` | Returns a copied/immutable record without granting execution permission. The caller retains the actual result bytes/receipt separately. |
| `Close` / `close` | Permanently prevents new submissions, admissions and resolutions. Previously saved exact observations/resolutions remain readable/idempotent. |

An unknown outcome closes the ledger and retains its admission. Known admitted
failures remain charged. A full admission quota does not prevent recording or
rejecting a later identifiable submission; its index is separate from quota usage.
Lookup of a pending or unknown duplicate never authorizes another effect. Unknown
outcome reconciliation and reporting are host-owned work outside this helper.
`result_digest` pins the raw bytes of the separately retained operation result;
it is not a new wire receipt. Helper records are internal state, not another
interchange schema or a replacement for the existing attempt/result contracts.

## Required runtime integration

The shared in-memory helpers do not provide crash durability. The host's durable
wrapper implements the persistence ordering below; the full dispatcher must also
perform the specified policy, lineage and native-effect checks:

1. Validate outer framing/campaign binding and the simple body; derive a trusted
   operation identity. Resolve duplicates against their original stored target
   before checking obsolete bindings. Verify uniqueness against any native history
   already bound to the campaign; a numeric constructor seed does not import that
   history or its IDs.
2. Observe and durably journal each identifiable new tuple/high-water transition
   before subsequent tactical validation or a rejection response.
3. Perform capability, policy, artifact and native parent/thread validation.
   An allocated/rejected attempt is not evidence of a registered native parent.
4. Admit and durably journal execution intent and the admission charge **before**
   the first native operation. Execute only on a new successful admission.
5. Persist result bytes/receipts and resolution before returning a known result.
   Any journal-write failure closes execution and requires host recovery; never
   continue with only the in-memory state or refund uncertain effects.

Keep these objects separate from restored target state. Runtime journaling and
quota policy must bound retained submissions/results, including rejections;
these in-memory helpers do not implement storage limits or journal recovery.
Use one event-loop owner (or external serialization), not concurrent mutation.
Neither helper adds worker/revision access restrictions.

## Verification

Both suites read [attempt-bookkeeping.json](fixtures/attempt-bookkeeping.json).
Traces cover rejection 7 → correction 8, duplicate pending/completed/rejected
requests, changed identity conflicts, local-only gaps, malformed and reserved
arguments, admission boundaries, terminal unknown outcomes and integer exhaustion.
Restore retention is tested as continued use of the same state, without a native
restore; dispatch skipping, durable persistence and native adapter integration
have separate runtime component tests. Complete process/native qualification
remains outstanding. Regenerate fixtures with
`python3 scripts/generate_attempt_fixtures.py`; expectations are independently
authored, not generated by the implementation under test.
