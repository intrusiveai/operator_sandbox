# Harness loop accounting implementation

Status: Go/Python accounting helpers and 129 shared cases implement the finite-loop
rules in [HARNESS_EXECUTION_RULES.md](HARNESS_EXECUTION_RULES.md). The helpers run
inside a trusted dispatcher; they are not a broker, model codec, timer service or
wire authorization mechanism. No new wire operation or Interceptor change is added.

## Configuration

Go: `protocol.ResolveHarnessLimits(overridesJSON, policyCapsJSON)`.
Python: `loop.resolve_harness_limits(protocol, overrides=b'{}', caps=b'{}')`.
Python APIs are in `operator_contracts.loop`.

Both arguments are bounded JSON objects containing any subset of the seven
existing harness limit fields. Missing administrator fields select the documented
defaults. Each explicitly supplied policy cap narrows the corresponding resolved
value; an omitted cap does not cap an administrator override at the default.
Unknown fields, zero, booleans, fractions and unsafe integers fail. The returned
complete object is suitable for `EngineContext.limits.harness`. This is explicit
administrator configuration resolution, not mutation/defaulting of wire messages.

Create `NewHarnessLoop(effectiveLimits)` / `HarnessLoop(effective_limits)` once per
campaign, with all seven effective fields. Inputs and returned snapshots are copied.
Keep the same object across target restores. No counter or progress reset API exists.

## Model turns and tool batches

The serialized event sequence is:

1. `BeginModel(compaction)` / `begin_model(compaction=False)` charges a **new
   admitted** generation, including a refusal, empty response or compaction.
   The host must durably record admission before provider I/O. Pre-admission
   rejection does not call this method.
2. `AcceptResponse(toolCount)` / `accept_response(tool_count)` accepts the whole
   decoded batch before any tool dispatch. Over-limit batches stop exploration
   with `budget-limit` and skip every call. Compaction cannot dispatch tools.
3. `StartTool(name)` / `start_tool(name)` charges a newly dispatched model-facing
   call before argument validation, including unknown-tool/invalid-argument calls.
   Names are bounded tool identifiers; use the dispatcher's `unknown` classification
   for an unrecognized name that is not such an identifier. Both short names and
   `engine.` names are understood for record, restore and attempt accounting.
4. `FinishTool(outcome)` / `finish_tool(outcome)` settles that call exactly once.
5. `EndTurn()` / `end_turn()` runs after every call is settled or skipped. It
   updates the no-progress streak once, including empty/refusal and compaction turns.

`FinishTool` outcomes are internal dispatcher classifications:

| Outcome | Accounting |
|---|---|
| `success` | Reset only the consecutive invalid streak, except for `record_append`. |
| `invalid` | Increment cumulative and consecutive invalid counts once. |
| `preflight-rejected` | Known unchanged-target preflight rejection: neither increment nor reset invalid counts. |
| `failed` | Known execution failure: neither increment nor reset invalid counts; native admission remains charged separately. |
| `restored` | Validated successful `restore_request`: reset consecutive invalid count and skip the rest of this batch. Restore alone gives no progress credit. |

The last permitted model generation may complete its batch. The last permitted
tool may finish; remaining queued calls are skipped. Reaching either invalid
threshold stops with `harness-error`. A previously selected graceful stop reason
is retained; a hard stop always overrides graceful work. At simultaneous thresholds
in a tool completion, invalid-call closure precedes the tool quota; at turn end,
no-progress closure precedes the model quota.

Snapshot `queued_calls` and `skipped_calls` describe the current/last batch, while
usage counters are campaign-wide. The dispatcher owns actual call IDs and emits
their correlated not-executed results; the accounting helper supplies counts,
not synthetic native results. Starting the next model turn resets only batch
dispositions, not usage, invalid history or novelty history.

Transport retransmissions, repeated native responses and known operation replays
must be resolved by the runtime's saved-result lookup **before** calling accounting
methods. Internal artifact upload chunks are not model-facing calls. A newly
dispatched model call that retrieves an existing result still costs one tool call.
A duplicate restore response must not call `FinishTool` twice or start a second
model generation. No helper method supplies permission to repeat an effect.

## Reads and progress

`ReserveRead(kind, identity, offset, size)` / `reserve_read(...)` reserves a
positive requested range before I/O; `SettleRead(actualBytes)` / `settle_read(...)`
settles actual bytes **before exposing them to the adaptive loop**. One outstanding
read is supported by the serialized dispatcher. Initial instruction reads are
allowed while idle; model-requested reads occur inside an active tool.

- `kind: content` identifies immutable reference/skill bytes by their verified
  raw content digest. The same digest shares a range history across paths/names.
- `kind: observation` uses a trusted digest of the immutable feedback source/entry
  identity, not a model label or a native hash recomputed under another recipe.
  The adapter supplies a stable campaign-local identity across restores. It must
  distinguish different entries and revisions whose bytes can differ. This is an
  internal lookup key, not a new public artifact digest or wire field.
- Offsets and lengths are safe nonnegative integers with a representable end;
  requested length is positive. Actual returned length may be zero. Content
  integrity, visibility, file length and receipt/source membership are checked
  independently by the runtime.

Repeated and overlapping ranges cost their full actual byte count. Unused
reservation is refunded for short reads, EOF and unavailable results. A requested
range larger than the remaining allowance stops exploration without delivery or
truncation. Reaching the exact byte ceiling permits delivery of the last allowed
result, then skips queued calls. Initial reads count and enter novelty history,
so rereading them later cannot claim new progress. Integrity-only reads are not
charged through this API.

Snapshots separate `read_bytes` (known delivery) from `reserved_read_bytes`
(pending delivery). An unknown outcome calls `HardStop` and retains the pending
reservation; it cannot subsequently be refunded by settling zero bytes.

Novelty uses the union of delivered half-open byte ranges for each immutable
source. A turn receives progress credit only from a novel nonempty delivered range,
`ExperimentCompleted(verifiedReceiptID)` / `experiment_completed(...)`, or
`PayloadCommitted(verifiedDigest)` / `payload_committed(...)`. Experiment and
payload identities are deduplicated across the campaign. Valid negative experiment
feedback counts. Novelty callbacks require an active tool; experiment completion
also requires an attempt tool. Compaction gives no progress credit.

These callbacks are for verified runtime events. Never call them from model
claims, progress records, heartbeat text, labels or snapshot creation alone.
Operation receipts and actual verified bytes determine events; the helper does
not verify those receipts itself. The guest's local progress state is not host
attestation or a reason to refund authoritative host usage.

`NarrowRemaining(modelTurns, readBytes)` / `narrow_remaining(...)` applies a verified
host remaining-budget update between operations. It uses the smaller host/local
allowance and cannot widen a prior cap or reset usage. Zero future model allowance
still permits the already admitted batch to finish; zero read allowance stops
further exploration. Apply updates only when no model generation, tool or read
is outstanding. Independent host admission/read accounting remains mandatory.

## Bounded finalization and hard stops

When a quota or no-progress threshold is reached, mode becomes `finalizing`:
new model/tool/read work is forbidden. Settle already admitted known work and
finish the turn, then immediately call `BeginFinalization(nowMS,
campaignDeadlineMS, artifactRemaining)` / `begin_finalization(...)` once. A
startup read that exhausts quota can finalize from the idle phase without a turn.

The returned budget has:

- 16 new ordinary request slots;
- at most 2 MiB of distinct conclusion content, further narrowed by the remaining
  host artifact allowance;
- a deadline at the earlier of campaign deadline and start + 30 seconds.

`Charge(kind, conclusionBytes, nowMS)` / `charge(...)` reserves a request slot and
new conclusion bytes before transmission. Trusted kinds are `conclusion-artifact`,
`conclusion-record` and `stop`. The broker must verify the actual request payload,
artifact purpose/upload handle and record kind before assigning a kind. A model
or guest-supplied kind grants no authority. Charge each distinct conclusion's
bytes once before transfer, including content in an existing upload continued
during finalization; subsequent multipart requests still consume request slots.
Artifact begin/part/commit, conclusion record and stop retain their existing schemas.

The exact last allowed request/byte may complete. Zero remaining artifact bytes
still allows an unavailable-conclusion stop if time/request slots remain. A
request or byte overflow closes the budget without partial charging. A second
finalization start is rejected; it cannot renew time or quotas. Close the budget
after accepted stop or abandonment.

Times are safe integer milliseconds from one monotonic clock. `CheckTime(nowMS)` /
`check_time(...)` must also be driven by the runtime deadline timer while idle or
waiting for I/O. Deadline equality expires the budget; a clock reversal closes
it. The helper schedules no timers. Provider/operation deadlines, the campaign
hard deadline, 180-second stall detection and Docker termination remain runtime
responsibilities, independent of these counters.

`HardStop()` / `hard_stop()` immediately closes exploration and any associated
finalization budget, including when a model/read is outstanding. Use it for host
termination, unknown effects, channel failure and other terminal conditions; do
not wait for graceful work. Its internal `hard-stop` reason is not a new wire
finish-reason enum; the runtime journals and translates the actual cause.

## Verification and remaining integration

Both languages run [harness-loop-accounting.json](fixtures/harness-loop-accounting.json).
The 129 cases cover every configuration field, exact quota boundaries, invalid
streaks, no-progress/compaction, restore skipping, read reservation/refund/novelty,
host narrowing, finalization limits and hard-stop precedence. Mixed range traces
use an independent byte-set oracle. Regenerate with
`python3 scripts/generate_loop_fixtures.py`.

These helpers have no persistence, provider/network I/O, native execution or
container lifecycle. The host service and harness dispatcher now integrate saved-result lookup,
durable host accounting, verified event sources, bounded histories, actual
correlated result segments, timers and independent termination. Full fake-broker /
fake-harness transport conformance and runtime qualification remain outstanding.
