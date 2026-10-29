# Ordinary wire protocol

This document fixes the wire field choices for the ordinary operations implemented
in the shared validation library. It supplements [SHARED_CONTRACT.md](SHARED_CONTRACT.md).

## Registry and envelopes

[operations.json](operations.json) is the source for 14 ordinary operations:
artifact begin/part/commit, attempt execution, injection deletion, observation
read, typed record append, snapshot create/list/inspect, restore, finish, local diagnostic retention
and model generation. The [model codec contract](MODEL_CODEC_CONTRACT.md) fixes
the model exchange's native subset and trusted profile binding. Each entry identifies
request/result/error schemas, allowed error codes, lifecycle states, finalization
restrictions, timeout ceiling, encoded request/result ceilings, effect kind and
receipt policy. Native Interceptor operation names and schemas stay in its adapter.

Receipt policy `durable` requires saving the operation result before replying,
including upload steps that return an upload handle rather than a new artifact
receipt. Read results use `none`; repeated reads still incur their normal charges.

The generated [request](engine-pipe-request.schema.json) and
[response](engine-pipe-response.schema.json) envelopes bind operation names to those
exact bodies. `make generate` regenerates envelopes and schema-ID constants;
`make test` rejects stale generated files. No unknown operation or untyped forwarding
path is accepted. Model generation uses the implemented closed schemas and
shared native-codec fixtures. [Startup and manifest metadata](STARTUP_MANIFEST_CONTRACT.md)
now have schemas and shared validation. [Input content](ENGINE_INPUT_CONTRACT.md)
adds EngineContext and prompt provenance.
Records and conclusion content are defined in the
[assessment contract](ASSESSMENT_CONTRACT.md).

Every ordinary message carries the common version, kind, sequence, campaign,
launch, revision, call ID, durable operation ID and operation name. Requests add
positive `timeout_ms` and `body`. Responses contain exactly one `result` or `error`.
The global ceiling is 4 MiB per encoded envelope; record append narrows its
request and response ceilings to 64 KiB. Sequences are independent in each
direction; a response sequence need not equal the request sequence.

The response echoes the request's campaign, launch, revision, call ID, operation
ID and operation name. The body operation_id selects a target capability. Operator assigns internal
attempt request_id from the envelope operation_id; call_id remains exchange correlation.

The host clamps the requested timeout to the registry ceiling and remaining campaign
time. An otherwise valid longer requested timeout is not a new authorization or an
encoding error. Snapshot creation/restore have 300-second ceilings; other operations
in this stage have 30-second ceilings. Every deadline is host-monotonic.

Only admitted execution permits normal work. Finalization admits artifact operations
for a large assessment draft, local diagnostic reports and finish. For part/commit, determine purpose from the host's saved upload
record. The guest cannot relabel an existing upload. Registry metadata does not
replace runtime policy checks or authorize startup dispatch.

## Artifacts

| Operation | Request | Result |
|---|---|---|
| `engine.artifact_begin` | `purpose`, `artifact` descriptor | `upload_id`, identical purpose/descriptor, `next_offset: 0` |
| `engine.artifact_put_part` | `upload_id`, byte `offset`, base64 `content` | Same upload/offset, `raw_length`, `next_offset` |
| `engine.artifact_commit` | `upload_id` | Same upload, immutable `artifact_receipt`, original purpose/descriptor |

Purpose is `payload`, `carrier`, `conclusion` or `supporting-data`. The descriptor
contains raw-byte `digest`, `size_bytes`, `media_type` and `canonicalization`
(`raw` or `jcs-v1`). This matches the existing attempt artifact descriptor.
`jcs-v1` requires `application/json`; it asserts the submitted bytes already use
that canonical form. The host verifies rather than rewriting them or their digest.
An attempt uses direct input or a committed artifact receipt; Operator resolves
descriptors from retained bytes and campaign membership. Models do not repeat descriptors.

Objects are at most 16 MiB; a conclusion is at most 1 MiB and uses `application/json`.
Each part carries 1–262,144 decoded bytes, strict padded standard base64 with canonical
padding bits. `next_offset` equals requested offset plus decoded length. Uploads
are contiguous in the host upload record; gaps, overlap and changed bytes conflict.
Exact operation duplicates return the saved result without appending twice. Empty
objects need no parts. Commit verifies completeness, original declaration, raw
digest and any claimed canonical form before publishing an immutable receipt.

The validators implement per-message limits, base64 checks, chunk arithmetic and
request/result correlation. The [identity validation layer](CANONICAL_IDENTITY_CONTRACT.md)
adds complete artifact size/digest and claimed canonical-form checks. The [artifact service](../docs/ARTIFACT_SERVICE.md) implements upload
storage, contiguous-offset tracking, trusted declaration lookup at commit and
durable publication.

## Snapshots and remaining limits

Use opaque `source_session` and `checkpoint_id` handles, not native session IDs.
The host resolves them through the campaign's retained source records. Creation
accepts only optional `label` (at most 256 characters) and `description` (at most
4,096 UTF-8 bytes). Campaign and remaining native byte allowance are host supplied.

All snapshot metadata uses the [same schema](snapshot-metadata.schema.json):
campaign ID, source/checkpoint handles, optional `parent` handle pair, label,
description, UTC `created_at`, status and `canonical_size_bytes`. Empty label or
description means none; metadata always includes both strings. Status is `ready`
or `unavailable`; creation and successful restore return `ready`. Discovery may
describe a retained checkpoint that is no longer available. The host checks actual
availability and native compatibility during restore preflight. Do not expose
native paths, oracle state or private session binding fields in this projection.

| Operation | Request | Result |
|---|---|---|
| `engine.snapshot_request` | Optional label/description | `receipt_id`, `snapshot`, `remaining_limits` |
| `engine.snapshot_list` | Optional source handle, offset, limit | `campaign_id`, `snapshots`, `offset`, `total`, optional `next_offset` |
| `engine.snapshot_inspect` | Source/checkpoint handle pair | `snapshot` |
| `engine.restore_request` | Source/checkpoint handle pair | `transition_receipt`, previous/new revision, `snapshot`, `remaining_limits`, `harness_disposition: continue` |

Creation preserves the supplied label and description exactly. Inspection and
restore return the selected handles; every metadata record belongs to the envelope
campaign. Missing parent is omitted, not null. Timestamps must denote a valid UTC
calendar instant with `Z` and at most nine fractional digits.

Listing defaults to offset 0 and limit 50; the maximum limit is 100. Order by
creation instant, source handle, then checkpoint handle. `total` describes the
filtered inventory for that request. Return a page from one host inventory view;
pages across separate calls are not a frozen view. Handles cannot repeat within
a page. `next_offset`, when present, equals offset plus returned count and must
advance. Omit it on the last page, including empty results past the end. Respect
the source filter and refresh discovery after changes.

[Remaining limits](remaining-limits.schema.json) carries nonnegative host-authoritative
allowances: `campaign_time_ms`, `attempt_admissions`, `model_tokens`, `model_turns`,
`artifact_bytes`, `artifact_objects`, `snapshot_admissions`, `snapshot_bytes`,
`observation_reads` and `observation_bytes`. These are remaining allowances, not
initial ceilings or guest assertions. Zero means exhausted. Use the wire safe
integer range; narrow larger administrative ceilings before guest admission.
Restores never refund charges. Guest-local loop counters remain governed by
[Harness execution rules](HARNESS_EXECUTION_RULES.md), not replaced by this projection.

A restore response envelope echoes the exchange's request revision; the result
retains the original effect's previous/new revisions. The new revision must exceed
the previous one. For a first-time transition, the host/guest state machine must
verify that the previous revision is the active request revision before applying
the new one. A known duplicate can return an older saved transition even when the
current exchange has newer attribution; validate it against the recorded operation
and never advance again. The stateless validator cannot establish that ledger fact
and must not impose worker/revision ownership restrictions on duplicate lookup.

## Errors, feedback and finish

[OperationError](operation-error.schema.json) bounds the code, message, optional
JSON Pointer instance path, effect state and disposition. Registry entries enumerate
the allowed codes for each operation, including the existing observation and cleanup
codes. Unknown effects require termination. `OUTCOME_UNKNOWN` must report unknown
effects; correction requires `effect_state: none`. Integrity errors, cleanup/restore
execution failures and internal errors are terminal. Known preflight rejections
remain distinguishable from post-closure failure. Existing typed attempt results
retain their own execution dispositions.

Feedback validators check receipt/entry/range correlation, canonical base64, decoded
length, requested chunk bound and EOF against the descriptor's size. They do not
prove visibility, source membership or the full artifact digest; those require the
host receipt map and complete-content verification. Unavailable/truncated content
retains the existing explicit result semantics.

Graceful completion uses the exact [finish request](engine-finish-request.schema.json)
and [acknowledgement](engine-finish-result.schema.json) from Shared Contract §11.
The request selects exactly one direct assessment, committed assessment-draft
artifact receipt or unavailable reason. No prior conclusion registration is
required. Acceptance retains the assessment or unavailable reason, closes
execution admission and requires exit within 5,000 ms, with finalization pending.
The [assessment contract](ASSESSMENT_CONTRACT.md) defines normalization and reference
checks; the [completion service](../docs/COMPLETION_SERVICE.md) retains the result
and coordinates owned cleanup.

[Spool ACK](engine-spool-ack.schema.json) syntax is also implemented with a 1,024-byte
ceiling. Initial null and zero positions remain distinct. The transport must still
check launch binding, future positions, monotonic consumption and cleanup using
its own saved state. An ACK is neither an operation result nor an execution receipt.

## Validation boundary

Use Go `LoadProtocol` / `ValidateRequest` / `ValidateResponse` / `ValidateAck`, or
Python `Protocol` / `validate_request` / `validate_response` / `validate_ack`.
Supply the original request bytes when validating its correlated response.
Neither implementation executes operations or mutates campaign state.

Campaign admission, active launch/sequence tracking, recorded duplicate identity,
original effect binding, receipt membership, upload persistence, budgets and
monotonic deadlines remain mandatory runtime gates. Passing these validators is
not permission to dispatch. The operation registry and conformance tests define
the shared field names and boundaries used by runtime callers.

## Correctable argument rejection

Transport validates the closed envelope and operation bounds before delivery. The
host MUST validate the registered body before any effect. A valid envelope with
invalid arguments receives a retained, correlated error, not an execution receipt.
Successful responses require a fully valid request; error responses may correlate
to an invalid body. Malformed JSON, invalid envelope identities and unknown routes
remain transport violations. See SIMPLE_CAMPAIGN_CONTRACT.md for the ordinary workflow.

### Database setup actions

`database_response` is an optional simulation surface on a declared PostgreSQL
service. Its selector requires `service` and `call_ordinal`, and may narrow the
captured `{query, parameters, result}` request with `request_body_pointer`,
`request_body_equal`, or `request_body_contain`. It does not accept HTTP selectors,
model tool names, or a turn selector. Scopes are `once` and `standing`. Placement
selects `/rows/<row>/<column>` and optional JSON members; the host permits only
installed pointers and native validation preserves column types and row shape.

Database state uses `environment_state`/`fidelity`, a declared table policy ID as
`collection`, and a JSON-encoded object string as `key`, for example
`"{\"id\":\"document-one\"}"`. Composite keys must include exactly the declared
key columns with non-null scalar values. Equivalent JSON keys compare by value;
invalid supplied keys are not repaired. Existing-row writes require a permitted
writable-field pointer. Root `replace` requires explicit creation permission,
matching payload keys, and only declared fields; it never upserts. Removing an
injection does not undo committed state. Restore a checkpoint to reset saved data.

Both surfaces retain existing input limits, artifact ownership, origin, budgets,
no-replay rules, and the ordinary one-finish workflow. A database setup policy
rejection uses `database_setup_denied` with the setup field path and confirms that
execution did not occur. No arbitrary SQL tool or direct database credential is
provided to the Harness.
