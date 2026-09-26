# Native Interceptor client

Status: the host-only client is implemented in `internal/interceptor`.
It provides campaign attachment, instance status, immutable v1alpha2 operation
encoding/dispatch, typed closure confirmation, lifecycle restore/stop, durable
operation-record decoding, snapshot creation/discovery and bounded evidence downloads.
It does not start a campaign or
serve as the harness-facing policy broker. Shared Operator/Attack Harness schemas
and Interceptor's existing API are unchanged.
The [phase handoff](IMPLEMENTATION_STATUS.md) summarizes completed scope and the
remaining host interpretation, campaign-broker and runtime gates.

The authoritative native contracts remain Interceptor's
[local integration guide](../../interceptor_sandbox/docs/local-api.md) and
[operation wire guide](../../interceptor_sandbox/docs/cross-vm-gateway.md).

## Transport

`interceptor.New()` connects only to `http://127.0.0.1:8080`, using a literal IPv4
loopback dial. There is no configurable URL, DNS lookup, proxy, authentication or
TLS setting. Methods select fixed routes. Campaigns and guest requests cannot
provide destinations, headers or credentials.

Requests use POST and JSON, with `Accept-Encoding: identity`. Redirects and encoded
responses are rejected. Response headers are bounded to 16 KiB. This initial client
bounds both complete JSON request and response envelopes to **5 MiB**, checking
actual response reads even when Content-Length is absent or incorrect. Larger
artifact envelopes are not supported by this stage; future artifact admission must
account for base64/envelope overhead and this client ceiling before dispatch.
Evidence archives use the separate streaming transfer described below.
Snapshot list/inspect responses have the narrower native **4 MiB** complete-envelope
limit, enforced while reading even without a Content-Length header.

Attachment/status have a 30-second total deadline. Native dispatch uses the earlier
of the saved request deadline, caller context deadline and a 300-second transport
ceiling. Dialing is bounded to five seconds. The broker must select shorter
operation-specific deadlines and remaining campaign time before saving the request.
No method renews a saved native deadline. Expired/canceled requests are refused
before dispatch; reconciliation uses a fresh `operation.status` request.
Lifecycle dispatch also uses a saved absolute host deadline, capped at 300 seconds
per call. Lifecycle record queries have a fresh 30-second deadline. Interceptor's
admitted lifecycle work continues independently after client disconnection.

The client disables connection reuse and never supplies replayable HTTP bodies or
idempotency headers. It makes no automatic retries. A closure/status request does
not share a client-side mutation lock or connection queue with an in-flight native
operation. Serialization/admission belongs to the host broker and native supervisor.

## Exact native requests and responses

`PrepareOperation(request, body)` creates an immutable `PreparedOperation` with
`Bytes()` and `Request()` accessors. The host must journal those exact bytes and
request identity before dispatching an effect through `Client.Execute`.

Preparation fills the native API version and body digest when omitted, validates
IDs, operation names, deadline and attribution, and enforces the byte bound.
It preserves interior whitespace, number spellings and HTML-sensitive characters
in the body. The digest is SHA-256 of the exact embedded body bytes, **not** the
shared contract's canonical object digest. Outer whitespace is rejected because
native JSON value decoding does not preserve it. Returned byte slices are copies.
Native uint64 revisions retain their native range; they are not decoded through
the shared `jcs-v1` safe-number profile.

Native JSON framing rejects duplicate keys at every nesting level, invalid UTF-8,
non-object roots, trailing values and depth above 64. Transport/typed wrapper
decoders require exact field spellings and required fields, including zero-valued
`session_revision` and false-valued status flags. Responses must match their HTTP
status; native 204 uses HTTP 200 and may contain `body: null` or omit the body.
A successful transport response can still be native 202, 4xx or 5xx.

`Execute` returns that native response without treating transport success as
execution success. `Response.Code()` extracts a bounded symbolic code for host
handling. A native `operation_in_progress`, outcome-unknown code or missing record
must be reconciled under the campaign's terminal/unknown-outcome rules; none permits
reissuing an uncertain effect under a new operation ID.

Transport loss, redirects, malformed/oversized replies and deadlines during a sent
request return `CallError{Uncertain: true}`. Local validation or cancellation before
dispatch cannot send the request. Error strings do not include server bodies,
credentials or host paths. Typed attachment/status/closure helpers return a
`RemoteError` for valid non-200 native responses, retaining the response for host
interpretation. The caller must not infer non-execution solely from an HTTP status.

## Campaign attachment and status

`Client.Attach(ctx, campaignID, workerID, allowTargetStop)` posts the actual host
worker attribution and explicit final-stop choice. It checks returned campaign,
session and worker bindings, v1alpha2 compatibility, running native phase,
feedback profile and evidence ceiling. It checks agreement among the native
session and capability manifest's declared environment/application/capability
identities. Native session revision and campaign run revision are separate values.

The returned `Attachment` contains a small routing projection plus the original
native metadata/capability JSON for host validation and audit. These are protected
host data: session metadata can include host paths. They are never a guest response.
Full native capability digest verification, native-to-public capability projection,
feedback narrowing and target admission remain required adapter work; matching
declared digests alone does not establish those properties.

`Client.Status(ctx, campaignID)` checks the integrated instance ID, active binding,
retained session membership, phase, closure/store flags and optional terminal
failure. `Status.Ready()` requires ready phase, open admission, an available store
and no recorded failure. It is a status observation, not permission to dispatch.
Malformed or unavailable status is not healthy status.

The broker must persist the initially accepted instance/session binding and check
subsequent results against it. `Status.Matches(instanceID, binding)` compares the
instance, session and run revision while ignoring historical worker attribution.
It does not adopt replacements or change the host binding. A new Interceptor
instance after service loss cannot resume the old campaign. Explicit successful
restore is the only normal path for adopting a replacement session/revision.

## Terminal admission closure

`PrepareClose(request)` constructs only `session.owner` with `close_execution` and
the addressed campaign/session. Persist it, dispatch it through `Execute`, then
use `DecodeClosure(response, campaignID)`. Public bind is rejected; binding uses
attachment. Native `session.stop` is excluded from this operation path because
confirmed target stop belongs to the separate lifecycle route.

Closure confirmation requires the direct native owner object, matching campaign,
valid original attribution and a non-null closure timestamp. Live and retained
stopped/error sessions use that same shape. The original worker/run revision are
recorded attribution and need not match the closing caller. A wrapped `{owner: ...}`
or incomplete acknowledgement is rejected. Admission closure does not establish
target termination; it also cannot be used as the prelude to a healthy restore.

## Lifecycle and reconciliation

### Restore and confirmed target stop

`PrepareLifecycle(request, deadline)` freezes a `snapshot.restore` or `session.stop`
command. Persist `Bytes()` and `Deadline()` before calling `ExecuteLifecycle`.
The absolute host deadline is separate from the native wire, which has no deadline
field. Reusing a prepared request never renews that deadline; there is no automatic
retry. Restore accepts a checkpoint ID and optional retained source session ID.
Stop rejects checkpoint/source fields and uses the instance's attachment stop policy.

`DecodeRestore(response, prepared, priorBinding, priorSession)` checks the fresh
session, exactly one increment of the authoritative prior binding's run revision,
the checkpoint/source and metadata parent lineage, campaign, running phase and
unchanged target environment/application/capability digests and native feedback
profile. Native session revision can reset independently. Request worker/revision
are attribution and do not determine the replacement revision or restrict access.
A replayed result may contain the original worker's attribution.

`DecodeStop(response, prepared)` requires the addressed session and `phase: stopped`.
This is the native lifecycle stop acknowledgement; an owner closure acknowledgement
alone is insufficient. Non-200 replies retain their exact native status/code in
`RemoteError`, including preflight rejection, in-progress and uncertain failures.

These decoders do not adopt a binding, resume work or restart the harness. The host
broker must validate the source against saved campaign lineage before dispatch,
reject reuse of any earlier session ID, persist an accepted replacement once, and
check the same Interceptor instance remains ready at that binding before continuing.
Known checkpoint preflight rejection can retain the original healthy binding under
the restore policy. Uncertainty or failure after closure remains terminal; a later
successful record can support cleanup/reporting but cannot reopen execution.

### Separate operation ledgers

`Client.LifecycleStatus(ctx, prepared)` queries `/v1/status` with the saved campaign
and lifecycle operation ID. `DecodeLifecycleRecord` validates the original request,
native command fingerprint, record state and nested response. Fingerprinting follows
native struct serialization and clears only worker/revision attribution. A running
lifecycle record contains `202 operation_in_progress`; a completed record contains
the saved outcome, which can be a rejection or failure. Pass a successful saved
response through `DecodeRestore` or `DecodeStop` before using its contents.

Native experiment operations use `PrepareOperationStatus(query, original)` followed
by `Execute` and `DecodeOperationRecord(response, original)`. The query has fresh
request/operation IDs and a fresh deadline, addresses the original session/campaign,
and can carry the current worker attribution after a restore. It must not address
the replacement session to look up the old operation. The decoder checks the full
original request fingerprint and matching command, exact body digest, deadline and
native expected revision. Worker/revision differences are accepted as attribution.
The optional native command fingerprint is checked when present; native owner-body
normalization is retained separately from the raw body digest.

Native states remain explicit: `running` has the native store's empty response
(`status: 0`); `completed` has a finished response; `unknown` retains its failure
response. The placeholder is accepted only inside a running operation record and
is never an HTTP success. Missing records, running/unknown records and unavailable
queries cannot establish non-execution or authorize another effect under a new ID.
No decoder changes the host terminal fence. Record data and raw session metadata
are protected host audit inputs, not guest-visible payloads.

## Snapshot creation and discovery

`PrepareSnapshotCreate(request, SnapshotCreate{...})` prepares a native
`snapshot.create` operation for the existing journal-before-`Execute` path. The
input contains optional label/description and a required explicit
`maximum_committed_bytes`. Its zero value means exhausted, not omitted. The host
must obtain this allowance from cumulative campaign accounting, retain the same
allowance on retries, and enforce snapshot admission count independently of native
session counters. Labels are valid UTF-8 with at most 256 characters; descriptions
are valid UTF-8 with at most 4,096 bytes. Campaign association comes from the request
envelope, never a body override.

`DecodeSnapshotCreated(response, prepared, targetSession)` requires native status
**201**, verifies the checkpoint, and compares campaign/source, label/description,
environment/application digests and canonical size against the saved request and
target. It rejects a receipt larger than the saved allowance or any successful
creation against an exhausted allowance. Native rejections such as
`429 snapshot_bytes_exhausted` remain `RemoteError` values. The decoder does not
charge counters or adopt a checkpoint: exactly-once accounting and durable receipt
storage remain broker responsibilities, including after operation reconciliation.

`Client.ListSnapshots(ctx, SnapshotListRequest{...})` and
`Client.InspectSnapshot(ctx, campaign, sourceSession, checkpointID)` use the native
read-only host routes with 30-second deadlines. They work for retained source
sessions after restore and after target closure while Interceptor remains alive.
They require no worker attribution or mutation identity and do not restart targets.
Inspect requires the complete source/checkpoint handle and checks both returned IDs.

Listing returns one `SnapshotPage`, including an empty array for an empty result.
Native defaults and limits are 100 records per page, maximum 1,000, maximum offset
and selected inventory of 10,000. The client validates campaign/filter agreement,
page length, totals, exact next offset, ordering by creation time/source/ID, and
duplicate handles within the page. It never automatically follows pagination.
An offset beyond the current total returns an empty page. Inventory is not frozen;
the broker must refresh from zero if concurrent creation or restore changes it,
and apply the smaller harness page/control-frame limits when projecting results.

All three paths use the same native `Checkpoint` model and integrity verification.
The native hash is SHA-256 of Go's ordered struct JSON with `hash` set to an empty
string (the key remains present), including campaign/description metadata. It is
neither the shared contract canonical digest nor a hash of incoming wire bytes.
Native uint64 journal/event sequence values retain their precision. Unknown fields,
duplicate JSON keys, missing required fields, malformed digests, incompatible
metadata versions/status and invalid hashes are rejected.

Read paths accept legacy checkpoints that omit campaign/description metadata,
relying on Interceptor's campaign-scoped source-session lookup. They preserve those
original fields and hash; new creation requires the explicit expected campaign.
The later harness adapter can project the attached campaign and empty description
without modifying the native receipt. An explicitly different campaign is rejected.
Native checkpoint metadata is host-only: the harness projection still needs source
lineage/parent-handle resolution, compatibility checks and public field selection.
A verified ready checkpoint does not guarantee restore preflight will succeed.

## Protected evidence download

`Client.DownloadEvidence(ctx, request, privateDirectory)` posts the lifecycle
version and saved campaign/session IDs to `/v1/evidence`. It never sends a host
output path. Supply an explicit absolute host deadline, local `MaxArchiveBytes`
from `evidence.max_archive_bytes`, and `InterceptorMaxBytes` captured from the
attached instance's status. Limits accept 1 through 9007199254740991 bytes; the
configured local default is 4 GiB. The deadline includes server archive generation
and download and is shortened by an earlier caller-context deadline. It is not
the harness control-frame deadline. There are no automatic retries or range/resume
requests. The existing fixed-loopback, no-proxy, no-redirect transport applies.

Successful transfer requires HTTP 200, `application/x-tar`, one `Content-Length`,
one `X-Content-SHA256` and one `X-Evidence-Max-Bytes` matching the saved native
ceiling. Encoded, partial, chunked or trailer-bearing responses are rejected.
The declared size must fit both ceilings. A 64 KiB buffer streams bytes into a
random exclusive 0600 temporary file with incremental hashing; the extra-byte
probe never writes beyond the declared size. Premature EOF, excess readable body
bytes, digest mismatch, cancellation or storage failure reject the transfer.
HTTP message framing defines the body; bytes outside that framing are not evidence.
Connection reuse is disabled. Successful bytes are synced before returning.

The directory must already exist, be private, owned by the service user and not
be a final symlink. The client pins it with `os.Root` and verifies its identity.
The caller must select a host-only campaign staging directory that the guest cannot
mount. `EvidenceDownload.Receipt()` records requested campaign/session, byte count,
digest and both ceilings. `Reader()` returns a bounded read-only view for the
next validation step, exposing neither a path nor a writable file descriptor.
Always defer `Close()`, which removes the temporary file even after a successful
download. Finish readers before closing; handle methods are not concurrent lifecycle
operations. No committed evidence is replaced. Failures remove incomplete files;
cleanup failure is explicit. After process loss, campaign recovery must remove
or revalidate abandoned staging files; this client does not recover or publish them.

Valid native rejections remain `RemoteError`, preserving HTTP 413
`evidence_limit_exceeded`, `maximum_bytes` and `evidence_retained` without treating
them as target execution failures. Transfer/storage/protocol failures use the
separate `EvidenceError` kind, with no raw body or filesystem path in error strings.
The finalizer must record an evidence-collection gap and avoid retrying unchanged
capacity failures. The per-archive bound does not cap aggregate retained storage or
concurrent downloads; campaign admission/finalization controls those lifetimes.

**A successful download proves byte-transfer integrity only.** Before committing
evidence or declaring completeness, run the structural inspection below, then
verify native manifests/journal hashes,
campaign/session lineage and native completeness markers. The receipt identifies
the requested session; archive identity is not established by the HTTP digest.
This stage does not extract archives, publish evidence, expose it to the harness,
or implement final reports/import/recovery. Administrative import will use the same
native validator once implemented.

### Archive inspection without extraction

`download.InspectArchive(ctx, limits)` reads the complete tar and produces a bounded
index with each member's name, byte count and SHA-256. It rechecks the whole archive
against the transfer receipt, checks blob contents against their hash-based names,
and exposes bounded read-only member readers. The index shares the download's
lifetime; close the download after all readers finish. It does not write extracted
files, interpret native JSON or publish evidence.

`DefaultArchiveLimits(maxBytes)` selects 10,000 entries and the supplied total
expanded-byte ceiling. Explicit limits require 1–100,000 entries and a positive
expanded-byte ceiling within the existing maximum archive-byte range. The future
finalizer must retain the selected limits with collection policy. Metadata blocks
have a separate 64 KiB budget per tar-header advance; member hashing uses a 64 KiB
buffer. Large native files can use the tar PAX size extension.

Inspection accepts only regular files with exact native export names: the known
top-level metadata/journals, checkpoint JSON files, SHA-256 blobs and documented
restore-source/failure records. It rejects unknown names, traversal/absolute paths,
aliases, duplicate entries, links, devices, directories, privilege mode bits,
extended attributes, sparse files and other PAX extensions. Required export members
must exist, and restore-source checkpoint/journal files must appear together.
The native writer's two-block footer must terminate the archive exactly; missing
footers, trailing payloads, concatenated archives and extra padding are rejected.

Structural success is deliberately distinct from native evidence validity. Native
JSON version/identity checks, journal chains, manifest/reference checks, execution
records and completeness/provenance interpretation remain a separate verifier gate.
Until that gate exists, inspected archives remain uncommitted temporary inputs.

## Validation and pending integration

Tests use copied native fixtures with [recorded provenance](../internal/interceptor/testdata/README.md),
controlled responses and a real loopback HTTP test server. They cover exact native
body encoding, byte freezing, request/response bounds, malformed responses,
redirect/compression rejection, expiry, native 204 framing, binding mismatches,
terminal status, original closure attribution and lost replies without automatic
replay. Lifecycle/record tests additionally cover absolute deadlines, source and
replacement lineage, independent native revisions, cross-worker attribution,
missing/in-progress/unknown outcomes, changed-command rejection and strict nested
responses. The loopback server is a protocol test double, not a running Interceptor
target or a qualification result.
Snapshot tests use independently generated native fixtures for current and legacy
checkpoint hashing, including HTML escaping and uint64 values above the shared
contract's safe-integer range. They cover creation status/allowance/receipt binding,
metadata tampering, source scoping, pagination, empty inventories and response bounds.
Evidence tests cover strict headers, the smaller of both ceilings, a streamed
archive larger than the JSON limit, bounded buffer reads, interrupted/damaged bodies,
storage write failure, native quota errors, private staging cleanup and a real HTTP
test using Interceptor's `http.ServeContent` response pattern.
Archive tests cover bounded member access, native blob names, duplicate and unsafe
members, metadata budgets, entry/expanded-size boundaries, footer/truncation errors,
changed temporary bytes and canceled inspection.
The stateful HTTP workflow test combines attachment, snapshot discovery, a lost
restore reply, lifecycle and original-session operation reconciliation, replacement
cleanup/stop and source-session evidence collection. It asserts one effect per
mutation and uses reconciliation only for cleanup after uncertainty.

Native evidence archive/provenance validation and publication, typed experiment bodies/results, capability projection and
feedback filtering remain adapter work. Durable dispatch/reconciliation, status
polling, terminal fencing and the campaign CLI still need integration. No command
in this stage attaches to or mutates a real local target automatically.

### Native provenance verification

`EvidenceArchive.VerifyProvenance` requires an independently saved
`EvidenceIdentity`, including the exact checkpoint digest for restored sessions.
It verifies native serialization hashes, session/manifest/bundle identity, event
and state chains, execution-state journal binding and operation fingerprints,
checkpoint journal positions, restore-source chains, registries, blob references,
and evaluation/completeness consistency. Only successful verification constructs
`VerifiedEvidence`; its archive reader and receipt remain host-only.

Parsing is bounded to 64 MiB per metadata member, 32 MiB per journal line and
100,000 records per journal, in addition to archive transport/entry limits.
Unsupported or inconsistent evidence is rejected. Valid partial evidence retains
explicit gaps. Native hashes establish internal integrity and saved-target binding;
they do not authenticate arbitrary files or establish that oracle assertions are true.
