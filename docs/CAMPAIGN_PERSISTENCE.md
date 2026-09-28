# Host campaign persistence

Status: implemented in Go under `internal/campaign`. This package provides
persistence; Docker launch, tool dispatch and harness orchestration are implemented
in its [host worker](HOST_LAUNCH.md) and [campaign service](CAMPAIGN_SERVICE.md) callers.
The shared host/harness wire contract remains separately versioned.

## Immutable launch records

`RunManifest` uses `api_version: operator.dev/run-manifest/v1alpha1`. Its closed,
required fields are defined by `internal/campaign/manifest.go`; `ParseManifest`
rejects duplicate, unknown, missing and invalid fields. It is host-private and is
not a new guest input file. The complete canonical JSON is limited to 256 KiB.

| Fields | Meaning |
|---|---|
| `campaign_id`, `launch_id`, `container_id`, `initial_revision`, `created_at` | Immutable launch identity and canonical UTC RFC 3339 timestamp. `container_id` is the host-assigned 64-character lowercase hex identity in the shared startup contract. |
| `host_platform`, `image_platform`, `transport`, `runtime_profile` | One of the four supported host combinations, matching Linux image architecture, Linux `fifo` or macOS `spool`, and `operator-container/v1`. |
| `image_digest`, `release_record_digest` | Frozen local Docker image ID and existing release-record pin. |
| `contract` | Package `version`/`digest`, plus `catalog_digest` and `operations_digest`. |
| `engine_context_digest`, `input_tree_digest`, `skill_set_digest`, `scenario_bundle_digest` | Canonical object identities of the complete frozen input documents. Exact file-byte identities remain in the existing input descriptors. |
| `host_policy_digest`, `model_profile_digest` | Frozen host policy and the safe model profile identity supplied in EngineContext. Credentials remain outside these records. |
| `target` | Initial adapter, native session ID, worker instance ID, native feedback profile, native capability-source digest and public capability-projection digest. Worker and revision remain attribution. |
| `remaining_limits`, `harness_limits` | Complete initial effective budgets, validated against the existing shared schemas. |
| `retention` | `mode: manual-purge`, host-selected `max_journal_bytes` and `max_segment_bytes`. |

`RunManifest.Bytes()` validates and canonicalizes the complete manifest using
`jcs-v1`. Bootstrap's existing `run_manifest_digest` is the SHA-256 of those bytes;
there is no self-digest field. `ValidateLaunchInputs` runs the shared launch
identity checks, then checks the manifest against the transcript, complete input
documents, supplied host policy and EngineContext identities/limits. It does not
authenticate a publisher, select policy, or inspect the native session. Callers
must establish those facts independently and verify the actual staged files.

After Docker create, save `launch/docker-binding.json` using
`operator.dev/docker-binding/v1alpha1`. It contains campaign, launch and shared
container IDs, run-manifest digest, full Docker container ID, image ID, selected
local `unix:///absolute/socket` endpoint, Docker daemon ID and required labels:

```text
ai.intrusive.operator.campaign  = campaign_id
ai.intrusive.operator.launch    = launch_id
ai.intrusive.operator.container = container_id
```

Persist this record before Docker start. An identical save is idempotent; a
different binding is rejected. Never derive recovery's daemon endpoint from the
current CLI context or environment. `ReadDockerBinding` works while the campaign
writer is active, blocked or failed and never acquires its lock or reads journal
data. The [termination client](DOCKER_TERMINATION.md) verifies the saved daemon ID,
exact container, image and labels against Docker. Missing identity is uncertainty.

Neither the manifest nor the Docker binding changes during a healthy target
restore. Journal the revision transition and replacement native target binding.

## Directory layout and access

The caller provisions an existing private state root owned by the Operator user.
The store requires directories without group/other permissions and creates its
directories as `0700` and files as `0600`. No path comes from guest content.

```text
campaigns/<campaign-id>/
  campaign.json                     # immutable manifest/launch pin
  writer.lock                       # advisory host writer lock
  journal-head.json                 # last committed sequence, hash and byte total
  termination.lock                 # separate nonblocking emergency-record lock
  termination-intent.json           # first terminal decision, when requested
  termination-results.json          # bounded history of Docker stop observations
  launch/
    run-manifest.json
    docker-binding.json             # only after successful Docker create/binding
  journals/<16-digit-revision>/
    events-000001.jsonl
    content/event-<16-digit-sequence>-<2-digit-index>.bin
  artifacts/
  revisions/
  reports/
```

`Create` exclusively creates a fresh campaign group. It refuses any existing
campaign ID, including an incomplete preparation. It does not delete failed
preparations or retained evidence. `os.Root` confines file resolution; readers
reject symlink/nonregular journal and content files. Private service ownership
remains essential: corruption hashes are not signatures against that same UID.

Campaign components use the artifacts, revision and report directories for
retained state. Journal/result inventories, evidence retention and reporting/export
are implemented. [Administrative purge](PURGE.md) removes complete managed groups
after retirement and inactivity preflight.

## Append and commit rules

`Writer.Append(Entry)` serializes host journal writes. An event records version,
campaign/launch/manifest binding, campaign-wide sequence, current revision, UTC
timestamp, kind, optional operation transition, bounded metadata, content
descriptors and the previous event digest. Revisions never move backwards;
sequence numbers start at one and continue across healthy restores. Historical
request/worker/native-revision attribution belongs in metadata even when an event
is recorded under a later active revision.

Each JSONL line is `{ "event": ..., "digest": ... }`. `digest` hashes canonical
`event` bytes, excluding only that outer digest. The first event's
`previous_digest` is the run-manifest digest; later events reference the preceding
event. The durable head pins the final sequence/digest and cumulative bytes so
removing an entire final line or segment is detectable too.

For each append:

1. Validate the entry and check the cumulative journal budget before writing.
2. Publish each exact content body through a private temporary file: write,
   file sync, atomic no-replace link, temporary-name removal, directory sync.
3. Append the event line to the current revision's segment and sync that file.
4. Publish the new head using a synced temporary file, atomic replacement and
   directory sync. Only then return success and advance in-memory bookkeeping.

Segments rotate before exceeding the host-selected segment budget. A new
revision starts a new segment directory. Each event envelope is bounded to
256 KiB plus its newline; metadata must be a JSON object of at most 64 KiB.
Up to eight distinct content roles can retain exact binary bodies, each at most
4 MiB, with media type, host-generated relative path, raw size and SHA-256 digest.
Record engine-visible/provider-bound bodies as separate roles; exclude host-added
credentials and credential-bearing routing. This generic store does not validate
operation-specific audit completeness or perform redaction.

`max_journal_bytes` includes event lines and retained content across all revisions.
It must cover at least one segment; `max_segment_bytes` is between 256 KiB and
64 MiB. Both are explicit host policy inputs. Inspection and writer inventories
also cap any one directory at 100,000 entries. Reaching a byte or inventory limit
permanently fences the writer; it never deletes earlier evidence. These journal
limits are distinct from the existing 512 MiB default transient spool limit.

I/O errors also permanently fence the writer. An error after publication may mean
the record reached disk; callers must stop execution and treat the outcome as
uncertain, even if later inspection finds an intact record. Validation errors
before any write do not poison the store. The dispatcher must persist rejection
records as required; it cannot interpret an append validation error as permission
to skip journaling or continue an effect.

`Failure()` and `Close()` use the writer mutex. Neither is a prerequisite for
independent Docker termination. `Close()` only releases resources; it does not
assert campaign completion, native-effect resolution or container exit.
It also closes the lock-independent terminal signal exposed by `Fence()`.

## Operation history and recovery

An optional `operation` object binds a durable `operation_id`, host-computed
`identity_digest` and one of these transitions:

```text
INTENT_COMMITTED -> DISPATCHED -> RESULT_COMMITTED | UNKNOWN
INTENT_COMMITTED -------------> RESULT_COMMITTED | UNKNOWN
```

The direct result path supports local/rejected work. Reused intents, changed
identities and repeated or invalid transitions are rejected. The dispatcher
resolves healthy duplicates from already retained results before asking for a new
append; the journal does not grant duplicate execution. Native receipt validation,
authorization, reservations, counters and result semantics remain dispatcher work.

`Inspect` obtains the writer lock without waiting and refuses active writers. It
streams records, verifies ordering, hash chains, content bytes and committed head,
and returns a verified prefix with an error on damaged/incomplete evidence. It
does not truncate files, promote uncommitted tails, replay effects or repair the
head. A visitor sees individually verified records; the final return determines
whether the entire journal was intact.

`Observe(ctx, ...)` supports live status/log observers without taking the writer
lock. It opens one atomically published head, then verifies exactly that prefix,
including referenced content, operation transitions and reservations. Appends
committed during observation appear on the next read. Pending files, later
segments and incomplete future revisions are outside the captured prefix; this
result deliberately has no `JournalIntact` field. A successful observation MUST
NOT authorize recovery, cleanup, execution or a claim that the worker is healthy.
Observers MUST check the final error as well as individual visitor results.
Cancellation stops scanning without changing files or the worker.

Operations without a committed result are reported as `UNKNOWN`, including
intent-only operations. The result preserves their last recorded state. Inspection
never returns execution permission or an open writer. Recovery remains cleanup
and reporting only, including when all persisted records are intact. [Startup recovery](STARTUP_RECOVERY.md) now integrates separate cleanup and
reconciliation records with the host lifecycle.

## Validation and remaining integration

Tests cover all four platform/transport manifests; real shared launch fixtures;
identity reads while the writer mutex is held; immutable campaign/launch identity;
rotation and revision continuity; exact binary content; operation transitions;
budget exhaustion; corrupt/missing content, full-line loss and truncated tails;
injected file/directory sync failures; and process exit without a graceful close.

Run `make test` and `go test -race ./internal/campaign`. The filesystem tests run
on the current macOS ARM64 host; cross-compilation checks the other three targets.
Neither substitutes for Docker/transport qualification or a power-loss test.

The [durable attempt admission layer](DURABLE_ATTEMPT_ADMISSION.md) persists
attempt observations/charges/results, reserves audit capacity, checks free space
and signals a lock-independent terminal fence. The
[Docker termination layer](DOCKER_TERMINATION.md) supplies the independent observer
and emergency records; the launcher and broker integrate terminal checks before
admission and dispatch. Reservations protect the logical write budget, not physical
disk space against other host writers. [Transport](HOST_TRANSPORT.md), service
timers, reporting and Attack Harness runtime integration are implemented. Campaign
purge is implemented; full runtime qualification remains outstanding.
