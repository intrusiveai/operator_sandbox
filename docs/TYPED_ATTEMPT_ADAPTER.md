# Typed Interceptor attempt adapter

`internal/attemptadapter` connects validated shared attempt requests to the durable
native executor. The [campaign service](CAMPAIGN_SERVICE.md) now supplies its live
callbacks; campaign start CLI and Docker launch remain separate work.

## Preparation

1. Obtain a verified live capability binding.
2. Resolve administrator-selected operations, concrete injection targets,
   scopes/placements/pointers, merge fields, optional caller identities and
   retention permission with `Resolve`. Evidence kinds are explicitly allowed.
   The resolver checks native capability facts and produces the capability policy
   for bundle admission. A published injection family alone grants no selector.
3. Run bundle compatibility admission using that policy.
4. Journal the identified attempt submission before tactical validation.
5. Call `Compile` with the request and trusted, campaign-associated artifact bytes,
   current-session parent lineage, scenario IDs, release pin, timestamps, native
   revision and remaining feedback allowance. Compilation performs no I/O.
6. Admit `Plan.RecordJSON` and mark the parent dispatched. Create an execution
   using that ledger and the pinned native/Docker guard. The constructor verifies
   the saved plan, original request identity and target provenance.

The resolver consumes concrete host scope records from the private
[TargetProfile](CAMPAIGN_PREPARATION.md). Preparation resolves those scopes and
publishes the effective harness context. These records are host policy inputs,
not additional model arguments or a new public wire contract.

## Translation and execution

Compilation verifies the installed request schema, exact artifact digest/size/media
and canonical bytes, action uniqueness, scenario/release attribution, current native
parent lineage, selected routes and input delivery schema. It fixes the order of
artifact registration, attempt registration, declared injections and one invocation.
Narrative fields never choose a tactic or repair arguments.

Native capability placement names (`append`, `prepend`, `merge`, `insert`) map to
the request vocabulary (`append_text`, `prepend_text`, `merge_object`,
`insert_array`) during permission checks. Native injection definitions retain the
request vocabulary. A carrier is sent as verified inline invocation input while
`payload_digest` retains the attack payload's identity; Interceptor's native
artifact shortcut cannot name a distinct carrier.

Every step receives a deterministic operation ID, frozen deadline, exact body
digest and expected native revision. The installed interpreter verifies typed
operation receipts rather than accepting HTTP success alone. The next step is
prepared only after the executor commits the previous result. Native/effective
feedback profiles remain distinct, collection uses the exact returned turn, and
bounded content reads feed the verified feedback projector before cleanup.

`Run` is single-use. Known failures and uncertain/malformed results fence execution
and prevent further experimental dispatch. Product-authored guest errors expose no
native error text. The returned injection handles record only confirmed creations
and deletions. Retained injections remain associated with their campaign and
attempt/action handles, without worker/revision ownership restrictions.

## Publication and terminal integration

The result contains schema-validated guest JSON, an immutable feedback receipt,
confirmed injection handles and durable native step IDs. The caller must commit
receipt mappings, all retained feedback bytes and the final attempt result before
replying to the harness. The [attempt broker](ATTEMPT_BROKER.md) now performs this
publication and implements the `engine.injection_delete` tool route.

After terminal failure, the ordinary executor stops. Its confirmed-created handles
are available to the separate bounded terminal-cleanup controller; `Run` does not
reopen execution to perform cleanup or collect additional failure feedback.
The campaign service wires independent termination, native closure and bounded
terminal cleanup. Reporting-only evidence provenance and publication remain pending.
Final attempt receipt publication is performed by the broker before any reply.

Tests exercise all these translations and failure boundaries against a scripted
native peer with the real campaign journal and executor. They check current native
revisions, multi-chunk feedback, optional unavailable bytes, empty selection,
retained actions and failure fencing. This qualifies host protocol behavior, not
a running Docker/Interceptor deployment.
