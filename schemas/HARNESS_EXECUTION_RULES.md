# Harness attempt allocation and loop limits

Status: accepted MVP contract. These rules complete the numbering and finite-loop
decisions; Operator/Attack Harness implementation and shared Go/Python conformance remain
pending. They supplement SHARED_CONTRACT.md without a new broker operation.

## 1. Campaign-wide attempt numbering

`attempt_index` is the chronological submission number, not the number of executed
experiments, the thread generation, a native session revision or a quota counter.
It starts at 1 in a new campaign and strictly increases across all threads and
healthy restores. Gaps are allowed. Its maximum is 2^53−1; never wrap or recycle.
If another allocation would exceed that maximum, finish with `budget-limit`.

The trusted harness request builder owns allocation. The model-facing projection
omits `request_id`, `attempt_id` and `attempt_index`; the wrapper fills those
bookkeeping fields, leaving the model's tactical fields unchanged. Supplying
reserved bookkeeping fields is an argument error, not an instruction to override
the allocator. The full EngineAttemptRequest wire schema still requires all three;
`request_id` equals the ordinary envelope's durable `operation_id`.

Allocate as follows:

1. At admission, initialize the local high-water mark from the required
   `EngineContext.attempt_index_high_watermark` (0 for a new campaign). Operator
   derives this from its campaign ledger and any verified native attempts already
   bound to this campaign before publishing context. The field is a nonnegative
   integer at most 2^53−1. If a native index cannot be represented, reject preparation.
2. For a new `attempt_execute` call actually selected for dispatch, first require
   syntactically valid bounded JSON object arguments. Reserve fresh request/attempt
   IDs and index `local_high_watermark + 1` exactly once, then advance the local
   mark before structural, artifact, lineage and capability validation. Keep the
   allocation with the local call record, including any rejected result.
3. Malformed JSON/non-object arguments cannot form a submission: report bounded
   invalid-call diagnostics without allocating an index. Invalid outer model
   responses follow existing codec/protocol failure rules and allocate nothing.
   Calls skipped after restore, quota closure or termination are never
   dispatched and allocate nothing. They must not become attempt records.
4. A local or host rejection after allocation retains that number in history.
   A corrected submission is a new request ID, new attempt ID and new index. Never
   renumber later submissions to fill the gap. A rejected attempt is not a valid
   parent for a native child: reference a registered same-thread parent or form a
   root with generation 1 under the existing lineage rules.
5. Exact duplicates retain the original tuple/content and known result. Duplicate
   lookup precedes new-allocation/monotonicity checks; they allocate no new number
   and cannot cause another experiment. Changed content under a used request ID
   conflicts. This does not permit a guest retry after timeout/channel loss or an
   unknown effect; reconciliation remains host-owned.
6. Operator journals an identifiable new submission's request/attempt/index tuple
   before target admission, even if semantic validation later rejects it. For an
   unseen request, require the index to exceed its campaign high-water mark and
   the attempt ID to be unused; persist the updated mark with the submission.
   Invalid bookkeeping cannot advance it. Gaps need no native placeholder records:
   local rejected submissions may never reach Operator. Record local rejections
   in bounded harness history and subsequent model/tool history as guest assertions;
   they cannot overwrite host-observed admission or execution records.
7. Preserve the harness allocator and Operator ledger across restore. Never seed
   them again from the restored native registry or immutable launch context. The
   native registry may contain an earlier prefix; Operator sends the new campaign
   index unchanged. No worker/revision ownership or counter synchronization RPC
   is needed. Failed campaigns still do not resume or reconstruct a harness.

The campaign execution quota (default 100) counts durable **attempt admissions**,
not the highest allocated index. Operator validates the request, content, binding,
capability and policy before admission, then charges one attempt when committing
execution intent before the first native registration/setup/invocation. Rejection
before that admission charges no attempt, although it consumes any allocated
number and applicable tool/invalid-call limits. An admitted operation remains
charged if later setup/invocation fails. A known duplicate charges once. Unknown
outcomes retain their admission and follow terminal reconciliation rules. Restores
and deletion never refund campaign admissions. Interceptor separately limits the
number of native registered attempts; its limit is not an index ceiling.

| Event | Index / admission effect |
|---|---|
| Attempt 7 rejected for unavailable payload before admission | Keep 7 as rejected; zero execution admissions. |
| Corrected submission | Use fresh IDs and index 8; charge one only if admitted. |
| Exact known replay of submission 8 | Same index/result; no second allocation or admission. |
| Restore checkpoint containing attempts through 3 after allocating 8 | Next new submission is 9, including when 8 was rejected. |
| Restore skips a queued attempt call | No allocation, registration or admission. |
| JSON cannot be decoded as a tool argument object | No allocation; tool and invalid-call accounting still applies. |

## 2. Configurable finite-loop defaults

Administrator configuration uses `limits.harness.<field>`. Omitted configuration
selects the defaults below; explicit values must be positive exact integers at
most 2^53−1 (zero is invalid, never unlimited). Do not use JSON Schema defaults to
mutate requests. Operator resolves configuration against installed release policy
and applicable host/target limits before startup; the smaller limit wins. The
closed [effective-limits schema](harness-loop-limits.schema.json) requires every
field in `EngineContext.limits.harness` and the admission budget projection.
Changes take effect on a new campaign, never by resetting a running campaign.

| Field | Default | Meaning |
|---|---:|---|
| `max_model_turns` | 300 | Admitted model generations across the campaign, including model-based compaction and any model-assisted conclusion. |
| `max_tool_calls` | 2,000 | Model-facing calls selected for dispatch, including unknown names and locally rejected arguments; internal upload chunks are not extra model tool calls. |
| `max_tool_calls_per_response` | 16 | Total client tool calls in one native model response, checked before dispatching any of them. |
| `max_invalid_tool_calls` | 50 | Cumulative unknown-tool/argument/schema/semantic rejection results before execution admission. |
| `max_consecutive_invalid_tool_calls` | 5 | Consecutive dispatched calls producing those invalid-call results. |
| `max_read_bytes` | 268,435,456 (256 MiB) | Cumulative raw reference/skill-content and observation bytes delivered to the adaptive loop, including repeats. |
| `max_no_progress_turns` | 10 | Consecutive completed model turns without one of the progress events below. |

These supplement the existing 100 admissions, 30 minutes, 250,000 model tokens,
artifact/snapshot ceilings, operation deadlines and 180-second idle/stall timeout.
Reaching any applicable limit is sufficient; increasing a loop limit does not
relax another limit. None of these counters resets on restore. Release policy
may cap administrator overrides; effective values and reasons are visible in
startup context and the campaign journal.

### Accounting and enforcement

- Charge a model turn when Operator durably admits `engine.model_generate`, before
  contacting the provider. A pre-admission rejection costs no model turn; an
  admitted refusal/empty output still costs one. Known duplicates do not double
  charge and unknown outcomes remain reserved/terminal. Compaction is not a free
  model call. The last permitted model generation may finish its tool batch while
  other limits allow; finalize before attempting generation N+1. All generated
  responses remain subject to token/time limits.
- Charge one tool call immediately before local tool dispatch, including an
  unknown name or invalid argument object. The call that reaches the ceiling may
  finish; no later queued call starts. Count a local wrapper once even if it uses
  several bounded broker operations. Transport duplicates do not create new calls;
  a newly issued model tool call is counted even if it retrieves a saved result.
  Skipped calls are never dispatched and consume no tool/admission/effect charge.
- An over-limit tool-call batch is rejected as a whole before any tool effect;
  count its admitted model generation and close the loop with `budget-limit`.
  Preserve its undispatched disposition for reporting; do not execute a prefix
  or make another model call to repair an oversized batch.
- Increment invalid totals/streaks once per dispatched call returning a correctable
  unknown-tool or pre-admission validation rejection, local or host, even if several
  fields are wrong. A successful dispatched tool other than `record_append` resets
  only the consecutive invalid streak (invalid calls since that last success).
  No-tool model turns, skipped calls, records of an error and
  heartbeats do not reset it. Known runtime preflight rejection with unchanged
  target is not malformed input; it counts toward tool/no-progress limits but not
  invalid-call counters. Policy/budget closure and failures follow their own stop
  rules rather than being reclassified as validation errors. At either invalid
  threshold, stop further tools/model generations with `harness-error`.
- Count raw bytes returned by `reference_read`, instruction/skill loading and
  `engine.observation_read` before exposing them to the adaptive loop. Includes
  automatic initial instruction reads and every repeated range. Transport framing,
  base64 expansion and integrity-only file/manifest validation have their existing
  independent bounds, not additional read-byte charges here. Internal reuse of already charged
  cached bytes is not a fresh read; returning those bytes through a new read call
  is charged again. Reserve a requested range before reading; reject a range that
  exceeds remaining allowance without delivery. Settle to actual returned bytes
  (including zero-byte EOF/unavailable); unused reservation is not consumed usage.
  Unknown read outcomes retain the reservation and follow terminal reconciliation.
  Do not silently truncate solely to fit the remaining allowance. Reaching or
  requesting beyond this limit ends exploration with `budget-limit`.
- The harness enforces all local counters. Operator independently enforces the
  model, feedback-byte, admission and other limits it can observe. Local reads and
  dispatcher counters remain attributed harness state, not host attestation or a
  new monitoring requirement. Guest reports never refund authoritative host usage.
  Operator supplies host remaining budgets; the harness combines them with its
  retained local counters using the smaller allowance. No new telemetry RPC is
  required. Neither side restores counters from target snapshots.

### Progress and termination

At the end of each completed model response plus its dispatched/skipped tool
results, reset the no-progress streak only if that turn produced at least one:

- completed experiment with a known outcome, including valid negative feedback;
- first delivery of a nonempty byte range not previously read from an immutable
  reference/skill digest or feedback source/entry (track the union of byte ranges);
- first successful commitment of a payload/carrier content digest in the campaign.

Otherwise increment the streak once. An empty/refusal/reasoning-only model response
counts as a no-progress turn if it is otherwise nonterminal. Re-reading cached
ranges, exact duplicates, zero-byte/unavailable reads, changing labels/descriptions,
record/progress writes, heartbeats, cleanup, snapshot creation or restore alone
do not reset it. State-management calls enable experiments; a completed experiment
or new content delivery/commitment supplies progress credit. Model claims of
success are not progress evidence. Model-assisted compaction consumes a model
turn and is itself no progress; never reset the streak by summarizing the conversation.

At the no-progress threshold, stop exploration with `no-useful-next-experiment`.
This finite-turn guard is separate from the existing 180-second idle/stall timeout;
an admitted operation uses its own deadline, and heartbeats extend neither limit.

When a limit blocks further work (after the last permitted operation/batch as
defined above), cancel remaining undispatched calls, preserve their
not-executed status for reporting, and finalize from known records without another
model generation or target effect. Allow only the existing conclusion artifact,
conclusion record and `engine.request_stop` path: at most 16 ordinary finalization
requests, at most 2 MiB of conclusion bytes, within 30 seconds and all remaining
host time/artifact allowances. These fixed finalization bounds do not renew
exploration quotas. If resources cannot support the conclusion, use the existing
explicit unavailable conclusion with `budget-limit` or `local-error` as applicable.
No new finish-reason enums or implicit report success are introduced. Host hard
stop, terminal errors, unknown effects and channel failure bypass graceful work;
cleanup/reporting must never delay Docker termination.

## 3. Acceptance cases

Shared Go/Python allocator/dispatcher traces must verify rejected 7 → corrected 8;
exact duplicates and changed-content conflicts under a used ID; local-only gaps;
malformed and skipped calls without allocation; no rejected native parent; host high-water mark
persistence before rejection; restore retaining index/counters; index overflow;
admission counts independent of numbering; every default/override and zero rejection;
limits exactly at N versus N+1; oversized batch with no partial execution; 5
consecutive versus 50 cumulative invalid calls; successful reset of streak only;
repeat/overlapping read charges versus novel-byte progress; EOF and unused read
reservation; refusal/compaction/no-tool turns; repeated restore/snapshot and forged
progress; restore-skipped tool accounting; bounded model-free finalization and
host-stop precedence. Operator/Attack Harness runtime tests remain implementation work.
