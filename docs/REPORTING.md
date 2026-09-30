# Retained reports and portable exports

```sh
operatorctl report --run ./runs/example
operatorctl export --run ./runs/example --output ./exports/example
operatorctl report --campaign ID --state-root /absolute/private/state
```

## Execution and publication

Report/export MUST operate entirely on retained files, under the selected campaign
writer lock. They MUST NOT contact Docker, Interceptor, model providers or secret
stores, terminate processes, replay operations, collect archives, or claim recovery
work. Active writers MUST prevent generation. `--run` MUST resolve the saved start
link; it MUST NOT accept an additional campaign/config/state-root selector.

The campaign worker MUST attempt local report generation after execution and
its durable completion recording, with a separate five-minute timeout. It MUST
return `reporting: generated | report_failed | not_started` separately from
execution and completion-recording status. A report error MUST NOT convert an
execution outcome into failure or defer target cleanup. Explicit report/export
commands MUST allow later retries from retained evidence. Process loss before this
pass MUST NOT cause startup to resume a campaign or automatically generate reports.

Each generation MUST live under
`campaigns/<id>/reports/<generation-sha256>/`. It MUST contain `report.json`,
`report.md`, `result-manifest.json`, `execution-bundle.json`,
`target-evidence-set.json`, projected `events/<part>.jsonl`, retained public input
and content copies, and a final `generation.json` inventory. The source execution
journal MUST remain unchanged. Generation MUST use private temporary directories,
synced regular files, and final directory publication. Failed derived staging MUST
NOT be published; interrupted staging is disposable, not adopted evidence.

Identical source facts and bytes MUST produce identical generation bytes and IDs.
Timestamps MUST come from retained source records, not generation time. Repeated
generation MUST rehash and reuse an identical existing generation. Corrupt existing
generations MUST fail verification, never be overwritten. Later evidence/cleanup
records MAY produce a new generation; earlier generations MUST remain unchanged.

## Exchange documents (v1alpha1)

These are administrator result contracts, separate from the host/harness wire
package. The serialized Go types in `internal/reporting` and `internal/campaign`
MUST implement the following document relationships. All document digests MUST use
SHA-256 over canonical JSON (JCS); copied byte files and JSONL parts MUST use raw
SHA-256. Digest strings MUST use `sha256:<64 lowercase hex>`.

| Document | Required contents and binding |
| --- | --- |
| `execution-bundle.json` | `api_version: operator.dev/campaign-execution-bundle/v1alpha1`, campaign ID, complete RunManifest, journal integrity, verified event count, ordered source-part descriptors, visibility and claims-authority declarations. |
| `target-evidence-set.json` | `api_version: operator.dev/target-evidence-set/v1alpha1`, campaign ID, administrator-native-evidence visibility, ordered selected sessions with exact native identities, collection outcomes and archive records. |
| `result-manifest.json` | `api_version: operator.dev/result-manifest/v1alpha1`, campaign/RunManifest digests, execution-bundle and evidence-set digests, independent execution/cleanup fields, attempt/coverage/usage summaries, harness claim count, explicit gaps and unknown operations. |
| `report.json` | `api_version: operator.dev/campaign-report/v1alpha1`, exact result-manifest digest and deterministic summary. |
| `generation.json` | `api_version: operator.dev/report-generation/v1alpha1`, campaign ID, RunManifest digest and sorted file descriptors (`path`, `size_bytes`, `digest`). Its canonical digest is the generation ID. It MUST NOT list itself. |
| `export.json` | `api_version: operator.dev/campaign-export/v1alpha1`, generation digest and sorted copied-file inventory, including `generation.json` and verified native tar archives. It MUST NOT list itself. |

ResultManifest MUST NOT reference a report or generation digest. This prevents a
reverse digest dependency. Report receipts MUST identify the campaign, generation,
result digest and journal integrity. Generation success MUST mean publication,
not campaign success or complete evidence. Explicit command exit status zero MUST
allow a successfully published report that clearly contains evidence gaps.

## Facts, uncertainty and coverage

Reports MUST distinguish:

- Host-recorded submission, admission, dispatch, invocation and cleanup states.
- Scoped feedback entries/categories, their native source/assurance, visibility,
  availability and truncation. Actual feedback bytes remain digest-bound content.
- Harness assessment records as non-authoritative claims, with their original
  structured content retained for administrator inspection.
- Native archive integrity/completeness as distinct from a successful experiment.
- Recorded target closure, injection cleanup, target stop, container cleanup and
  emergency termination observations as independent facts. A termination record
  MUST NOT imply current Docker state or container removal.

The aggregate oracle verdict MUST remain `unknown`; the deterministic summarizer
MUST NOT convert text, model conclusions, archive completeness or a successful
invocation into a demonstrated exploit. Scoped oracle observations and predicate
details remain in retained feedback and native evidence for independent consumers.

Attempt rows MUST preserve stable request/attempt IDs, attempt index, revision,
scenario provenance, state and dispatch flag. Objective references MUST come from
the accepted ScenarioBundle. Rejected/non-dispatched attempts MUST NOT satisfy
scenario/objective coverage. Exploratory attempts MUST NOT fabricate objective
coverage. Coverage means recorded attempt coverage, not objective achievement.
Unattempted scenario/objective IDs and operation IDs MUST be explicit. Injection
route entries MUST bind canonical policy-route digests and surface names, with
`not-attempted` or `arm-attempted` state derived from retained native step dispatch;
arming MUST NOT be called observed injection delivery.

Usage fields MUST retain the highest recorded cumulative counters across revisions,
including attempts, models and snapshots. Absent counters MUST remain absent,
not be synthesized as zero. Incomplete journals MUST mark integrity false, retain
only the verified prefix and unresolved operations, and leave execution unknown.
Missing launch-input completion, native evidence, coverage or invalid cleanup/
termination records MUST produce explicit gaps. Reports MUST NOT repair sources.

The native evidence set MUST include the recorded source/replacement lineage,
live outcomes, latest explicit collection outcome, and all adopted imported
archives. Archives MUST be rehashed before being marked verified. Unavailable or
corrupt archives MUST remain explicitly unverified and MUST NOT be copied during
export. Report generation MUST NOT create collection or cleanup intents.

## Visibility and portable data

Projected source events MUST retain sequence/revision, kind, canonical original
event digest, bounded allowlisted bookkeeping metadata, and content inventories.
The export MUST preserve exact approved input/model/guest request-response,
feedback, artifact and claim bytes using `content/<raw-sha256>` names. Launch input
parts MUST retain their logical relative input path and part metadata. The accepted
bundle MUST also be available as `input/scenario-bundle.json`.

Host preparation/control documents MUST be omitted from copied source content;
their inventory entries MUST say `host-only-omitted`. Metadata MUST use an explicit
allowlist rather than exporting arbitrary host maps. Docker endpoints, private
storage locations, host credential selectors, secret-store settings and credential
values MUST NOT be supplied by the exporter. Original target, submitted, model and
native evidence can contain sensitive application data; these are administrator
evidence exports and MUST NOT become new harness-visible feedback.

Content inventories MUST distinguish administrator evidence from omitted host
records. Native archive references MUST use `evidence/<raw-sha256>.tar`. Every
copied file MUST have an integrity descriptor. Consumers MUST verify descriptor
bytes/digests and document bindings before relying on an export; the projected
event digest binds the original host event but is not a claim that omitted fields
can be reconstructed. Export MUST NOT traverse arbitrary retained directories.

## Bounds and failure handling

Generation MUST admit at most 100,000 verified journal events and 100,000 derived
files. JSON documents/inventories MUST be bounded to 16 MiB; JSONL parts MUST be
bounded to 4 MiB. Source journal files/content retain their existing limits.
Each write MUST check the saved free-space floor with an additional 16 MiB metadata
allowance. Native archive copies MUST stream with bounded memory, digest and size
checks. The campaign lock MUST remain held throughout export to exclude collection,
another report, or later purge. These checks are not filesystem reservations.

Export MUST exclusively create a new private output directory outside the managed
state root. It MUST reject existing destinations, including partial exports and
links. Files MUST be synced and the final `export.json` marker published last.
An interrupted export without that marker MUST remain incomplete. Retrying MUST
use a new destination; export MUST NOT overwrite or silently adopt partial output.
Explicit external copies are administrator-owned and survive campaign purge.

Tests MUST cover deterministic reuse, changed evidence generations, archive and
generation corruption, active writers, size/space failures, cancellation, unsafe
paths, partial export, independent worker reporting failure, actual host attempt
and feedback records, and unchanged source journals. These tests MUST NOT be
described as live host/target/provider qualification.
