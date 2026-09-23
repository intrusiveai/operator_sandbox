# Structured objectives and scenarios bundle

Status: accepted input design, updated 2026-09-21. The closed
[ScenarioBundle schema](scenario-bundle.schema.json) and public capability schema
are published in the local catalog with reference-chain fixtures. Full Go/Python
package conformance and runtime implementation remain prerequisites to release.
This document completes the ScenarioBundle input requirement in the
[existing shared package](SHARED_CONTRACT.md#7-full-inputs-and-skill-manifests-without-oversized-control-frames).

## 1. Ownership and execution boundary

Operator owns `operator.dev/scenario-bundle/v1alpha1`, with catalog ID
`urn:operator:schema:scenario-bundle:v1alpha1`, registered in `catalog.json`.
ScenarioBundle is the same immutable input consumed by Attack Harness. Users and
external generators use the same validator; descriptive producer labels are not an
allowlist. Submission requires no generator installation, catalog entry, signing
workflow or model-generated provenance.

Operator's installed target profiles, credentials, feedback and effective limits
control admission and every operation. A bundle requests work and may narrow limits;
it cannot select Docker settings, tools, provider profiles, endpoints or credentials.
Attack Harness chooses concrete experiments through the existing tool catalog. Scenario IDs
remain provenance, and the existing exploratory attempt origin supports experiments
without a supplied scenario. The existing conclusion accounts for objectives,
hypotheses, evidence, limitations and untested coverage.

## 2. Top-level fields

All fields are required unless explicitly optional. Unknown fields are rejected;
there is no coercion or implicit schema upgrade.

| Field | Type and meaning |
|---|---|
| `schema_version` | Exact string `operator.dev/scenario-bundle/v1alpha1`. |
| `bundle_id` | Stable descriptive input ID, distinct from the host-assigned campaign ID. |
| `producer` | Object with required `kind` (bounded descriptive label) and optional `name`, `version`. Labels never affect admission. |
| `target_requirements` | Object with required `target_id`, `capability_source_digest`; optional `capability_projection_digest` and `required_capability_refs`. See Section 4. |
| `objectives` | Nonempty array of objective records. |
| `scenarios` | Array of scenario records; empty is valid for objectives-only execution. |
| `feedback` | Object with required `requested_profile`: `black-box`, `diagnostic` or `oracle-assisted`. It is a request, not authority. |
| `evidence` | Object with required `requested_classes` (unique capability-advertised class IDs) and `limitations` (bounded strings). Empty classes mean no extra requested evidence. |
| `artifacts` | Array of immutable supporting-reference descriptors; empty is valid. |
| `coverage` | Object with required `objective_refs` and `completion_guidance`. Account for every declared objective exactly once in `objective_refs`. |
| `context` | Optional bounded descriptive string for target purpose, environment and relevant known facts. |
| `requested_limits` | Optional positive finite bounds defined below. |
| `metadata` | Optional bounded descriptive provenance entries, each with `namespace`, `key`, `value` (strings). Rich structured provenance uses inventoried artifacts. No executable selectors or implicit dependency fetching. |

Requested limit fields are optional and closed: `attempt_admissions`,
`active_seconds`, `model_tokens`, `snapshot_creations`, `snapshot_committed_bytes`,
and `harness`. The first five map to the existing cumulative campaign counters;
`harness` may contain a subset of the field names in the existing
[harness limits schema](harness-loop-limits.schema.json), with the same positive
integer types. Omitted fields use installed defaults. Resolve each supplied value
against the smaller of host, release and target ceilings, and disclose requested
and effective values. This input shape does not rename EngineContext fields or
change runtime accounting. Larger requests never enlarge effective limits.

A requested feedback profile caps the desired live view. Resolve its permitted
kinds against the configured target profile and any host narrowing under the
[feedback contract](FEEDBACK_CONTRACT.md); disclose restrictions before admission.
The resolved profile describes the harness view, not the native AttemptContext
profile. Preserve Interceptor's exact session profile for native registration;
translate the selected kinds and independently filter manifests and byte reads
under the resolved policy. A broader target profile does not require changing
or restarting the session to honor a narrower bundle.
An explicit required objective/scenario capability that needs unavailable feedback
or evidence fails validation; optional unavailable evidence is a recorded gap.
The bundle cannot change per-attempt observation selection semantics or authorize
protected oracle definitions, fixtures or event logs.

## 3. Objectives, scenarios and supporting references

### Objectives

Each objective has required `objective_id`, `description`, `required` (boolean),
and optional `priority` (integer 1–100; 1 is highest), `success_criteria` (strings),
`required_capability_refs` and `artifact_refs` (unique ID arrays).
Descriptions state the intended effect or question without prescribing a payload.
Success criteria are hypotheses/evidence expectations, not executable predicates.
`required` means the objective must be addressed or explicitly accounted for; it
does not require success, extend a budget or prevent a safety stop.

### Scenarios

Each scenario has required:

- `scenario_id`, `objective_refs` (nonempty unique objective IDs), `hypothesis`;
- `priority` (1–100), `required` (boolean), `required_capability_refs` (possibly empty);
- `guidance`, an object with `surfaces`, `action_refs`, `mutation_dimensions`,
  `composition` (arrays of descriptive strings or safe advertised IDs as applicable);
- `expected_observations`, `evidence_criteria`, `coverage_obligations` (string arrays);
- `stopping_guidance` (string).

Optional `artifact_refs` names supporting descriptors; optional `metadata` uses
the same bounded provenance-entry shape as the bundle. Action refs use the public
`action:` namespace and are optional suggestions. Unknown/unavailable suggestions
produce gaps; they become admission requirements only when separately declared in
`required_capability_refs`. Descriptive metadata remains extensible within its
bounded namespace/key/value shape. Surfaces and composition are descriptive
suggestions; typed requirements belong in `required_capability_refs`. No prose is
parsed into native commands, selectors or an alternative permission policy.

Unsupported explicit requirements in a required scenario block input readiness.
An optional scenario with unsupported requirements is retained with a validation
gap and cannot be represented as a tested route. Optional suggestions do not block
other experiments. Required scenarios are accounted for, not mandatory payload
sequences. The harness can explore other routes to the objective within policy.

An objectives-only bundle needs no placeholder scenario. Its attempts use
`origin: exploratory` and objective/hypothesis associations in the existing
strategy/coverage records. Scenario-origin attempts name a supplied `scenario_id`;
no new objective field is added to the existing EngineAttemptRequest wire schema.

### Supporting artifacts

A descriptor has required `artifact_id`, `digest` (raw `sha256:<64 lowercase hex>`),
`size_bytes`, `media_type`, `purpose`, `visibility` (fixed `operator-engine`, the existing engine-visible classification), and
`required` (boolean); optional `schema_id` and `omission_reason` (descriptive).
Required references cannot include an omission reason. Optional absent references
need an explicit omission reason accepted and frozen by the host. References from
a required objective/scenario must resolve; a reference essential to understanding
that item must itself be marked required.

`--artifacts` selects an explicit local source directory. Resolve each descriptor
only to its direct regular-file child `sha256-<hex>` derived from the raw digest;
never interpret artifact IDs or metadata as paths/URLs. Reused bytes may have
multiple descriptive references, but size/media/visibility claims must agree and
conflicting descriptors fail. Verify size/digest while copying into immutable host
staging using Section 5.1 of the product spec. No symlinks, hard links, special
files, nested mounts or unlisted source entries. Omitted files are not fabricated
as empty files. Copied inputs are governed by the existing InputTreeManifest limits.

Bundle and reference content is already the permitted engine-visible projection.
Reject known protected classifications, private configuration and forbidden fields;
never silently redact a bundle and keep its original digest. A byte/hash validator
cannot prove that arbitrary user prose is free of secrets. Trusted preparation
must expose only declared public capability fields and permitted source content.
Source citations are inert metadata; Operator never follows them as fetch requests.
Custom skills and system-prompt replacement/extension retain their existing separate
build, selection and staging rules; a reference cannot install a skill or override
the effective prompt.

## 4. Capability and identity validation

The administrator selects a target through the CLI/environment or installed
TargetProfile. `target_id` is a public logical identity checked against that
selection, not a route or native session ID. `capability_source_digest` identifies
the exact exported source retained with the input. The host must have those bytes
from discovery or the supplied offline capability export and verify the source's
own digest recipe. A hash alone does not establish source authenticity.
The public export's hash-named native companion supplies those bytes for offline
import; it is generated by Operator, not handwritten by bundle authors.

Normalize and bind that export using the Target Integration Edge and the normative
[public capability mapping](CAPABILITY_EXPORT_CONTRACT.md). Source identity records
provenance and is verified against the retained authoring export; it is not an exact
live-target pin. Without `capability_projection_digest`, admission checks the live
logical target and explicit dependencies under current policy. Unrelated capability
changes do not reject the bundle. When supplied, the projection digest must match
both the authoring and current static projection: this is opt-in exact matching.
Exclude volatile session, worker, revision, readiness and remaining budgets from
static identity. Record authoring and accepted live bindings separately; Attack Harness uses
the live execution projection with the unchanged bundle. A healthy restore uses
the existing revision transition without rewriting mounted inputs.

Capability refs are namespaced IDs in the selected projection. Requirement lists
can be empty; descriptive objectives/scenarios need no invented references. Only
top-level and required-item dependencies block admission when unavailable. Optional
item dependencies, action suggestions and evidence requests produce explicit gaps.
Wrong reference types and dangling internal objective/artifact links are errors.
The capability contract defines exact IDs, evidence classes and digest recipes.
Never synthesize
capabilities from an objective or trust a producer's claimed validation outcome.
Required/optional validation checks structural feasibility and declared capabilities,
not whether an adversarial hypothesis will succeed. Missing factual context is
reported at a bounded field path. Operator does not use a planning model to repair it.

Raw file digest identifies exact submitted bytes. `object_digest`, where used,
follows the existing `jcs-v1` canonical-object recipe over the complete bundle; this
schema has no self-digest or signature fields. Preserve both identities in input
validation provenance. A reused `bundle_id` does not overwrite earlier content;
changed bytes create a new immutable input generation. Campaign start idempotency
and campaign identity remain the existing host-owned contract.

## 5. Bounds, preparation and compatibility

Use strict UTF-8 JSON: reject duplicate keys, non-finite numbers, unknown fields,
trailing values and nesting beyond 32. Local IDs are 1–128 ASCII letters/digits or
`._:-`, with an alphanumeric first character. Public capability/action/evidence
references use the fixed namespaces and 256-character ceiling in the capability
contract; they are not attempt-local IDs. Profile/schema/media type strings
are bounded by their published schemas. Narrative strings are at most 4,096 UTF-8
bytes each; `context` at most 16 KiB. Each string/ID list is at most 256 entries.
At most 100 objectives, 256 scenarios, 4,000 artifact descriptors and 128 metadata
entries per object; unique IDs/references are checked semantically. Ordinary integers
are exact through 2^53−1. The encoded bundle ceiling is 4 MiB.

These ceilings sit inside existing shared limits: 4,096 input files/64 MiB, 1 MiB
per ordinary supporting-reference file, separate skill/manifest accounting and
cumulative runtime read limits. The full ScenarioBundle is a structured primary
input; it is not constrained by the ordinary reference-file ceiling. Reserve input
capacity for EngineContext and the prompt. Count all copied data and fail before
launch if the complete input inventory exceeds a shared ceiling. No larger control
frame, network transport or changed manifest delivery is introduced.

Validation publishes the accepted input descriptor, target/source binding, effective
limits/feedback, missing optional routes/references and exact provenance. Start
rechecks live target compatibility and runtime gates. After initialization, Attack Harness can
index and read the full immutable bundle, record selected/read/unread/omitted
references, and work through its existing bounded loop. Runtime read/turn limits
can leave untested coverage; never claim every listed scenario was exercised.

Include the published closed schemas, catalog entries and reference-chain fixtures
in `operator-contracts`; complete the shared bindings and semantic validators
before independent production implementation. Keep the existing envelope,
startup, EngineContext/manifest roles, tool request/result versions, feedback,
restore and conclusion/stop behavior. Do not claim that this document alone
publishes or qualifies that package.

## 6. Examples and required conformance

- [Objectives-only submission](fixtures/scenario-bundle-objectives-only.json).
- [Scenario-guided submission](fixtures/scenario-bundle-scenarios.json).

These two illustrative submissions use synthetic target IDs/digests. The
[Interceptor-derived reference chain](fixtures/capability-chain/README.md) adds an
exporter-produced source, actual public projection and bundle using verified fixture
digests. All examples require no artifacts or external generator provenance. Conformance must include both
input forms and generic producer labels, supplied optional provenance, malformed
JSON/unknown fields, missing/duplicate/dangling IDs, changed digest/source files,
required versus optional gaps, path/link attacks, oversized inventories, forbidden
capability/configuration claims, policy narrowing, compatibility-mode live changes
and exact-pin static mismatch.
Include a shared fake-host/harness trace proving that each form uses the existing
startup, attempt origin, coverage/conclusion and stop contracts. Preserve all
existing operation-schema and runtime qualification gates.
