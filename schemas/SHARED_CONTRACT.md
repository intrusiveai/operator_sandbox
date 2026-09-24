# Shared host/harness contract

Status: accepted design, 2026-09-16; contract package publication and runtime
implementation remain pending. This is the authoritative Operator-owned source
for shared host/harness wire semantics, identity, startup, manifest delivery and
completion. Component specs describe their responsibilities and reference these
rules; they must not define competing wire contracts.

Approval fixes the design below. It does not claim that all JSON Schemas, Go/Python
bindings, validators or fixtures exist or pass. Publish package `0.1.0` only after
the remaining schemas and semantic checks are authored and both conformance
runners pass. Existing attempt/feedback schemas remain active as specified below.
The closed ScenarioBundle and TargetCapabilityManifest schemas are now in the
catalog. Their [capability mapping](CAPABILITY_EXPORT_CONTRACT.md) and reference-chain
fixtures define compatibility-based admission and opt-in exact projection pins;
this does not complete the remaining package publication or runtime gates.

The [validation foundation](../contracts/README.md) implements strict JSON decoding
and offline structural validation of the current catalog in Go and Python, with
shared byte fixtures and generated schema-ID constants. The
[ordinary protocol stage](ORDINARY_WIRE_CONTRACT.md) supplies typed envelopes for 13
operations, their registry, spool ACK schema and stateless correlation checks.
The [assessment contract](ASSESSMENT_CONTRACT.md) supplies typed record append,
conclusion content and completion-chain validation.
The [startup/manifest stage](STARTUP_MANIFEST_CONTRACT.md) supplies closed control
messages, successful-transcript checks, bounded input/skill inventories and shared
raw-descriptor/path validation. The [input-content stage](ENGINE_INPUT_CONTRACT.md)
adds EngineContext, prompt provenance/composition and launch byte consistency.
Runtime gates remain separate work.
The [canonical identity stage](CANONICAL_IDENTITY_CONTRACT.md) implements `jcs-v1`,
raw/canonical digest helpers, canonical launch bindings and artifact-content checks.
The [package integrity stage](PACKAGE_INTEGRITY_CONTRACT.md) adds the closed package
manifest, deterministic construction, payload verification and a loader that retains
the verified version/digest. Package publication and runtime qualification remain pending.
The [transport codec stage](TRANSPORT_CODEC_CONTRACT.md) implements bounded FIFO
frames, spool naming, lane validation, consecutive sequences and cumulative ACK
checks. Physical I/O, queues, scheduling and deadlines remain runtime work.
The [attempt bookkeeping stage](ATTEMPT_BOOKKEEPING_CONTRACT.md) implements
campaign-wide allocation and in-memory duplicate/admission/result transitions.
The [loop accounting stage](HARNESS_LOOP_ACCOUNTING_CONTRACT.md) implements
finite-loop limits, read/progress accounting, batch skipping and bounded finalization.
Durable history, dispatch, verified event sources and timers remain runtime work.
The [model codec stage](MODEL_CODEC_CONTRACT.md) supplies typed model relay,
a pinned Chat Completions text/function subset,
trusted policy binding and correlated native continuation segments. Other codecs
and real provider/route qualification remain implementation work.
Complete message bindings/schemas, stateful checks and transport conformance remain outstanding.

## 1. One owner and one pinned package

Operator owns `operator-contracts`, initially package version `0.1.0`, with package
manifest schema `operator.dev/contract-package/v1alpha1`. The package contains
closed JSON Schemas, the offline URI catalog, operation registry, semantic rules,
Go/Python bindings and validators, and shared golden test vectors. Engine builds
pin the exact package version and content digest; the installed Operator broker
must support that exact digest. No runtime negotiation, network schema fetching,
unknown-field tolerance or silent fallback is needed in this MVP.

Package version and individual message versions are separate. Keep the current
attempt request/result v1alpha2 and observation read/selection/manifest schemas.
Use engine-pipe/v1alpha1 for the new pipe envelope and engine-conclusion/v1alpha2
for the expanded conclusion. Package upgrades publish a new immutable digest;
a running campaign never changes its contract package.

The package manifest contains `api_version`, `package_version`, sorted file
inventory (relative path, byte length, raw digest), schema catalog identity,
operation-registry identity and semantic-profile identities. Exclude the package
manifest and signatures from the inventory they bind. The package content digest
is the canonical digest of that manifest payload. The host-only
[HTTPS release record](ENGINE_RELEASE_CONTRACT.md) binds this package digest,
the local Docker image ID and the required Operator/runtime/platform compatibility.
Operator and the engine consume their installed package copies.

The [implemented package layout](PACKAGE_INTEGRITY_CONTRACT.md) retains flat schema
filenames compatible with the offline catalog loader:

```text
operator-contracts/
  package.json
  catalog.json
  operations.json
  *.schema.json
  semantics/{encoding,identity,digests,lifecycle,limits}.md
  go/
  python/
  fixtures/{valid,invalid,canonical,frames,traces}/
```

## 2. Common types and identity

| Type | Rule |
|---|---|
| ID | Existing ASCII identifier pattern, 1–128 characters. |
| Integer | Exact JSON integer, nonnegative unless explicitly specified; at most 2^53−1. Booleans are not integers. |
| Time | UTC timestamp with explicit `Z`, for attribution; never guest authority for deadline enforcement. |
| Digest | `sha256:` followed by 64 lowercase hex characters, with an explicitly named digest profile. |
| Bytes | Strict padded standard base64, bounded both before and after decoding. |
| Optional field | Omission means absent; null is allowed only by an explicit union. No coercion, prose repair or implicit schema defaults. |
| JSON | Strict UTF-8; no duplicate keys, nonfinite values, trailing values or unknown fields; maximum nesting 32. |

The shared decoders preserve decimal values for schema checks, normalize numerical
zero, reject nonzero binary64 underflow and reject integer-valued binary64 numbers
outside the safe integer range. JSON decoding does not perform `jcs-v1`
canonicalization. Schema patterns for constrained single-line fields must reject
CR/LF explicitly and use regex syntax supported by both language validators.

Use these distinct identities:

- `campaign_id`: access scope and campaign identity, fixed for the harness lifetime.
- `launch_id`: host-assigned opaque identity of this one harness/container launch.
- `container_id`: host-assigned container identity, supplied at startup and bound
  by RunManifest. Runtime PID/cgroup/native runtime handles remain host-only.
- `run_revision`: current target revision. It changes after successful restore;
  it is not a new launch and does not grant ownership of historical artifacts.
- `call_id`: one ordinary transport request/response exchange, allocated by the guest
  client library; distinct from a provider tool-call ID.
- `operation_id`: durable submission identity for effects and their saved results.
- `receipt_id`: host-created handle for committed data/results, never a credential.

Keep worker IDs and native Interceptor session/state revisions in the host adapter.
The host records worker/revision attribution when forwarding native operations.
They are not additional guest access boundaries. A currently admitted harness may
read earlier same-campaign receipts through their original source bindings.

Existing EngineAttemptRequest/Result `request_id` means durable submission ID.
For this package, require it to equal the envelope's `operation_id`.
The envelope's `call_id` is the separate exchange correlation. This explicit rule
avoids silently assigning two meanings to the existing nested request_id field.

### 2.1 Attempt indices and execution admissions

The [campaign allocator rules](HARNESS_EXECUTION_RULES.md#1-campaign-wide-attempt-numbering)
are normative. The trusted request builder assigns a new attempt index and fresh
request/attempt IDs once for a decoded new attempt submission, before validation.
An allocated rejected submission keeps its number; a corrected submission gets
the next one. Undecodable/non-object arguments and undispatched calls allocate nothing.
Exact duplicates retain identity; restores never rewind campaign numbering.
Operator records the high-water mark independently of execution admission counts
and sends the campaign index unchanged to Interceptor, which already accepts gaps.

EngineContext includes required `attempt_index_high_watermark` (integer 0 through
2^53−1, normally 0), derived from the host ledger and verified bound native history
before startup. The harness seeds its allocator once and retains it across restores.
The model-facing attempt projection excludes bookkeeping request/attempt/index
fields; the full v1alpha2 wire request retains them. These are identities, not
model tactical choices. The default 100-attempt ceiling counts durable execution
admissions after validation, never the highest index; the allocator contract fixes
rejection, failure and duplicate accounting without a new allocation RPC.

## 3. Digest and duplicate rules

Preserve separate recipes rather than pretending every sha256 value hashes the
same representation:

1. File/artifact descriptors hash exact raw bytes. This includes manifests as
   mounted files, prompts, skills, conclusions and provider request/result bytes.
2. Operator structured object identity uses the existing `jcs-v1` canonical JSON
   profile over the schema-defined payload, omitting explicitly designated
   self-digest/signature fields. Do not normalize Unicode text or arbitrary data.
3. A descriptor carries both raw-file identity and canonical object identity only
   where both are required, under distinct names. A descriptor's ordinary `digest`
   always means raw bytes; use `object_digest` for canonical identity.
4. Native Interceptor resources retain their actual native digest algorithms;
   OCI image identities retain their OCI meanings. Adapter tests verify these
   independently; never recompute a native hash with Operator's canonicalizer.

Golden vectors must pin exact canonical UTF-8 bytes as well as expected hashes,
including Unicode, escapes, numeric boundaries, property order, absent/null fields
and nested provider data where allowed. Default Go/Python JSON serialization is
not the cross-language digest contract.
The [implemented identity contract](CANONICAL_IDENTITY_CONTRACT.md) fixes the
serialization profile, exact preimages and validation APIs. Schema checks precede
binary64 canonicalization. Package authenticity and native source verification
remain independent of these object-digest checks.

For effectful operations, duplicate identity binds campaign, operation name,
durable operation ID, canonical body and the original effect target recorded by
the host. Transport sequence/call ID and caller attribution do not change it.
An exact accepted duplicate returns its saved result without repeating the effect;
changed command content conflicts. Lookup precedes obsolete target-binding checks
for an already-recorded operation. Unknown outcomes are reconciled by Operator,
never retried by the harness with a new ID. Read calls are separately bounded and
charged for returned bytes/requests, including repeated reads.

## 4. Pipe envelope and channels

The [transport codec contract](TRANSPORT_CODEC_CONTRACT.md) documents the matching
Go/Python helpers and their integration boundary for the rules in this section.

Use four logical channels: ordinary request/response and host/guest control.
The [guest transport contract](../GUEST_CONTAINER_SPEC.md#4-non-network-transport-and-launch-identity)
selects named FIFOs on Linux hosts and regular-file spools on macOS hosts. The
`engine-pipe/v1alpha1` API name identifies their common envelope. Mount permissions
and launch assignment establish access; IDs remain consistency checks. No socket,
integration credential or public guest endpoint is introduced. Full input/skill
manifests remain in separate immutable read-only mounts.

Linux bootstrap opens directional FIFOs at FD 3–6 within the 60-second readiness
deadline. A four-byte unsigned big-endian length precedes each strict JSON object;
reject invalid/oversize lengths before allocation. Initial no-writer EOF is handled
only during rendezvous; established peer loss is terminal. No dummy endpoints or
reconnect. Mount permissions preserve direction and identity; no kernel-enforced
post-bootstrap reopen ban is required. A later open grants no sequence reset,
replacement peer or failed-campaign recovery.

macOS publishes each complete strict JSON envelope as an atomic regular file, with
no length prefix. The same deadline bounds transport startup. Bootstrap initializes
fixed spool lanes, not FIFO FDs. Section 4.1 fixes file naming, acknowledgements
and cleanup; [host profiles](../HOST_RUNTIME_PROFILES.md#macos-regular-file-spool)
fix mounts and cleanup. Include both in package fixtures before publication.
Existing Interceptor HTTP/model spool wrappers are not Operator's message schema.

Maximum ordinary JSON object size is 4 MiB; control is 64 KiB. Each of four directions
has its own consecutive sequence, starting at zero; no gaps, replay, reset or wrap.
Healthy restores retain channels/counters. Stdout/stderr remain diagnostics only.
Files in a spool are provisional transport data, never durable operation receipts.
Spool polling cannot extend deadlines; missing files do not imply EOF or no effects.
Publication/acknowledgement/full-queue waits use the five-second transfer bound; waiting for a
response uses its operation deadline. A failed peer is terminal on both transports.

Common fields: `api_version`, `kind`, `seq`, `campaign_id`, `launch_id`,
`run_revision`. A request adds `call_id`, `operation_id`, `operation`, `timeout_ms`
and one typed `body`. A response echoes the three operation/correlation fields
and the request's run_revision, and contains exactly one typed `result` or `error`.
Control messages use a closed `kind` discriminator and a typed body.

Example ordinary request (IDs illustrative):

```json
{
  "api_version": "operator.dev/engine-pipe/v1alpha1",
  "kind": "request",
  "seq": 12,
  "campaign_id": "campaign-01",
  "launch_id": "launch-01",
  "run_revision": 3,
  "call_id": "call-13",
  "operation_id": "read-13",
  "operation": "engine.observation_read",
  "timeout_ms": 30000,
  "body": {
    "receipt_id": "attempt-receipt-07",
    "entry_id": "entry-1",
    "offset": 0,
    "max_bytes": 262144
  }
}
```

One ordinary request may be outstanding. Permit at most two ordinary frames per
local queue (8 MiB encoded aggregate) and 16 control frames (1 MiB aggregate).
FIFO partial-frame/blocked-write and spool publication/acknowledgement/full-queue timeout: 5 seconds.
Response availability is bounded by the operation deadline, not the transfer timer.
Control handling and the independent host stop mechanism remain responsive during
ordinary waits. Broken required channels, malformed messages or sequence/binding faults
are terminal; no reconnect/resume protocol is introduced.

### 4.1 macOS file-spool wire rules

This is the authoritative physical mapping of the shared envelope onto file spools.
It adds no harness tool, operation result or durable receipt. Host transport directory
and mount rules are in [host profiles](../HOST_RUNTIME_PROFILES.md#macos-regular-file-spool).

#### Lanes and publication

Use `/run/operator/spool/{ordinary-in,ordinary-out,control-in,control-out}`. `in` is
host-produced/guest-read-only; `out` is guest-produced/host-readable. Each lane has one
writer. Each lane's sequence starts at zero, uses the shared safe-integer range and
does not reset across healthy restores. A filename is exactly 20 zero-padded decimal
digits followed by `.json`, matching the envelope `seq`, for example:

```text
00000000000000000012.json
```

The writer creates `.00000000000000000012.tmp` exclusively in that same directory,
writes one complete strict UTF-8 JSON envelope, closes it and atomically renames it
to the final filename. Never overwrite a ready message or republish it as a retry.
Readers ignore temporary files. Temporary messages count against the two ordinary
or 16 control outstanding slots and corresponding byte bounds. Serialize publication
within each lane, with at most one temporary file including ACK updates in a control
lane. Spool publication requires atomic visibility, not crash recovery; per-message
spool fsync is not required. The separate journal retains its durability requirements.

Use fixed, descriptor-relative no-follow access. Require bounded regular files;
reject links, special files, unexpected names and malformed/oversized JSON. Bound
the actual read even if file size changes after inspection. Validate launch, sequence,
channel role and envelope before accepting a captured in-memory copy; do not reopen
the sender's file as authoritative request data. Atomic publication is not integrity
proof against a compromised writer. A consumed ready file may remain until producer
cleanup; never dispatch it twice. An observed gap, changed unconsumed sequence or
attempt to reuse a sequence is a protocol failure.

#### Cumulative consumption acknowledgements

Host publishes `control-in/consumed.json`; guest publishes `control-out/consumed.json`.
Each side atomically replaces its own file using `.consumed.tmp` in the same directory.
The closed object is at most 1,024 encoded bytes and has exactly these fields:

```json
{
  "api_version": "operator.dev/engine-spool-ack/v1alpha1",
  "launch_id": "launch-01",
  "ordinary_seq": 12,
  "control_seq": 3
}
```

`ordinary_seq` and `control_seq` are the highest consecutively consumed incoming
message sequences, or `null` if none have been consumed. Integers range from zero
through 2^53−1; negative values are invalid. The host file acknowledges `ordinary-out`/`control-out`; the
guest file acknowledges `ordinary-in`/`control-in`. They carry no run revision:
acknowledgement state belongs to the continuing launch. Apply the shared strict
JSON rules, including unknown/duplicate-field rejection. Missing initial ACK is
equivalent to both fields null until a published message's ACK deadline expires.
The guest learns its launch binding from the first validated `bootstrap` before
publishing its first ACK; it must not parse campaign inputs to initialize transport.

A receiver advances a position only after copying, validating and accepting the
message into its bounded processing queue. It does not wait for the operation to
finish. Do not ACK more messages than the local bounded queue can hold. Control/
ACK pumping continues while a model/target operation awaits its result.

The producer accepts only the matching launch and positions no greater than its
highest published sequence. Track positions monotonically; repeated/stale lower
positions do not roll state back or trigger redelivery. Null after a higher observed
position cannot clear it. A future or wrong-launch ACK is a protocol failure.
Delete covered message files promptly, before reusing their physical queue capacity.
The receiver never needs to delete files from its read-only inbound mounts.

Consumption ACKs release transport storage only. They do not prove external
execution, durable acceptance, success or cancellation. Host journal intent and
result commits still gate effects and guest delivery. ACK files have no channel
sequence, never enter the operation dispatcher, do not consume control-message
slots and are never themselves acknowledged. There is exactly one final ACK file
per producer.

Host directory-size accounting also applies under the
[host profile](../HOST_RUNTIME_PROFILES.md#host-spool-size-check): configurable
512 MiB default, checked every second, with immediate Docker termination on excess.
This adds no wire message, guest tool or acknowledgement and does not increase
message/queue limits.

#### Polling, timeouts and teardown

Poll every 10 ms using the single-threaded event loop. Service ACKs and control before
bounded ordinary work. Look up known next-sequence filenames and ACK paths; bound
any additional directory inspection. No filesystem-notification, socket or extra
thread dependency is introduced. Ordinary traffic must not starve control processing.

Publication, waiting for a consumed ACK and full-queue waits each have a five-second
monotonic ceiling, capped by remaining applicable startup/campaign time. Start the
ACK timer when the final message is published. No ACK timer applies when no message
is outstanding. Polling, stale ACKs, file timestamps or partial progress do not
renew timers. Operation response deadlines remain separate: a consumed model request
may run to its allowed operation deadline while the transport keeps pumping.

Failed publication/ACK, queue-limit violation, malformed traffic or a required peer
failure closes admission and triggers Docker termination with explicit uncertain
effects where applicable. File absence is neither EOF nor proof of no execution.
Do not age-delete an unacknowledged message and continue, retry publication, reset
sequences or replay leftover files after a crash. Failed writes remove their exact
temporary file when possible; no unbounded sweep blocks termination.

After an accepted graceful stop, the guest consumes the stop response, publishes
its consumption ACK and exits within the existing five-second exit limit. It never
waits for an ACK of that ACK. Host-confirmed expected exit may complete teardown
even if the final transport ACK was lost; saved journal records determine finalization.
Immediate termination never waits for control delivery or transport ACKs. After
confirmed container exit, discard the spool under the host cleanup rules. Healthy
target restores retain all transport/ACK state.

## 5. Deadlines, limits and errors

The guest requests positive relative `timeout_ms`. Operator computes a monotonic
host deadline from the smaller request, operation-policy limit and remaining
campaign time. Guest timestamps/heartbeats cannot extend it. Initial operation
ceilings: model generation 120 seconds; snapshot creation/restore 300 seconds;
other ordinary operations 30 seconds. Target invocations also obey their declared
adapter timeout. Initial phase limits remain 60 seconds for confinement/readiness
and 60 seconds for input initialization. The 30-minute campaign hard ceiling stays.

Snapshot creation uses configurable campaign defaults of 20 admissions and
1,073,741,824 cumulative committed canonical file bytes, as defined in Operator
Section 10.3. Counts/bytes survive restores and deletion never refunds them.
`engine.snapshot_request` accepts only optional `label`/`description`; Operator
persists and supplies the remaining byte allowance to native `snapshot.create`
as `maximum_committed_bytes`. It is not a guest-controlled tool argument. Retries
reuse the original native request and consume no second admission/byte charge.

The existing 180-second progress rule must distinguish an idle/stalled harness
from a host-known admitted operation with its own deadline, such as restore.
Optional progress reports are bounded to one per second and do not renew limits.

Use one bounded error shape: `code`, `message`, `instance_path` when relevant,
`effect_state` (`none`, `known`, `unknown`), and `disposition`
(`correct-and-resubmit`, `gap-and-continue`, `terminate`). Limit message to 512
characters and path to 2,048, with no raw credentials/private diagnostic bodies.
Operation-specific codes are enumerated in the registry; the existing feedback
error codes remain. Protocol errors may close a channel without a reply when
correlation cannot be trusted.

Errors before effects may be correctable. Known optional missing feedback is a
gap. Unknown model/target effects, receipt integrity conflicts and execution
failures terminate execution. Timeout never proves non-execution. Existing typed
EngineAttemptResult already carries execution/effect/retry dispositions: return
it as the operation result, without a second competing success flag.

### 5.1 Harness-loop defaults and accounting

Adopt [the finite-loop rules](HARNESS_EXECUTION_RULES.md#2-configurable-finite-loop-defaults)
and closed [effective-limits schema](harness-loop-limits.schema.json). Administrator
configuration is `limits.harness`; all resolved fields appear in
`EngineContext.limits.harness` and the startup admission budget projection. Initial
values are 300 model turns, 2,000 dispatched model tool calls, 16 calls per model
response, 50 total/5 consecutive invalid tool calls, 256 MiB cumulative reads and
10 model turns without defined progress. All are configurable positive integers,
with omission selecting defaults and release/host/target policy able to narrow.

The linked contract fixes reservation/settlement, duplicates, skipped calls,
compaction, exact progress events, counter retention across restore and bounded
model-free finalization. Host-observed charges and locally enforced counters are
distinct; guest assertions never refund authoritative host charges. These rules
add no monitoring service, heartbeat exemption or generic retry mechanism.
Include allocator/loop traces and structural limit vectors in both Go/Python
conformance runners before publication.

## 6. Exact startup exchange

The [startup/manifest contract](STARTUP_MANIFEST_CONTRACT.md) fixes the implemented
envelope/body fields and validation boundaries for this exchange.

| Order | Direction / control kind | Meaning |
|---|---|---|
| 1 | Host: `bootstrap` | Assign campaign/launch/container/initial revision; identify pinned package, engine release, runtime profile, transport (`fifo` or `spool`) and RunManifest; provide bootstrap time limit. No bulk input inventory. |
| 2 | Guest: `confinement_ready` | Echo binding/package/release/profile/transport after installing the live restrictions. This is an assertion, not host attestation. |
| 3 | Host: `initialize` | After runtime, journal and Docker lifecycle gates pass, authorize input reads and provide the bounded fixed-manifest descriptors and initialization deadline. |
| 4 | Guest: `initialized` | Report verified descriptor/object digests for inputs, EngineContext, prompt and skill set; echo package/catalog identities. No ordinary dispatch yet. |
| 5 | Host: `admission_open` | Confirm all gates and exact initialized inputs; supply current revision, advertised operations and initial effective/remaining budgets. Ordinary requests may now begin. |

No model/target/input processing precedes its specified gate. A startup error or
mismatched identity terminates the launch. New campaigns get new launch IDs;
healthy restores do not repeat this handshake. Host termination control can
interrupt every startup phase.

## 7. Full inputs and skill manifests without oversized control frames

Use the required read-only `/run/operator/manifests/` mount. Control messages
contain descriptors, not embedded file inventories. Use fixed `path_id` values
resolved by the release rather than caller-chosen paths:

- `input-tree` → `/run/operator/manifests/input-tree.json`
- `skill-set` → `/run/operator/manifests/skill-set.json`
- ordered skill-manifest slots 0–15 → `skills/0000.json` through `skills/0015.json`
  beneath this manifest directory.

A descriptor contains path_id (or indexed skill slot), schema ID, raw size/digest
and canonical object_digest. The package fixes the allowed mapping. Manifests
are regular immutable files read incrementally after confinement; no runtime
archive extraction, links, directory scanning or schema downloads are required.

InputTreeManifest entries contain opaque entry ID, fixed root kind, normalized
relative path, role, media type, raw size/digest and optional schema ID. Inventory
order is canonical by root/path. The SkillSetManifest contains ordered selected
skill identities and descriptors of complete per-skill manifests; those contain
the full validated file inventory and SKILL.md entrypoint. Reject undeclared files,
traversal, links and duplicates, including normalization collisions.

Encoded manifest ceilings: InputTreeManifest 8 MiB, SkillSetManifest
64 KiB, each per-skill manifest 2 MiB, all manifest files 40 MiB aggregate. Bound
relative paths to 1,024 UTF-8 bytes and depth 16. Preserve current data limits:
4,096 input files/64 MiB excluding separately counted skills; 16 skills/64 MiB
aggregate; 1,024 files/8 MiB per skill; 1 MiB per ordinary skill/reference file.
Manifest bytes count against host/guest staging resources in addition to data.
The [startup/manifest contract](STARTUP_MANIFEST_CONTRACT.md) fixes the portable
Unicode path profile, sorted inventory rules and exact metadata fields.

EngineContext remains a safe immutable launch projection: input/prompt/skill
references, capability/model/schema identities, operation registry and initial
limits. It has no secrets, its own digest or future manifest digest. The original
complete ScenarioBundle remains a separate input, not a summary in EngineContext.
The [public objectives/scenarios input](SCENARIO_BUNDLE_CONTRACT.md) specifies the
producer-neutral content of that existing ScenarioBundle slot, including an empty
scenario list. Input origin does not alter startup, tool or conclusion semantics.
The [input-content contract](ENGINE_INPUT_CONTRACT.md) fixes the implemented
EngineContext and prompt-provenance fields, exact-byte composition and validators.
The package must provide closed ScenarioBundle/prompt/record schemas under
explicit catalog IDs; opaque 'any JSON' placeholders are not a published contract.

Maintain the acyclic construction order:

```text
scenario + prompt + references + skill manifests + capabilities + initial limits
  -> EngineContext -> InputTreeManifest -> RunManifest -> startup descriptors
  -> actual process/launch record
```

Manifest files do not inventory themselves. RunManifest remains the host-owned
launch record (including private adapter binding); its digest and safe fields go
to the guest, not the private native session configuration. Skill archive transport
belongs to host build/publishing; both runtime ends consume the normalized data
inventory and content identities, not an archive-specific guest ABI.

## 8. One closed operation registry

The current [ordinary wire implementation](ORDINARY_WIRE_CONTRACT.md) fixes concrete
artifact, snapshot, restore and stop fields and binds the existing attempt/cleanup/
feedback schemas. Typed records and conclusions are defined in the
[assessment contract](ASSESSMENT_CONTRACT.md). The model family remains required before
publication. The checked-in registry is a development subset, not the complete
released tool catalog.

`operations.json` binds each operation name to exact request/result/error schema
IDs, permitted lifecycle states, timeout and size ceilings, effect classification
and receipt policy. Both tool catalog generation and broker dispatch use it.
Arbitrary strings, unknown fields and generic forwarding are not dispatch paths.

| Family | Operations and important constraints |
|---|---|
| Model | engine.model_generate; pinned provider-native codec/profile, no guest endpoint selection or silent translation. Codec-specific schemas/vectors are package members. |
| Artifacts | engine.artifact_begin / put_part / commit; begin fixes purpose/media/raw size/digest, parts identify upload/offset/base64 bytes, commit verifies complete content and returns immutable receipt. Parts ≤256 KiB; objects ≤16 MiB. |
| Attempts | engine.attempt_execute; retain current v1alpha2 request/result, observation selection and native delivery semantics. |
| Cleanup | engine.injection_delete; [closed schemas and semantics](INJECTION_CLEANUP_CONTRACT.md), one attempt-receipt/action handle, current-target deletion, confirmed absence succeeds, restore-aware duplicate behavior. |
| Feedback | engine.observation_read; retain adopted receipt/entry/range contract and profile-limited manifest. |
| Records | engine.record_append; closed record_kind union for hypothesis, progress, lineage, coverage and conclusion references; host supplies authoritative attribution and committed record receipt. |
| State | engine.snapshot_request / snapshot_list / snapshot_inspect / restore_request; preserve campaign/description metadata, source/checkpoint handles, discovery and live-harness restore. |
| Completion | engine.request_stop; payload and acknowledgement below. |

Cleanup request/result v1alpha1 schemas are package members. Bind their operation
to current campaign policy, the ordinary lane and a 30-second ceiling (also bounded
by remaining campaign time). Earlier source receipts identify an action; dispatch
uses the current target while duplicate results retain their original effect target.
The cleanup operation is not an application invocation and does not consume an
attempt admission. Include structural and semantic cleanup vectors in both
Go/Python package runners before publication.

Use restore of an explicit baseline checkpoint to return the target to its baseline state.

Runtime local reference_read/skill loading uses manifest entry IDs; it does not
add a host file-read operation. Artifact receipts and observation entries remain
different handle types. Same campaign membership permits lookup across revisions,
while current channel, visibility, policy and recorded-source checks remain.

## 9. Healthy restore transition on the same channel

The restore request uses the current run_revision and a permitted source/checkpoint
handle. Drain ordinary work; preserve the harness, scratch and channels. Known
preflight rejection returns an error with effect_state none and leaves the
revision unchanged. Post-closure failure/uncertainty remains terminal.

The successful response correlates to the request's old run_revision. Its typed
result contains `transition_receipt`, `previous_run_revision`, `run_revision`
(new), selected snapshot metadata, `remaining_limits`, and
`harness_disposition: continue`. The host commits the transition/new native
binding once before responding. The guest adopts the new revision before its next
ordinary request. Neither endpoint resets sequences, manifests, cumulative IDs,
budgets or adaptive context. Duplicate transition results cannot advance twice.
Native session ID/revision routing remains an adapter responsibility.

For a recorded duplicate, the result retains the original transition revisions.
The response envelope still echoes the current correlated exchange. Runtime
validation must distinguish that saved transition from a first-time transition
using the operation record; a stateless comparison against current caller
attribution must not deny a legitimate saved result or apply it twice.

Host control messages are independently ordered and can overtake ordinary channel
bytes. A terminate message therefore acts on the launch regardless of revision;
it cannot be rejected because a restore response has not yet arrived. Ordinary
responses correlate to their original requests; the successful restore result is
the authority for updating guest active revision. No cross-channel total order or
second revision-ack handshake is required by the MVP.

### 9.1 Tool-call batch boundary after restore

A batch is the ordered list of client tool calls from one complete model response.
A successful healthy restore ends dispatch of that batch, while the harness and
conversation continue. After validating the correlated restore result, adopt its
confirmed new revision once and preserve the actual restore tool result. Do not
execute any remaining call from that model response, including reads, local
helpers, artifact publication, cleanup, another restore or stop. Do not retag those
queued calls with the new revision or automatically replay them on a later turn.
Validate the native response envelope and call-ID uniqueness before any dispatch;
per-tool argument validation/handler execution is not needed for skipped calls.
Earlier executed calls and their results retain their original attribution.

For each skipped call, append exactly one provider-native correlated tool result
with its original tool-call ID (and name where required by the codec), in the
original call order. Its content uses the closed
[ModelToolNotExecutedResult schema](model-tool-not-executed-result.schema.json):

```json
{
  "status": "not_executed",
  "code": "TARGET_REVISION_CHANGED",
  "effect_state": "none",
  "message": "Not executed: target revision changed. Choose the next action using the restore result.",
  "previous_run_revision": 3,
  "run_revision": 4,
  "transition_receipt": "transition-04"
}
```

The revisions and transition receipt must exactly match the successful restore
result; the new revision must be greater than the previous revision. This is a
local dispatcher result, not an EngineAttemptResult, a native operation response
or a new host receipt. It describes non-dispatch, not a target execution failure.
It must never claim an attempt was registered, an artifact committed, a stop
accepted or a cleanup completed. The shared error envelope is unchanged; this
schema defines provider-facing content for this local skip reason only. Package
it with the codec fixtures, without registering a new `engine.*` wire operation.

Once all original tool-call IDs have one actual or not-executed result, the next
model generation receives the original assistant response, actual results through
the restore, and all skipped-call results in a provider-valid conversation segment.
Use the new revision for that model request and subsequent admitted work. Let the
model choose new actions with fresh call/effect identities. If restore was the
last call, the next model turn still receives its result before further tools.
`harness_disposition: continue` permits this next model turn, not the remainder
of the old batch. Preserve the segment through compaction; do not drop skipped
results, orphan tool-call IDs or issue a model request with an incomplete segment.

Skipped calls consume no attempt admission, tool effect budget or artifact upload
because they are never dispatched. Existing model tokens, elapsed time and bounded
response/queue processing remain charged; later model generation has normal limits.
Record skipped dispositions in bounded harness history and preserve them in the
next model request's ordinary host journal. No synthetic native operation or
receipt is created for them. Duplicate restore responses must not advance the
revision, append results or submit the next model generation twice.

A known preflight rejection with `effect_state: none` leaves the revision
unchanged: return the actual restore error and continue later calls in declared
order under normal validation, admission and budgets. Failed/unknown restores,
invalid transition results, host stop and channel failure remain terminal: do not
execute later calls or generate another model response. A terminate message takes
precedence even if a successful restore/result segment is already available;
record undispatched calls for reporting without delaying termination.

Examples and acceptance cases:

| Batch / outcome | Required continuation |
|---|---|
| `[restore_request, attempt_execute]`, restore succeeds | Return actual restore result and correlated `not_executed` attempt result; ask the model for its next action at the new revision. No attempt dispatch/registration. |
| `[record_append, restore_request, observation_read, injection_delete]`, restore succeeds | Keep the record result, return restore result, skip both later calls (including the read). |
| `[restore_request, restore_request, request_stop]`, first restore succeeds | Exactly one transition; skip the later restore and stop. Campaign remains admitted unless independently stopped. |
| `[restore_request, attempt_execute]`, known preflight rejection | Return rejection; validate and dispatch the attempt at the unchanged revision if otherwise permitted. |
| Restore fails/is uncertain, or terminate races with success | No later tool or model generation; preserve known results and undispatched status for reporting. |

Go/Python codec/dispatcher conformance must cover these cases, restore-last and
single-call batches, malformed arguments in skipped calls, duplicate transition
results, every supported provider's call-ID/name correlation and required result
order, budget accounting and compaction preserving the complete tool segment.
Structural vectors are in `fixtures/restore-batch-not-executed.json`; state-machine
and real-provider codec qualification remain implementation work.

## 10. Structured conclusion

Publish `operator.dev/engine-conclusion/v1alpha2`, retaining `status` values
completed/partial/failed and claim interpretations supported/inconclusive/
not-observed. Completed describes the conclusion, not successful exploitation.

Required groups:

- `binding`: campaign_id, launch_id, final run_revision and exact launch input,
  prompt, skill-set, release and contract identities. Host validates these copies.
- `finish_reason`: objectives-addressed, no-useful-next-experiment,
  capabilities-exhausted, budget-limit, context-limit or harness-error.
- `summary`: bounded guest assessment, not an authoritative host outcome.
- `objectives` and `hypotheses`: IDs, scenario/exploratory lineage, outcomes and
  references to committed attempt/decision/coverage records.
- `claims`: statement, interpretation, assurance and exact attempt/observation
  references, including byte ranges when only an excerpt supports the claim.
- `coverage`: tested and untested objectives/alternatives, with reasons.
- `uncertainties`: unresolved effects, unavailable/truncated feedback, unread
  inputs, summaries/omitted ranges and remaining evidence gaps.
- `record_refs`: committed related strategy/lineage/coverage records.

Initial bound: 1 MiB encoded conclusion, 8,192-character summary, 100 claims,
100 objective/hypothesis entries each, 100 gap entries and 256 record references;
arrays/text are additionally bounded by total encoded size. Claim field/reference ceilings are fixed by the
[assessment contract](ASSESSMENT_CONTRACT.md). Detailed exploration history goes in
previously committed records; overflow is explicit partial coverage, not silent
loss. Validate known references/ranges and campaign membership, not 'truth' of
model interpretations. No guest claim changes host execution/evidence status.

Publish the conclusion through existing artifact operations, then append a
`conclusion` record containing its committed artifact receipt and finish reason.
The host verifies the conclusion and returns a record receipt before normal stop.

## 11. Exact graceful stop request and acknowledgement

Normal engine.request_stop body:

```json
{
  "finish_reason": "objectives-addressed",
  "conclusion": {
    "state": "committed",
    "artifact_receipt": "artifact-conclusion-01",
    "record_receipt": "record-conclusion-01"
  }
}
```

The committed references and finish reason must agree. If the live harness cannot
produce a conclusion, permit the explicit alternative
`{"state":"unavailable","reason":"context-limit"}` (bounded enum: context-limit,
budget-limit, local-error, serialization-failed). This preserves a truthful partial
finish without fabricating receipts. It does not waive host hard deadlines.

After durably accepting the finish request and closing new execution admission,
Operator returns this ordinary correlated result:

```json
{
  "status": "accepted",
  "stop_receipt": "stop-01",
  "execution_admission": "closed",
  "conclusion_state": "committed",
  "exit_required": true,
  "exit_within_ms": 5000,
  "finalization_status": "pending"
}
```

This means the host accepted finishing and the guest must exit. It does NOT mean
that native target cleanup/export, assessment, report generation or host-observed
container exit has completed. Those are host campaign-status facts. The harness
submits no further ordinary work after acceptance and exits within the deadline;
Operator still verifies task exit and enforces teardown independently.

Exact duplicates return the saved stop receipt without repeating effects; changed
finish content under the same operation ID conflicts. The harness does not resend
after a lost/ambiguous acknowledgement or reconnect; the host's persisted record
remains authoritative. Exit zero alone never proves a successful campaign.

Immediate/fatal host stop uses control kind `terminate` with a bounded reason,
stop receipt when persistence is possible, and exit requirement. Delivery is best
effort: the independent kill path does not wait for any guest acknowledgement,
conclusion or reporting. Final target stop still obeys Operator's existing opt-in
policy; engine.request_stop cannot change it.

Reserve up to 1 MiB of the existing artifact budget and one conclusion slot for
graceful finalization. Begin normal finalization with up to 30 seconds reserved
inside the existing campaign hard deadline. Finalization admission permits only
conclusion artifact/record/stop operations, not another model/target experiment.
If time/resources are already exhausted or failure requires immediate stop, retain
partial host records and terminate; do not extend the campaign to get a conclusion. Expected EOF and process exit following accepted
graceful stop are normal teardown, not a new protocol failure; unexpected EOF
while execution is admitted remains terminal failure.

## 12. Go/Python conformance is part of publication

The release is not 'locked' merely because JSON files exist. Check in one set of
input byte streams, expected decoded forms, expected canonical bytes/digests,
expected emitted frames and expected accept/reject/lifecycle traces. Both runners
must consume the same files; neither generates its own expected answers.

Required vectors cover:

- all registered request/result/control variants and every closed union;
- duplicate/unknown keys, absent/null, integer boundaries, Unicode, malformed
  UTF-8/base64, depth/count/byte limits and canonicalization;
- fragmented/coalesced frames, sequence faults, partial EOF and transfer timeout;
- FIFO rendezvous/direction, initial versus established EOF, no dummy endpoints,
  no reconnect, endpoint substitution rejection and exact launch cleanup;
- spool exact filenames/temporary names, atomic publication and bounded no-follow
  reads; null/zero/stale/future/wrong-launch cumulative ACKs, 1 KiB ACK bounds,
  no ACK-of-ACK and producer cleanup on read-only inbound lanes;
- spool 10 ms control-priority scheduling, bounded queues including partial writes,
  five-second publication/ACK/full-queue timeouts, long-operation consumption,
  stale/duplicate files, storage failures and no replay after peer failure;
- exact startup ordering, gate failure, bad bindings and mismatched package digest;
- complete >64 KiB input inventories loaded through fixed manifest descriptors,
  full ordered skill verification, self-hash exclusion and path normalization;
- transport call ID versus durable operation ID, known duplicates, mutated-command
  conflicts, timeout before/after effects and unknown-outcome termination;
- campaign index allocation, rejected/corrected submissions, local gaps, admission
  accounting, restore retention and every finite-loop/progress/finalization boundary
  in HARNESS_EXECUTION_RULES.md;
- model codec boundaries, artifact begin/part/commit and existing native attempt
  digest/delivery fixtures, with native recipes preserved;
- profile-filtered feedback, chunk/range integrity, unavailable/truncated results,
  model-visible decoded content and original receipt access after restore;
  include the [feedback translation vectors](fixtures/feedback-translation.json),
  exact native profile preservation, empty-intersection collection suppression,
  legacy-alias/error filtering and denial of filtered-out entry reads under the
  [normative mapping](FEEDBACK_CONTRACT.md#native-profile-and-effective-harness-policy);
- restore preflight rejection, successful same-launch transition, duplicate result,
  terminal failure and terminate arriving before the restore response; successful
  restore batch termination, correlated not-executed results, next-model-turn
  context, nonterminal preflight continuation and duplicate/stop precedence (9.1);
- conclusion structure/references, explicit missing conclusion, stop accepted,
  duplicate stop, lost acknowledgement and independent terminal kill.

A small Go fake broker and Python fake harness must exchange messages over both
real named FIFOs and regular-file spools for startup → attempt/result → feedback read → restore → conclusion
commit → stop. These tests qualify shared syntax/state semantics, not production
containment, real-provider behavior or Operator runtime implementation.

## 13. Publication and implementation status

Attempt numbering after rejection and harness-loop defaults/accounting are resolved
by [Harness execution rules](HARNESS_EXECUTION_RULES.md). Attempt allocation and
admission transitions have [shared semantic traces](ATTEMPT_BOOKKEEPING_CONTRACT.md);
finite-loop/progress/finalization accounting also has
[shared semantic traces](HARNESS_LOOP_ACCOUNTING_CONTRACT.md). Durable runtime
integration, timer enforcement and full transport conformance remain implementation work.

The package shape, startup/manifest flow, identity/deadline rules and completion
semantics and macOS physical spool mapping above are accepted. Remaining work
is to qualify and package the initial native codec and implement any additional
advertised codec profiles, define the host-private
RunManifest, and complete
the operation registry, extend the existing offline
catalog and Go/Python validation foundation with semantic validators and fixtures;
port the required existing data contracts; and pass Section 12 conformance before
publishing package `0.1.0`. Do not substitute approval of this document for that
evidence or claim that the host/harness runtime has been implemented.

Interceptor's native APIs remain separately versioned; Operator is the adapter.
This contract introduces no additional lifecycle service, gateway, harness restart
or authentication scheme.
