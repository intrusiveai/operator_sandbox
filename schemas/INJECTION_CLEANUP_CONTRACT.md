# Explicit injection cleanup contract

Status: accepted container contract. Operator cleanup dispatch and Attack Harness
tool handling are implemented; complete live integration qualification is outstanding.
The closed request/result schemas are registered in `catalog.json`;
these semantic rules also require host validation and package conformance tests.

## Tool and wire shape

The model tool `injection_delete` wraps `engine.injection_delete`. It removes one
known setup injection from the currently bound target without an application
invocation, snapshot restore or harness restart. Advertise it when the target
adapter supports injection deletion and campaign policy permits it. Its arguments
are independent of feedback profile: deleting a declared action does not require
diagnostic/oracle feedback access.

Request body ([schema](engine-injection-delete-request.schema.json)):

```json
{"attempt_receipt_id":"attempt-receipt-07","action_id":"replace-ticket"}
```

The pair is the action handle. `attempt_receipt_id` is the host's existing
EngineAttemptResult.receipt_id; `action_id` is the exact ID the harness supplied
in that attempt's `pre_actions`. No additional discovery call or raw native ID
is needed. The shared envelope supplies campaign, current revision, operation ID,
call ID and timeout. There is no nested request ID, campaign/session override,
injection ID, arbitrary native operation, selector, payload or bulk-delete mode.

Successful result ([schema](engine-injection-delete-result.schema.json)):

```json
{"receipt_id":"cleanup-receipt-08","attempt_receipt_id":"attempt-receipt-07","action_id":"replace-ticket","outcome":"deleted"}
```

`outcome` is `deleted` or `already_absent`; both confirm that the addressed
injection is absent in the operation's recorded target session/revision. The
result echoes both action-handle fields, and its new receipt refers to the cleanup
record. Removal stops future matching/reapplication; it does not undo application,
file, managed-service or other effects already produced. Use a checkpoint restore
when the desired action is target rollback.

## Host resolution and dispatch

1. Resolve the attempt receipt in the same campaign and find the declared
   `interceptor.injection/v1alpha1` action. Require a host record of its native
   creation, not merely model claims, a guessed ID or an unexecuted setup entry.
   Missing/foreign receipts, unknown actions and never-created actions produce
   `ACTION_UNAVAILABLE` without target contact. This handle validation identifies
   the requested resource; creator worker/revision is not an access restriction.
2. During attempt delivery, persist the mapping from receipt/action to native
   injection ID and creation intent before arm dispatch; record the creation
   operation and known/unknown outcome before returning the attempt receipt. Reserve each generated injection ID across the campaign;
   never reuse it for a different action, even after deletion. Preserve these
   records across restores and worker attribution changes. A creation with an
   unresolved outcome follows terminal reconciliation rules, not guessed cleanup.
3. Serialize cleanup through the ordinary operation gate, including against
   snapshot creation/restore and attempts. Resolve the active target binding and
   journal cleanup intent, source handle, native injection ID, target binding and
   attribution before dispatch. The adapter submits native `injection.delete`
   with body `{"injection_id":"<host-resolved-id>"}` and the current campaign,
   worker/run attribution and native expected revision. It needs no original arm
   record or attempt-context fields in the restored native execution store.
4. Native `204` maps to `deleted`. Only the bound native
   operation result `404` with code `not_found` for this exact resolved deletion
   maps to `already_absent`. A missing route/session, generic transport 404,
   stale binding, storage failure or missing host mapping is not proof of absence.
   Interceptor must distinguish missing injection from journal/persistence errors.
5. Persist the cleanup result and receipt before returning. Apply ordinary request
   quotas and the 30-second operation ceiling, bounded by campaign time. Cleanup
   is not a new attempt or target application invocation. It may not delay urgent
   termination or reopen a failed campaign; post-closure cleanup is host-owned.

## Restore and duplicate behavior

Earlier same-campaign handles remain valid after a healthy restore. Keep the
campaign mapping even when the selected checkpoint predates the action. Cleanup
always addresses the current target, not the historical source session. If that
checkpoint contains the injection, delete the restored instance under the same
native ID; if it excludes it, native confirmed absence is successful cleanup.
No ownership transfer, original-worker match or creation-revision match is needed.

A snapshot may also resurrect an injection deleted after that snapshot was made.
Never use a previous revision's cleanup receipt/tombstone as proof of current
absence. A new cleanup intent after restore uses a new operation ID and current
revision, even when its action handle is unchanged. Exact duplicates of an old
operation return its saved historical result and must not silently apply to the
new target. The original attempt result and frozen feedback remain immutable;
append a separate cleanup record rather than rewriting their historical state.

Bind duplicate identity to the canonical body and the original effect target
under the shared contract. Same operation ID and unchanged command returns the
saved result without another effect. Changed handle content conflicts. A fresh
operation in the same healthy revision may return `already_absent`. Unknown
outcomes preserve the original native operation and request for host status
reconciliation; never retry a new deletion ID merely because a response was lost.

## Errors and acceptance checks

Use the shared bounded error envelope. Codes are `INVALID_ARGUMENTS`,
`ACTION_UNAVAILABLE`, `UNSUPPORTED_CAPABILITY`, `POLICY_DENIED`,
`IDEMPOTENCY_CONFLICT`, `STATE_CHANGED`, `CLEANUP_FAILED` and `OUTCOME_UNKNOWN`.
Invalid arguments/unavailable handles/unsupported capability have `effect_state:
none` and may return `correct-and-resubmit`; policy rejection follows host policy.
Protocol/binding faults and execution failures follow the shared terminal rules.
Deletion storage failures are `CLEANUP_FAILED` with `disposition: terminate` and
conservatively unknown effect state unless reconciliation establishes the outcome;
transport uncertainty is `OUTCOME_UNKNOWN`, `effect_state: unknown`, `disposition:
terminate`. Never manufacture a success from an error. Host reconciliation and
bounded cleanup remain available without resuming the harness.

Conformance must cover retained-action removal without invocation/rollback;
auto-cleaned or previously deleted actions; restored presence and absence;
restore resurrecting a previously deleted action; earlier worker/revision handles;
foreign/unknown/unexecuted handles; unchanged replay versus changed-handle
conflict; failure/timeout with no subsequent guest effect; native storage failure
not becoming absence; and success-result handle correlation. Shared Go/Python
fixtures must include these semantic cases before package publication. Structural
positive/negative vectors are in `fixtures/injection-cleanup.json`.

Run structural checks with `python3 schemas/validate_cleanup_fixtures.py`
from the Operator directory, with the `jsonschema` package installed. This does
not replace the shared semantic/broker suites or complete process/native
qualification. See [implementation status](../docs/IMPLEMENTATION_STATUS.md).
