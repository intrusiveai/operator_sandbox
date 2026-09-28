# Startup container reconciliation

`internal/startup.Acquire` returns an installation-wide worker lease only after
prior campaign execution has been reconciled. The new worker MUST retain this
lease through finalization. Acquisition neither resumes an old campaign nor opens
its journal for writing.

For each private campaign group:

1. Inspect the journal without waiting for an active writer. An active writer
   blocks acquisition, including one created by a caller without the host lease.
2. Read the saved exact Docker binding independently of journal health.
3. A complete journal without a launch start intent and without a Docker binding
   establishes that this preparation never reached container creation. Missing
   identity after a start intent requires the discovery procedure below. Damaged
   evidence without identity MUST block acquisition.
4. Query the saved local Docker endpoint and verify its daemon ID. List all
   container states using the full saved container ID, with nontruncated IDs and
   a fixed output format. An empty successful result followed by a matching
   daemon check establishes current absence. A failed inspect/list, abbreviated
   or unexpected ID, malformed reply, daemon change or timeout is uncertainty.
5. For a present container, verify the full ID, image, required labels and restart
   policy. An active orphan is terminated through the existing independent
   termination service and its emergency records. A live campaign writer is
   never treated as an orphan.
6. Remove an independently confirmed inactive container without force or volume
   deletion, then verify exact-ID absence again. Unconfirmed stop/removal blocks
   the new worker.

Returned per-campaign records distinguish journal integrity, never-created state,
current container absence and any new termination receipt. A damaged journal with
a valid independent binding can still have its container stopped; its bytes are
preserved and its integrity remains false. The caller MUST retain these recovery
results with a subsequently accepted launch. Neither current absence nor a
successful kill establishes historical native outcomes or complete evidence.

The read-only `dockercontrol.CheckInactive` probe does not change administrative
termination semantics: a failed inspect still cannot be reported as a confirmed
kill. Administrative termination MUST continue to require a saved full binding;
it MUST NOT discover replacement identities while an execution worker is active.

## Lost create replies

When the binding is absent after a start intent, the startup gate MUST use
`campaign.ReadCreateIntent` to require a complete verified journal and exactly one
start intent at the manifest's initial revision. The intent's image MUST match the
manifest. Its saved local endpoint and daemon identity MUST pass validation.

`dockercontrol.ResolveCreate` MUST query that daemon using all three fixed
campaign, launch and logical-container labels, all container states and full IDs.
Labels only select candidates. The response MUST contain exactly one full ID;
inspection MUST independently verify its image, required labels, disabled restart
policy and disabled auto-removal. The daemon identity MUST match before discovery
and after inspection. No container name or current Docker context participates.

An empty listing MUST remain unresolved: it cannot establish that an interrupted
create will never finish. Multiple matches, malformed replies, missing candidates,
changed identities, cancellation and lookup failures MUST also block admission.
Discovery MUST NOT create, start, retry, kill or remove a container.

Before cleanup, `campaign.SaveRecoveredDockerBinding` MUST take the campaign writer
lock, verify the complete journal again, match the binding to the original intent
and manifest, and atomically publish the absent `launch/docker-binding.json`.
Conflicting, corrupt and partial publications MUST remain untouched. Exact repeats
are idempotent. The original journal MUST remain unchanged. This binding grants
cleanup identity only; it does not authorize campaign restart.

The gate MUST then use the existing exact-ID termination/removal path and confirm
absence. If cleanup fails, the recovered binding MUST remain available for later
administrative termination or startup cleanup. The returned recovery row MUST set
`binding_recovered: true` when that acquisition recovered the identity. Subsequent
acquisitions MUST use the saved binding directly.

If Docker create itself returns a complete full ID alongside an error, the launcher
MUST retain the binding for cleanup and MUST fail the launch. That error MUST NOT
grant `StartCreated` permission, even after the binding is saved.

## Transient filesystem cleanup

After exact container absence (or verified never-created preparation), the gate
MUST acquire the campaign writer lock and reclaim `runtime/transport` and
`launch/policies`. It MUST select only these fixed campaign-relative namespaces;
journal/spool payloads cannot supply deletion paths. Cleanup MUST preserve the
first `transient-cleanup-intent.json` and publish the bounded latest result in
`transient-cleanup-result.json`, independently of the original journal.

The traversal MUST read directory names in batches of 128, check cancellation,
limit each tree to 100,000 entries and depth 128, and share a 30-second pass
deadline. It MUST NOT read spool bytes, follow links, cross filesystem devices or
remove a replacement root. Child links and special files MUST be unlinked without
opening their targets. Cleanup failure MUST retain remaining resources, report
uncertainty and block new admission; later startup can continue filesystem cleanup.

`launch/stage-<26 alphanumeric characters>` directories MUST be removed only when
a complete verified journal contains one matching `campaign.launch-inputs-retained`
completion record. Otherwise input copies MUST remain `retained-unarchived`.
Cleanup MUST leave journals, artifacts, submitted source inputs, reports, bindings
and unrelated launch entries unchanged. It MUST never reopen transport or replay
remaining messages. Returned recovery rows MUST include the cleanup result.

## Native target finalization

Installed startup MUST call `Gate.FinalizeNative` while retaining the installation
lease, after Docker/transient reconciliation and before preparing a fresh campaign.
`internal/nativerecovery` MUST hold the old campaign's writer lock and consume only
a complete verified journal. Damaged evidence MUST produce an explicitly
unconfirmed native outcome without target contact. Existing terminal results MUST
be retained as previous finalization outcomes, including any uncertainty.

Recovery MUST reconstruct the initial Interceptor instance, session, capability,
environment, application and administrator target-stop pins from retained
preparation. Recovery MUST advance the cleanup binding only through journaled
`state.replacement-verified` records; an unrecorded replacement or a merely different
current session MUST NOT authorize a new cleanup binding.
Fresh API status and native session status MUST match the selected identity and
policy before mutation. Recovery MUST NOT attach, restore, invoke the target,
generate model requests, or reopen native/harness admission.

Each campaign MUST have at most one automatic native recovery pass, with a shared
30-second deadline. A durable `native-recovery/intent.json` MUST claim that pass
before contact. Repeated startup MUST read its saved outcome; a claim without a
result remains unknown and MUST NOT dispatch again. Missing intent alongside other
recovery files MUST be treated as damaged evidence, not as a new claim opportunity.

The pass MUST confirm native execution closure before cleanup. If closure was
previously attempted, it MUST use current status to confirm it or retain uncertainty,
without submitting a replacement close. Cleanup MUST use only IDs from successful
journaled `injection.arm` operations, at most 64 per pass. Historical successful
deletions do not erase those candidates because a restore can reintroduce them.
Previously dispatched terminal cleanup or unresolved ordinary deletions MUST NOT
be repeated. A lost new deletion reply MUST end the deletion loop. Confirmed counts,
remaining candidates and bounded/unconfirmed/not-retried states MUST remain explicit.

Target stop MUST require the original administrator opt-in and confirmed closure.
A previously attempted stop MUST NOT be resent. Already stopped status may confirm
stopping, but MUST NOT imply injection cleanup or evidence collection succeeded.
Remote unavailability or changed identity MUST be reported as unconfirmed; no
fallback target or current configuration may substitute for the saved binding.

Recovery MUST preserve the original journal and write separate private audit files:

```
native-recovery/
  intent.json
  step-001.json
  ...
  result.json
```

There MUST be at most 140 step records of at most 512 KiB, each containing a bounded
request, response or fixed failure reason. Intent publication MUST precede each
external mutation. Any record failure MUST prohibit subsequent dispatch from that
handle. The final result MUST bind the step inventory by digest; subsequent reads
MUST verify every step and reject missing, changed, extra or partial records.
These bounded recovery records are separate from the closed execution journal's
budget. Before claiming the pass and before every step publication, recovery MUST
check free space for its remaining audit allowance (70 MiB plus two 256 KiB
metadata files) and the original journal's minimum-free-space floor. This logical
reservation does not prevent other host processes from consuming storage. Any
later write failure remains explicit uncertainty. Recovery records belong to the
same independently purgeable campaign group.

This pass records target closure/cleanup/stop. Late native evidence collection,
report publication and explicit administrator-directed reconciliation remain later
work. No recovery outcome grants execution permission.

Tests use real campaign files/locks and scripted Docker replies. They cover live
writers, orphan termination, previously removed containers, damaged evidence,
lost-create discovery, no-match/multiple-match uncertainty, preserved identity
after cleanup failure, daemon mismatch, failed kill/removal, invalid directory
entries and lease release on failure. Live Docker recovery qualification remains
part of runtime validation.
