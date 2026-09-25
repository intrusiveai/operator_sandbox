# Durable native execution

`campaign.Attempts.NativeSteps()` supplies the per-step journal underneath the
native executor. It shares the owning attempt ledger, so creating another handle
does not create another dispatch grant. It has no recovery/resume constructor.

## Journal ordering

The parent attempt must first pass tactical validation, `Admit` its translated
plan, and `MarkDispatched`. For each authorized native operation:

1. `Begin` commits the exact prepared envelope and reserves future audit capacity.
   It binds the operation to the admitted parent, campaign, active native session,
   attempt ID when present and current revision. Only one unresolved native step
   may exist. This does not replace typed plan/selector authorization.
2. `MarkDispatched` commits the possible-contact boundary and grants dispatch once.
   The executor must check the live binding, deadline and terminal fence before
   contact. A repeated call cannot repeat the effect.
3. `Resolve` stores the native envelope and adapter-verified disposition before
   handing it to any result builder. Failed, unknown and locally undeliverable
   admitted steps stop the fence before waiting for the journal lock.
4. `Lookup` verifies retained bytes on disk. Exact command duplicates retrieve the
   original attribution/result; changed command content conflicts. Worker/revision
   attribution is excluded using Interceptor's command-fingerprint recipe.

An attempt cannot resolve successfully while its native steps are pending,
unknown or unsuccessful. Unknown parent resolution is allowed during teardown;
it does not make unresolved native steps executable again.

## Bounds and recovery

Each native frame is at most the client's 5 MiB envelope limit. Exact frames are
split into ordered journal members of at most 4 MiB; native uint64 fields and JSON
serialization are not converted into the harness's JCS number domain. Member
hashes and sizes are verified before reuse. `Response.Bytes` retains the original
received envelope; a manually constructed response has no trusted wire bytes.

A step separately reserves five maximum event lines, two maximum response frames
and a 64 KiB reporting query. Its initial request is charged separately. This
covers dispatch, completion and one read-only reconciliation exchange, including
unknown outcomes. It does not reserve physical disk blocks or replace campaign
quotas/free-space checks. Known completion releases unused capacity; unknown
completion retains capacity for the reporting query.

`BeginReconciliation` accepts only a fresh `operation.status` query addressing the
saved command's campaign/session/operation. It commits the read before contact,
using the existing reservation even after terminal fencing. One such query is
allowed per unknown step. Repeated delivery retrieves the saved query/result;
it never renews the deadline or performs another read. `ResolveReconciliation`
retains the response and releases unused capacity without changing the original
unknown outcome. Further administrative evidence collection belongs to recovery.

Recovery uses the existing journal inspector. Interrupted intents, dispatches and
unknown completions remain unknown. Reconciliation evidence cannot reopen work.
The independent Docker observer must be armed by the runtime before admission;
these journal methods signal its fence but do not themselves terminate Docker.
