# Durable attempt admission

Status: implemented in `internal/campaign`. This stage joins the existing shared
attempt ledger to the host journal. It adds no host/harness wire fields and needs
no Interceptor or Attack Harness changes. The [durable native executor](NATIVE_EXECUTION.md)
now provides the per-step dispatch/audit boundary; concrete typed attempt and
feedback adapters remain a later stage.

## Startup and authority

Create the campaign writer from the verified RunManifest, then call
`ConfigureFreeSpace(minimumFreeBytes)` before its first event. This freezes and
journals the host-selected filesystem free-space floor. Call
`NewAttempts(writer, initialHighWatermark)` once, using the high-water mark from
the verified initial EngineContext. The constructor durably records its seed and
limits; it cannot replace an existing campaign ledger or reopen recovered work.

Admission capacity comes from RunManifest's initial
`remaining_limits.attempt_admissions`, including zero. The number of identifiable
submissions is also capped by `harness_limits.max_tool_calls`. This bounds rejected
submissions as well as admitted ones. Other tool calls still need their separate
campaign-wide accounting. Host policy selection and launch verification remain
prerequisites; these APIs do not establish trust in arbitrary caller configuration.

## Request-to-result ordering

The future broker uses these steps for `engine.attempt_execute`:

1. Validate the outer transport/envelope identity, campaign, launch and operation
   routing. Preserve the exact envelope in the transport audit. Pass its exact
   bounded attempt body and host-attributed worker/revision to `Observe`.
2. `Observe` checks strict JSON and the request ID, attempt ID and positive safe
   integer index before tactical validation. It computes duplicate identity,
   advances the high-water mark, reserves future audit capacity and commits the
   exact request body. Only a successful durable observation may be acknowledged
   or proceed to validation. Invalid tactics can therefore consume an index while
   consuming no attempt admission. Malformed/unidentifiable bookkeeping does not.
3. Apply the complete shared request validator, policy/capability/feedback rules,
   artifact checks and native parent/thread checks. For rejection, `Resolve` with
   `rejected` retains the rejection body and releases unused reservation capacity.
4. For authorized work, `Admit` charges one attempt and durably stores the bounded,
   authorized translated native plan. A repeated identical admission returns
   `fresh=false`; a changed plan conflicts. Capacity exhaustion leaves the observed
   request available for a durable rejection and does not refund earlier charges.
5. `MarkDispatched` commits the possible-contact boundary and returns `fresh=true`
   once. The executor must still recheck the terminal fence, deadline and exact
   live target/container before each native step of this one admitted plan. A
   false return never permits another execution of the plan. There is no implicit
   native retry.
6. The native adapter validates the response and determines its outcome.
   `Resolve` commits the exact guest result, optional native response and verified
   receipt metadata before guest delivery. Known failure remains charged. Unknown
   outcome closes execution before attempting the journal write.

The initial observation uses the journal's `INTENT_COMMITTED` marker for the
identified request. **This is not an admission charge.** The separate
`attempt.admitted` event records that charge after validation. `DISPATCHED` means
external contact may have happened; the durable result marker is
`RESULT_COMMITTED` or `UNKNOWN`. Event metadata carries the shared attempt record,
original target/worker/revision attribution, high-water mark and cumulative
admissions. Result content descriptors are in the event's `content` inventory.

The durable helper accepts a strict JSON object before it knows whether the full
tactical body is valid. It deliberately does not replace the shared attempt
schema validator. Likewise, plan authorization, native result interpretation and
typed response/receipt validation belong to the broker/adapter. `Resolve` checks
bounds and consistency, not the truth of a caller-supplied successful outcome.

## Duplicate identity and restore

Identity is SHA-256 of this complete `jcs-v1` object:

```text
{
  campaign_id,
  operation: "engine.attempt_execute",
  operation_id: request_id,
  body: complete parsed attempt body,
  effect_target: {
    adapter, session_id, capability_source_digest,
    capability_projection_digest, native_feedback_profile
  }
}
```

For an existing operation ID, use its saved original target before comparing the
body. Transport call/sequence IDs and caller worker/revision attribution do not
enter this identity. A newer worker/revision can retrieve the same campaign's
saved result. Changed command content conflicts; corrected tactical requests use
fresh IDs and the next index.

`Observe` returns `replay=true` for committed duplicates, including pending,
rejected, completed and unknown records. It does not create a new reservation,
advance counters or grant dispatch. `Lookup` verifies and reads result bytes from
the campaign journal rather than retaining potentially large responses in memory.
The future broker constructs the response envelope for the current exchange around
that saved result. It must not rewrite the saved receipt's original attribution.

`Rebind` records a caller-verified successful restore with the next revision and
replacement native session. First drain and resolve pending attempts. It keeps
the same ledger, admission charges and high-water mark. Existing request IDs still
resolve against their original target; new requests must address the active
revision. This is target routing, not ownership of earlier campaign data. Immutable
launch inputs and the harness remain unchanged.

## Audit capacity and free space

Reservations are host bookkeeping inside existing journal metadata, under the
reserved `journal_reservation` key:

```json
{"id":"request-1","action":"reserve","bytes":13369347,"release":false}
```

A consume directive uses the same ID, `action: consume`, `bytes: 0` and a release
boolean. Actual charged bytes are derived from the complete committed event line
and its content descriptors. Releasing a reservation frees only its unused
remainder. Already retained bytes and execution charges are never refunded.

The attempt reservation is `3 * (256 KiB + 1) + 3 * 4 MiB`: three maximum-sized
admission/dispatch/completion envelopes, one translated plan, one native response
and one guest result. The original observation/request bytes are charged separately.
Receipt metadata is capped at 60 KiB, leaving room for attribution, counters and
reservation metadata within the 64 KiB metadata ceiling. Every result/plan/native
body is separately bounded to 4 MiB. This covers the attempt-level audit records,
not an arbitrary sequence of native step exchanges. The native-step ledger now lets a multi-step adapter
reserve and journal each step's native requests, responses and uncertainty
separately before contact; it cannot treat this fixed allowance as covering all
injection/invocation/cleanup traffic. The adapter must enforce body bounds while
reading; an oversized or malformed result is terminal.

`AppendReserving` commits the reservation with its owning event. Unreserved
appends and other reservations cannot consume held campaign journal capacity.
`AppendReserved` spends that reservation; completion releases its unused remainder
in the same durable transaction. Direct metadata cannot forge a reservation
directive, and a reservation ID cannot be reused. Recovery verifies these
transitions and reports any outstanding capacity alongside unknown operations.

After free-space configuration, each append checks that available filesystem
bytes can cover the new write, outstanding reservations and the configured floor.
This is a campaign budget reservation and a filesystem preflight, **not exclusive
physical allocation**. Another campaign/process can consume disk space after the
check, and filesystems can still fail a write or sync. Such failures fence
execution; no helper claims an external operation can always retain its result.

## Terminal signal and recovery

`Writer.Fence()` exposes a one-way atomic stop reason and a `Done()` channel.
Reading or stopping it acquires neither the worker nor journal lock. Journal
I/O/quota/free-space failure, failed required bookkeeping and unknown outcomes
signal it. `Writer.Close` also closes the signal. The first reason remains stable.

Unknown outcomes signal before attempting their result write. Admission and
dispatch methods recheck the signal after committing, so a stop during persistence
does not produce a fresh dispatch grant. The future executor must check it again
at the external-call boundary. The runtime must observe the signal and initiate
independent Docker termination without waiting for more journal work; that Docker
handler is now available in the [Docker termination layer](DOCKER_TERMINATION.md);
the future launcher must arm it before admission.

Committed duplicates remain identifiable. Uncommitted in-memory transitions never
produce success responses, and a failed ledger cannot admit more work. Read-only
`Inspect` preserves the committed counters/events and outstanding reservations;
missing results remain unknown. It cannot recreate an executable attempt ledger.
`Status` is live bookkeeping; after failure, use retained evidence for reporting.

## Validation and remaining work

Tests exercise rejection gaps, zero/exhausted admissions, changed commands/plans,
concurrent duplicate observations/admissions/dispatches, restoration with new worker
attribution, exact saved result reads, result corruption, maximum audit bodies,
reservation competition, free-space exhaustion and failed writes at every boundary.
They also verify a terminal signal while the journal mutex is held, stop during
dispatch persistence, and abrupt process exit with a dispatched unknown operation.

Remaining runtime work includes the broker's actual transport/envelope audit,
authorization/native adapter and its step audit, model/other-operation accounting,
progress/finalization timer integration and wiring the implemented
[emergency records/Docker observer](DOCKER_TERMINATION.md) into launch and dispatch.
The [host transport layer](HOST_TRANSPORT.md) now
supplies FIFO/spool queues and startup/transfer/operation/campaign timers. None of the
new APIs starts a container or sends a native request.
