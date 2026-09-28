# Campaign retirement and purge

## Inventory and exclusion

The purge implementation inventories complete campaign groups rather than selecting
individual evidence files. Campaign groups include every revision, input copy,
journal/content object, artifact, native archive, evidence adoption record and
report generation. Separate early attachment and start-request groups belong to
the same campaign. Interrupted deletions retain a private purge plan that binds
original directory identities and the exact Docker binding for retry.

Preflight MUST acquire the installation's exclusive retention lease and host
execution lease, then retain campaign/attachment writer locks and existing run
selection locks until the operation ends. Live workers, submissions, preparation,
report/export generation and evidence reads MUST block purge. The MVP conservatively
serializes retirement across an installed state root. Purge MUST NOT stop active
work to acquire these locks.

A saved Docker binding MUST be checked against its original endpoint, daemon,
full container ID, labels and lifecycle policy. An inspect failure MUST NOT mean
absence. Missing Docker identity requires an intact journal proving no create
intent; lost-create identity or damaged launch evidence requires cleanup/reconciliation
before purge. A retained start record or attachment alone does not imply that a
container was created.

`--all` MUST preflight its entire selection before deleting evidence. Scans MUST
be bounded, avoid reading file contents, unlink rather than follow interior links,
and reject mount boundaries. Reusable configuration, credentials, skills, images,
release caches, submitted input projects and external exports MUST survive purge.

## Registered managed copies

A host producer MAY use `purge.BeginManagedCopy` to create a dedicated
`<run-output>/operator-campaigns/<campaign-id>` subtree. It MUST retain the returned
handle while writing. The API records a private per-campaign registration before
accepting content and places a matching `managed.json` identity in the copy.
Preflight MUST verify both records. A missing or conflicting marker MUST block
removal; an arbitrary path in campaign or harness input grants no deletion authority.

The registry is stored under `state-root/managed-copies/<campaign-id>`. Purge MUST
remove each registered subtree and its registry, preserving the run-output parent
and unrelated entries. Explicit `operatorctl export --output` copies are
administrator-owned and MUST NOT use this registry. They survive campaign purge,
including incomplete exports left by an interrupted export process.

Deleting retained evidence removes the ability to generate new reports or read
its retained feedback/checkpoint exports. It does not delete Interceptor's own
session data or independent administrator exports.

## Commands and durable deletion

```sh
operatorctl purge --campaign <campaign-id> [--config PATH] [--state-root DIR]
operatorctl purge --all [--config PATH] [--state-root DIR]
```

Exactly one selector is required. `--docker-bin` MAY select an administrator-owned
Docker executable; Docker is only resolved when the selected inventory contains
a saved container binding. Neither command requires model credentials or native
target connectivity. Purge MUST NOT call Interceptor's purge API.

After whole-selection preflight, purge MUST durably retire each start request and
confirm its exact service registration is absent. Confirmed inactive Docker
remnants MUST be removed by the saved full binding, then confirmed absent, before
retained filesystem data is deleted. Active containers MUST be refused, never
implicitly stopped. A manager/Docker failure MUST preserve all retained evidence;
a newly written retirement fence remains in effect for explicit retry.

Before deleting any selected evidence, purge MUST durably publish each campaign's
`purges/<campaign-id>/plan.json`. The plan binds original directory device/inode
identities, start IDs/digests and Docker identity. On retry, this record supplies
identity even after ordinary start or journal files have been removed. A replaced
directory or changed/unconfirmed Docker state MUST block deletion. An incomplete
unpublished plan MUST NOT authorize deletion; retry requires fresh preflight.

While a purge transaction exists, new start/preparation records and managed-copy
registrations for that campaign MUST be refused so they cannot escape the saved
inventory. Reads, reports and explicit exports MAY use remaining intact evidence
between purge invocations, under the existing exclusion locks.

Purge MUST remove the retry plan last. It MUST report removed, already-absent and
partial groups, returning exit 1 on failed preflight or incomplete deletion and
exit 2 on invalid arguments. Successful removal, including an already-absent
campaign, returns exit 0. Results use `operator.dev/purge-result/v1alpha1` and MUST
include the selected campaign IDs, outcome, fixed reason code and the consequence
of losing retained evidence. No age-based deletion or automatic retry is introduced.

Submitted run directories remain input projects. Their `start.json` lookup hints
MUST remain intact after start records are purged: observation reports the missing
record, and a later start requires explicit `--new-campaign`. A dangling lookup
MUST NOT silently become permission to repeat the old campaign.

For a refused `--all` preflight, `selected_ids` is a best-effort read-only inventory
and `selection_complete` MUST be false. After locked whole-selection preflight it
MUST be true. Fixed refusal codes distinguish busy resources, unconfirmed Docker
inactivity, unconfirmed launch identity and invalid/incomplete inventory. Deletion
results identify each successfully removed or partially removed group.

## Validation boundary

Tests cover pre-claim and interrupted preparation, a claimed worker killed without
cleanup, incomplete retirement publication, whole-selection refusal, exact saved
Docker identity, unconfirmed termination, directory replacement, active report/read
exclusion, imported native archives, report generations, managed copies, partial
filesystem deletion/retry and preserved complete/incomplete external exports.
Scripted manager tests cover Linux and macOS removal/absence semantics. The opt-in
native macOS test registers a LaunchAgent without starting its worker, retires it,
confirms deregistration and rejects a delayed real worker invocation. These tests
do not qualify production Docker execution or the full four-host runtime matrix.
