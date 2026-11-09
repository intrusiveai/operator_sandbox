# Model tool normalization

Shared package 0.8.0 defines the model interface separately from canonical host
operations. Both products MUST pin its exact identity using the existing package
and image compatibility mechanisms. Model arguments from earlier packages MUST
NOT be accepted as a fallback. Host receipt schemas remain authoritative.

## Shared API

Go exposes `Protocol.NormalizeToolArguments(name, raw, ToolResolution)`; Python
exposes `Protocol.normalize_tool_arguments(name, raw, context)`. Both return:

- `arguments`: the canonical host request body, or local publication/read helper
  arguments for `artifact_publish` and `reference_read`.
- `paths`: canonical host JSON Pointer to model JSON Pointer mappings. Use
  `RemapToolPath` / `tool_normalization.remap_tool_path` for the longest matching
  boundary prefix; preserve the host error code, reason and effect state.
- `observation_selections`: bounded reference facts for assessment fields that
  have no canonical observation field. Retain these as described below.

The trusted resolution context is product state, never part of tool arguments:

```json
{
  "campaign_id": "campaign-1",
  "call_id": "native-call-1",
  "resources": {
    "obs-1": {
      "kind": "observation", "campaign_id": "campaign-1",
      "available": true, "inspected": true, "live": true,
      "value": {"attempt_receipt_id": "receipt-123", "entry_id": "entry-456"}
    }
  },
  "database_keys": {"database/accounts": ["id"]}
}
```

Harness MUST supply only entries from verified input manifests or successful host
results, scoped to this campaign. At most 4,096 selected resource entries fit the
bounded context; it need not contain the entire runtime map. The map MUST survive
conversation compaction without being placed in prompts. Descriptive prose,
observations and model-written dictionaries MUST NOT populate it. A model can
select a handle, never assert what it resolves to. Labels are bounded identifiers,
not authorization prefixes. Operator independently validates expanded identities.

| Resource kind | Required canonical `value` fields | Additional checks |
| --- | --- | --- |
| execution | `attempt_receipt_id` | Must originate from a dispatched host result, never a local rejection. Preserve uncertain outcome status. |
| observation | `attempt_receipt_id`, `entry_id`; optional `range` | Range is `{offset,length}` with positive length. `inspected` means verified complete bytes for exactly this entry/range; listing is insufficient. |
| artifact | `artifact_receipt`, `purpose` | Available, successfully committed; finish requires conclusion purpose. Host validates descriptor, content and purpose again. |
| record | `record_receipt_id` | Accepted historical record, never artifact or attempt receipt. |
| hypothesis | `hypothesis_id` | Accepted historical hypothesis; conclusion-local hypotheses need no earlier record. |
| reference | `reference_id`, `entry_id`; optional `range` | Verified input/reference entry, including task/resource/scenario/tool guides. No filesystem paths. |
| injection | `attempt_receipt_id`, `action_id` | `live` must be true for the current target binding. Revalidate after restore. |
| checkpoint | `source_session`, `checkpoint_id` | Identity does not imply ready, available or restored. Host rechecks campaign/session availability. |
| session | `source_session` | Verified session within the current campaign lineage. |

The `available`, `inspected`, and `live` booleans default false, never true.
References used as observed content require available and inspected evidence.
Unread/unavailable observations may describe uncertainty entries. A partial read
MUST produce an exact range handle; a whole-entry handle becomes inspected only
once all bytes were verified. For range reads, model `offset` remains absolute within the original entry.
It MUST lie inside the selected range; normalization caps the read to the remaining
range length. Omitting offset means zero (which can be outside a range handle)
and omitting max_bytes means 4096. Invalid
supplied bounds MUST fail rather than become defaults.

`database_keys` maps `service/collection` to the exact permitted key columns from
verified target capabilities. Environment-state row keys are native JSON objects
with those non-null scalar columns. Normalization canonicalizes the object to the
host's existing string key. File namespace selectors remain unchanged. Native
selector, placement, policy and injection-content checks still apply.

## Stable identities and reference expansion

Generated IDs are `generated-` plus the first 32 lowercase hexadecimal characters
of SHA-256 over UTF-8 `campaign_id + "\n" + call_id + "\n" + model_path`.
Paths are `/setup/N`, `/claims/N`, `/hypotheses/N`, `/uncertainties/N`, or
`/hypothesis` for a historical hypothesis record. `call_id` is the stable native
provider call identity assigned/retained by Harness, never model arguments.
Repeated delivery of identical arguments MUST retain that identity and result.
Changed arguments under the same identity MUST conflict in the existing dispatch
ledger, before normalization can cause effects. New corrected calls use new IDs.

Resolve every local hypothesis/uncertainty label before expanding any references.
Labels MUST be unique and distinct from existing resource handles. Wrong-kind,
unknown, duplicate, ambiguous, self-parenting and cyclic references MUST fail.
Unlabelled declarations still get IDs but cannot be referred to by a model label.
Uncertainty references resolve only to labels in this assessment, never historical
resource-map entries. Historical hypothesis references use verified resources; no preregistration of
new assessment hypotheses is required.

For each item, normalize observations in model order, deduplicate exact references,
and derive the stable union of their execution receipts followed by explicitly
selected attempts. A claim's observation references and receipt list MUST agree.
No outcome, confidence, assurance, claim or hypothesis is inferred from execution.
Optional lists become empty; inline assessment status defaults to partial.

The existing `engine.diagnostic_report` accepts `observation_selections` batches
of 1–32 `{field_path,observation_ref}` entries for objectives, hypotheses and
coverage. Send the existing diagnostic with source `harness-local`, stage
`references`, reason `observation_selection`; do not send `stopping`. The host
validates campaign references, visibility, availability and ranges before retaining
these facts. These records describe Harness selections, not host claims of truth,
and MUST NOT replace a terminal failure or appear as rejected submissions. Persist
all batches before submitting the expanded assessment/record. A failure to retain
required selection facts MUST prevent that submission, using existing bounded
failure/finalization handling. Duplicate batches use the existing operation ledger.
No additional model turn is required. Tool-call arguments/normalization remain
runtime history; no entire handle map is copied into the model context.

## Validation and limits

Use the shared strict decoder before JSON keys can be discarded. Validate the
model schema, resolve resources, expand, then validate the expanded host schema.
The caller MUST also apply `ValidateRequestSizes` / `validate_request_sizes` using
frozen host allowances and the complete operation envelope before transmission.
Existing wire, target-input, observation, artifact, budget and lifetime limits
remain in force. Direct input uses exactly one present content field; JSON null is
content. Oversize input directs the caller to artifact publication.

Local failures expose stable `reason`, safe `field_path`, and `correction` through
existing ArgumentError/ContractError facilities. Harness supplies tool/call and
no-execution/correction-safe facts to its existing diagnostic reports. No input
prose, unrestricted keys, credentials or bodies belong in diagnostics. Host paths
are translated for model feedback; host authoritative codes and effects are kept.

Canonical host validation still owns artifact purpose/integrity, objective and
scenario membership, observation visibility/ranges, execution lineage and budgets.
A resource map is a translation aid, not authority. Successful restoration does
not revive stale live resources or replay uncertain work. Acceptance of finish
closes admission; exit status and cleanup completion remain separate host facts.

## Model result presentation

The Harness presentation uses the same resource noun as the next tool argument.
These names are shared integration conventions, not a replacement host response:

- Attempt results retain authoritative `status` and effect/dispatch facts. A real
  execution adds `execution` (execution handle); `observations` is a list whose
  entries carry `observation`, `kind`, `availability` and source/visibility facts.
  Retained injections carry `injection` in `injections`. Rejection does not create
  an execution handle. A receipt for unknown effects still reports unknown effects.
- Observation reads MUST return `readable_observation` for the original readable
  handle and `citation` for exactly the verified inspected entry/range. These are
  distinct roles, even when a complete read allows both to name the same handle.
  Keep availability, content/encoding, original-entry offset, raw_length, EOF and
  truncation. A failed read MUST NOT create a citation or mark bytes inspected.
  When more bytes are available in the readable extent, return `continuation` as
  `{"tool":"observation_read","arguments":{"observation":"<original readable handle>","offset":<next offset>,"max_bytes":<permitted chunk size>}}`.
  The arguments MUST be directly executable in current verified state; omit
  continuation at EOF, unavailability, or the end of the selected readable extent.
  Do not advertise a next offset outside a range-limited handle. The model MUST
  cite `citation`, not the unread whole-entry handle. Host wire results remain
  unchanged; this is the shared model presentation convention.
- Artifact publication returns `artifact`, purpose, media_type and size_bytes only
  after a successful commit. Historical append returns `record` and, for a
  hypothesis, `hypothesis` after acceptance.
- Snapshot results/list entries return `snapshot` and `session` when those
  authoritative identities exist, with the original status/readiness and bounded
  metadata. Listing preserves continuation. Restore/deletion preserve actual
  effect statuses and never imply reversal or replay of mutations.
- Reference reads return `reference` and source/range/continuation facts. Finish
  retains accepted/rejected, assessment_state, execution_admission,
  finalization_status and exit instructions. Acceptance MUST NOT imply cleanup.

The product MUST form these presentations from verified host results and retained
resource state. Preserve host results in evidence; remove repeated descriptors
from ordinary model results. Model result text is not a new source of authority.


## Correctable finalization and token admission

Tool guidance MUST include the generated minimal inline finish example, optional
structured uncertainties, allowed enum values, and the execution/observation/record
handle distinctions. Schema failures MUST return a bounded field path and
schema-authored expected type, missing field or enum values when available, never
submitted values. Reference errors MUST use verified runtime categories. They
SHOULD suggest up to three same-entry inspected handles or the required next read.
A corrected finish uses a new call identity and MUST NOT replay completed attempts.

Harness SHOULD begin finalization while the remaining token budget can fund an
assessment request and one bounded correction opportunity, using its existing
conversation and admission accounting. This is best effort, not a guarantee of a
valid assessment. If the next request cannot be funded, Harness MUST use the
structured model-token stopping report in SHARED_CONTRACT.md and an unavailable
finish. Previous correctable errors remain separate history; no evidence-read
counter may stand in for a model-token admission failure.
