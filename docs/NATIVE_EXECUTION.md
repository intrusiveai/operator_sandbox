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

A step separately reserves six maximum event lines, two maximum response frames
and a 64 KiB reporting query. Its initial request is charged separately. This
covers live-binding preflight, dispatch, completion and one read-only reconciliation exchange, including
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

## Executor and live guard

`internal/nativeexec` joins that journal to the fixed-loopback Interceptor client.
`NewGuard` freezes the native process/session/revision pin and exact Docker binding.
Before each fresh effect it checks Interceptor readiness and the original process
instance, then uses `dockercontrol.CheckRunning` to verify the harness's daemon,
full container ID, image, labels and lifecycle settings. Worker IDs remain
attribution. The guard's normalized live-binding proof is retained before dispatch.
The Docker check neither starts nor repairs a container.

`New` verifies that the supplied Docker binding matches the identity already
persisted in this campaign. It also requires an installed `Adapter`. Its `Authorize` method verifies exact
membership in the admitted plan and current policy; its `Interpret` method checks
operation-specific receipts and outcomes. These are mandatory trusted-code hooks,
not guest callbacks. The executor does not supply a permissive default or infer
success from HTTP status. The [typed attempt adapter](TYPED_ATTEMPT_ADAPTER.md)
now supplies these checks for compiled Interceptor attempts and feedback reads.
The [attempt broker](ATTEMPT_BROKER.md) also supplies exact-command handling for
retained-injection cleanup. That parent can dispatch only one resolved native
deletion. Native 404 `not_found` is successful only for this typed deletion path;
generic transport errors cannot establish absence.

`Execute` obtains the one-time durable dispatch grant, checks the terminal fence
again after persistence, and sends the exact saved envelope with a bounded context.
The fence cancels an in-flight call independently of the journal. Lost/malformed
replies, 202, 5xx and native unknown-outcome markers are unknown even if an adapter
would claim success. Known execution failure is also terminal. A client-confirmed
pre-send cancellation is recorded as not dispatched, but does not permit the
admitted plan to continue. Known late results can remain evidence; a stopped fence
never reopens.

Results are host-only. Each response must be durably committed and operation-
validated before use; it still needs the typed feedback projection before reaching
the harness. Concurrent duplicates return the committed result or an explicit
pending error and do not join/restart another effect.

`Reconcile` uses the saved unknown step, the original session and one persisted
`operation.status` query. Its reporting guard requires the original process and
retained session, but does not require healthy execution or a running harness.
It validates native command fingerprints and record identity before returning an
operation record. A completed native record is evidence only: the original host
step and attempt remain unknown and execution remains closed.

The launcher still needs to arm `termination.Service.Watch` before admission and
route closure/cleanup through the lifecycle controller. This executor signals and
observes the shared fence; it does not itself bootstrap a container or claim that
those service-lifetime integrations are already present.
