# Immutable host input staging

Status: implemented in `internal/staging`. This stage copies prepared inventory
bytes into service-owned directories for the three read-only Docker mounts. The
campaign preparation CLI, input admission, launch binding and Docker mount wiring
are implemented callers. Shared Operator/Attack Harness schemas are unchanged.

## Preparation interface

Use a protocol from the [installed contract loader](INSTALLED_CONTRACT.md).
`staging.Create(ctx, protocol, parent, manifests, contents)` requires:

- An existing private `0700` parent directory owned by the executing service user.
- The complete InputTreeManifest, SkillSetManifest and ordered per-skill manifests.
- Exactly the inventoried input/skill bytes, keyed by `input/<relative path>` or
  `customer-skills/<skill_id>/<relative path>`.

Choose the parent within the run/campaign's registered retention scope and keep
each stage exclusive to that campaign. Campaign association and eventual purge
must include this tree; it is not a shared cross-campaign content store.

The caller constructs and admits the campaign's context, bundle, effective prompt
and exact selected skill set before supplying these bytes. The stager requires a
verified package pin, validates the manifest set and its bounds, checks canonical
per-skill object/loading digests, and checks every file's actual length and raw
SHA-256 against its inventory. Extra or missing content keys fail. The caller must
not mutate buffers concurrently with `Create`; subsequent buffer changes do not
affect the staged files.

`staging.Capture(ctx, absolutePath, maximumBytes)` supports bounded capture of an
explicitly selected host source. It rejects symlinks at the selected file, hard
links, special files and oversized or observably changing content. It reads only
regular files, checks the opened inode, and bounds the actual read. Source ancestor
directories are trusted host paths. It copies no ownership, mode, link, timestamp or
extended metadata. The caller must verify the captured bytes against independently
selected inventory/digest requirements; the capture helper alone does not authorize
content. Generated context/manifests may be provided directly as frozen bytes.

## Resulting filesystem

Each call exclusively creates a randomly named `stage-*` directory:

```text
stage-<random>/                         0700, private host wrapper
  staging-receipt.json                  0600, host-only completion metadata
  input/                               0555
    run-context.json                   0444
    scenario-bundle.json                0444
    system-prompt.txt                   0444
    artifacts/sha256-<hex>              0444, when selected
  customer-skills/                      0555, present even for an empty selection
    <skill_id>/SKILL.md                 0444
    <skill_id>/<selected reference>     0444
  manifests/                           0555
    input-tree.json                    0444
    skill-set.json                     0444
    skills/0000.json ... 0015.json      0444, exactly the selected slots
```

Only the three child trees are mount sources. The private wrapper, receipt and
source files are never mounted into the guest. Each output is a new regular file;
the stager does not link source inodes, copy executable metadata or create mounts.
All nested output directories become `0555`. Kernel/filesystem name collisions or
unsupported names fail preparation; no path rewriting is permitted.

The existing shared limits govern allocation and writes: 64 MiB input content,
64 MiB aggregate selected skill content, 8 MiB per skill and 40 MiB aggregate
manifest bytes, plus the existing per-file/count/depth limits. Inventory validation
happens before filesystem creation, and the bounded selected bytes are copied into
independent buffers. Reads/writes check cancellation between bounded chunks.
Filesystem syscalls remain synchronous and require a responsive host filesystem.

## Completion, verification and cleanup

Files are written exclusively, set to their final modes and synced. Directories
are then sealed and synced. The host receipt is written and synced under a temporary
name, published with an exclusive hard link, and reduced to one link before the
stage and its parent are synced. Its API version is
`operator.dev/input-staging/v1alpha1`; it records the verified package identity,
canonical input-tree/skill-set digests, staged file count and byte total. Counts
include manifest files and exclude the receipt itself. No source paths enter the
receipt or guest manifests.

`Create` returns a `Tree` only after `Tree.Verify(ctx)` confirms exact file/directory
names, ownership, modes, sizes, hashes and the original wrapper inode. Unexpected
entries, missing files, links, changed bytes or permissions fail. Verification uses
bounded directory batches and pinned child-directory handles. The receipt's mere
presence is never sufficient launch authorization.

`Tree.Directory()` returns the private host path; `Tree.Receipt()` returns a value
copy. The caller must re-run `Verify` before Docker exposure and link the frozen
inputs to its private RunManifest and startup binding using the existing
`ValidateLaunchInputs`/`ValidateLaunchIdentities` checks. Service ownership must
prevent host-side modification for the entire harness lifetime; read-only mounts
do not prevent the host owner from changing backing files.

Failed creation attempts remove their own partial directory. If cleanup fails, the
error identifies the retained stage path and no usable `Tree` is returned. A host
crash can leave an orphan; startup recovery must treat unbound stages as cleanup
work, never resume them or infer campaign acceptance from a receipt. [Startup recovery](STARTUP_RECOVERY.md) now verifies durable campaign association
and cleans abandoned transient resources without resuming execution.

`Tree.Discard()` verifies the wrapper inode, restores directory write permissions
and deletes that exact tree. Call it only before mounting or after independently
confirmed container exit. Staging itself has no Docker binding and cannot establish
that lifecycle condition. Healthy target restores keep the same staged inputs and
manifest mount, alongside the existing harness process.

## Validation scope

Tests cover selected/empty skills, fixed manifest slots and modes, source-buffer
independence, exact hash/size checks, canonical manifest identities, extra/missing
content, capture limits and special-file rejection, on-disk tampering, cancellation,
private-parent requirements and partial-tree cleanup after a filesystem write
failure. Small test input documents exercise storage integrity, not campaign
admission. Passive-skill policy/signature verification, release prompt binding,
target capability admission, guest readability under Docker and full host/runtime
qualification remain independent gates.
