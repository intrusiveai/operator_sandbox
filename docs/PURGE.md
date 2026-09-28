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
