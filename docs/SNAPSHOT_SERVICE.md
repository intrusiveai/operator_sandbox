# Snapshot and restore service contract

Implemented in `internal/campaignservice`, using the existing Interceptor client,
campaign tool ledger, immutable preparation and attempt adapter. This stage adds
`engine.snapshot_request`, `engine.snapshot_list`, `engine.snapshot_inspect` and
`engine.restore_request` to the service's advertised operation set.

## Dispatch and persistence

State routes MUST share the ordinary gate and operation-ID namespace with attempts,
feedback reads and injection cleanup. A duplicate MUST replay its retained result;
changed command content MUST conflict. A new request MUST address the current
revision; a recorded duplicate MUST preserve its original result when submitted
from a later revision. Worker/revision fields remain attribution.

Before native mutation, the service MUST retain the request, absolute deadline,
admission and native command intent. It MUST reserve native audit capacity in
addition to tool-result and exact-wire-response reservations. The initial native
audit reservation is 2 MiB per state operation, released after completion. Native
mutation/status responses MUST fit the existing 256 KiB audit bound. Required
persistence failure MUST fence execution and independently terminate the harness;
no unretained transition response can be published.

Snapshot creation/restore MUST use the smaller of the requested timeout, 300 seconds
and the remaining absolute campaign time. Other state requests MUST also observe
the private profile's ordinary-operation timeout. No step or retry can renew the
original transport deadline.

## Creation and discovery

Creation MUST require the verified snapshot capability and a positive remaining
count/byte allowance. The host MUST durably charge one admission before dispatch;
an admitted failure MUST retain that count. The exact native `snapshot.create`
command MUST carry `maximum_committed_bytes` from the campaign ledger. A successful
hash-verified checkpoint receipt MUST charge its canonical commitment bytes once.
Restore, deletion and duplicate delivery MUST NOT refund those charges.

A precise native `snapshot_bytes_exhausted` response followed by a fresh healthy
original binding MUST return a bounded budget error without charging commitment
bytes. Other unconfirmed snapshot outcomes MUST remain terminal; absence of a
reply is not evidence that no checkpoint was committed.

Listing MUST expose pages of at most 100 snapshots and explicit pagination.
Inspection/restoration MUST use the source-session/checkpoint pair. The native
client MUST validate hash-covered metadata and campaign scoping, including retained
source sessions. The host MUST retain native metadata and expose only the public
schema's campaign, source/checkpoint, label, description, creation time, status and
canonical size fields. Legacy campaign metadata MUST remain unchanged in native
evidence; public campaign association comes from the scoped query. A parent handle
MUST be omitted when its source session cannot be established.

## Healthy restore

The service MUST serialize restore with ordinary work while preserving the harness,
Docker binding, scratch, manifests and transport. It MUST mark a planned transition
before lifecycle dispatch. The idle watcher MUST ignore status samples during that
transition and discard samples of a superseded binding. The absolute timer and
independent Docker termination observer MUST remain active.

Only the precise native preflight rejection
`checkpoint_unavailable_or_incompatible`, followed by fresh confirmation that the
original binding remains healthy/open, permits continuation at the old revision.
Lost replies, changed identity, malformed lineage, post-closure failure and journal
loss MUST close execution. No new restore operation can be used to guess an outcome.

Successful restore MUST verify the receipt against the original session, selected
source/checkpoint, process instance, capabilities, environment, application and
feedback profile. It MUST recheck replacement readiness, owner policy and bundle
compatibility. It MUST retain the result and commit the new target binding before
returning `harness_disposition: continue`. The response envelope MUST echo the
request's old revision; its result contains the incremented revision. The response's
audit record belongs to the current journal revision. A duplicate MUST neither
restore again nor increment the revision again.

## Lineage and counters

Campaign attempt numbering, original receipts, injection handles and cumulative
allowances MUST survive restore. Remaining limits MUST account for admissions,
retained artifacts, feedback charges, snapshot commitments and elapsed time.

For checkpoints created by this service, the host MUST retain its verified native
parent/turn inventory at the checkpoint boundary. Restore MUST discard later native
lineage and rebind that inventory to the replacement session. When a requested
parent is verified only in campaign history and cannot be verified in the restored
registry, the adapter MUST preserve the original
harness request/generation in the journal while creating a native root at generation
one. Subsequent native children MUST follow native generation ordering; independent
harness generation ordering MUST still be checked. Guest assertions alone MUST NOT
establish a parent, thread or known turn.

The shared contract requires the harness to skip later calls from the model response
containing a successful restore. This host stage MUST preserve the correlated
transition result; the harness dispatcher MUST produce explicit skipped-call
results. No extra revision acknowledgement is required.

## Validation boundary

Tests MUST cover cumulative limits, duplicate/conflicting IDs, source metadata,
replacement lineage, known rejection, lost replies, journal loss, restored/rebased
parents, planned transitions and independent stop during restore. Physical spool
tests MUST continue on the same launch with increasing channel sequences through
restore and subsequent attempts.

These tests use real host journals, shared validators and file spools with scripted
native/Docker peers. Artifact upload, model relay, assessment/conclusion/stop
dispatch and launcher/control integration are implemented. Complete process/native
qualification remains outstanding.
