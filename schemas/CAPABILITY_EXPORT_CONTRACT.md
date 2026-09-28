# Public capabilities and compatible bundle admission

Status: accepted MVP contract, 2026-09-21. The closed public capability and bundle
schemas are in the offline catalog. The Go host capability/bundle library verifies
and projects native exports, checks compatible submissions and preserves live
binding/policy records; see the [implementation guide](../docs/CAPABILITY_ADMISSION.md).
The fixture runner checks the reference chain. Submission/validation/start and
broker integration are implemented.
The public capability-export CLI is implemented. Complete process/native conformance
and package publication remain outstanding. Existing Attack Harness tool schemas are unchanged.

## 1. Public export

`operatorctl capabilities export` produces
`operator.dev/target-capability-manifest/v1alpha1`, validated by
[target-capability-manifest.schema.json](target-capability-manifest.schema.json).
The administrator supplies the logical `target_id` through the installed target
configuration; a bundle cannot choose a native route. The public export contains:

- `source`: adapter identity, native schema version, native
  `capability_source_digest`, and `raw_digest` of the exact retained native bytes.
- `capabilities`: the deterministic static projection described below.
- `capability_projection_digest`: `sha256` of `jcs-v1(capabilities)` only. Source
  metadata and this self-digest are outside that payload.

Export also writes the verified native bytes beside the public JSON as
`sha256-<source.raw_digest hex>.json`. `--capabilities` imports the public file and
this deterministic companion (or an already verified host-retained copy with the
same raw hash). Each is a bounded regular file; use no-follow reads and reject
links or hash mismatches. The companion is at most 4 MiB and stays in host
provenance storage, outside the Attack Harness input projection. Authors copy references
and source hashes from the public file; they never construct this companion or
calculate hashes themselves. No URL lookup or arbitrary source pathname is used.

Arrays of records are sorted by `ref`; feature IDs and set-valued string arrays
are sorted lexically and deduplicated. Native duplicate identifiers are errors,
not last-writer-wins. Delivery examples preserve order, and embedded schemas and
example JSON preserve their values; do not sort arrays inside those documents.
Within services, sort endpoints by `id` and creatable collections by `collection`;
reject duplicate identifiers within each enclosing service.
Absent optional fields stay absent. Normalize native null collections to empty
arrays only in the public projection, never when verifying the native source.
No volatile session, worker, revision, readiness, active profile or remaining
budget fields enter the static projection. Host runtime permissions are applied
separately; an export describes capabilities, not authority to execute them.

The full export is at most 4 MiB, with at most 256 records per capability array.
It follows the existing strict UTF-8/duplicate-key/depth/exact-number rules.
Narrative byte ceilings remain semantic checks: JSON Schema maxLength counts
characters, not UTF-8 bytes. Preserve native delivery limits: 256 KiB per contract,
64 KiB per embedded schema, eight examples, 16 KiB per encoded example, and the
`interceptor.delivery-schema/v1` offline schema profile. Schema/examples are inert
input descriptions, not extensible operation envelopes or executable configuration.
Unknown adapter/native versions fail explicitly; never guess a mapping.

## 2. References authors can copy

| Reference | Meaning and deterministic construction |
|---|---|
| `operation:<id>` | One application operation, e.g. `operation:invoke`. Its `operation_id` remains the native operation ID (`invoke`) used in the existing Attack Harness invocation field. |
| `action:injection:<surface>` | One injection action capability, e.g. `action:injection:mcp_tool_result`. Its execution type remains `interceptor.injection/v1alpha1`. |
| `service:<id>` | A logical service and its safe delivery/selector context. |
| `file_namespace:<id>` | A logical mutable-file namespace, never its host/container root. |
| `evidence:<kind>` | One permitted feedback class: `target_output`, `operation_error`, `injection_delivery` or `oracle_outcome`. |
| `feature:snapshots` | The adapter supports target checkpoint creation/restoration. |

References are at most 256 ASCII characters, using the existing ID character set
plus the fixed prefix. Native logical IDs and attempt-local IDs remain at most
128 characters. A reference names an item in one selected target export; it is not
a global authority, URL, route or artifact path. Service endpoint IDs are local to
the enclosing service and are not additional capability references in this version.
Do not derive public IDs from descriptions, list positions, session IDs or hashes.

`required_capability_refs` can name any advertised reference. `guidance.action_refs`
can name only `action:` records. `evidence.requested_classes` can name only
`evidence:` records. A wrong reference type is a structural error. An attempt's
`pre_actions[].action_id` is a newly chosen local handle, not an advertised action
reference. Keep cleanup handle semantics and all existing operation wire fields.

Authors may leave requirement/action/evidence lists empty. Objectives and scenario
prose do not imply hidden dependencies. Attack Harness can choose any currently permitted
route, including capabilities absent from the authoring export, after ordinary
host validation. Descriptive guidance is never parsed into an executable plan.

## 3. Interceptor mapping `interceptor/v1`

Verify the native export with Interceptor's actual typed manifest digest recipe:
JSON-marshal the supported native Go representation with `Digest` set to the empty
string (the field remains present), preserving its struct field order, map-key
ordering, escaping, omission and null semantics. Hash those bytes. This is NOT
JCS or a hash of the formatted export file. Keep the raw-file hash separately.
The supported inputs are capability-manifest/v1alpha1 and /v1alpha2; delivery
contracts require the v1alpha2 `interceptor.delivery-schema/v1` profile. Missing
legacy delivery information remains an explicit limitation, not an inferred schema.

| Native source | Public projection / rules |
|---|---|
| Installed logical identity | `capabilities.target_id`; fixed adapter `interceptor/v1`. Native release, target version and environment/application digests remain source provenance, not automatic live-match requirements. |
| `operations[]` | `operations[]`: prefix ID, preserve `operation_id`, `delivery_status`, `maximum_input_bytes` and validated `delivery` when present. No input contract means `missing-input-contract`; such an operation cannot satisfy a runnable-operation requirement. Preserve descriptions, schema basis and benign examples. Native method/path stay in the host route map. |
| `injection_profiles[]` | `actions[]`: one prefixed surface with fixed action type, scopes, placements, selector-field names and carriers. Publish only supported surfaces when both `injection.arm` and `injection.delete` are advertised. This lists action families, not permission to select arbitrary services/files. |
| `services[]` | `services[]`: prefix `id` as `ref`, rename `id` to `service_id`; copy only kind, role, transports, injection_surfaces and optional implementation, response_mode, mcp, endpoints, creatable_collections. Preserve validated public endpoint contracts, public logical paths, injection roots and output pointers. These describe selectable content, not network destinations for the harness. |
| `file_namespaces[]` | Prefix ID, rename to `namespace_id`, copy allow_create and optional max_files/max_file_bytes. Never export actual roots. |
| `feedback_profiles`, `feedback` | Keep sorted supported profiles. Publish one evidence record per known kind permitted by at least one supported profile, only when compatible observation view/content operations exist. Each record carries the kind and the subset of supported profiles permitting it. Apply the live effective feedback policy at admission and every read. |
| `snapshot_capable` | Add `feature:snapshots` only when true and native snapshot.create is supported; this mapping's installed lifecycle adapter must also support restore. |
| Other native fields | Host-only provenance/validation. Do not copy generic native broker operations, network/provider/authentication configuration, raw oracle definitions/fixtures or event logs into public execution choices. |

Each service's injection surfaces and each namespace's policy still constrain an
action family. Do not infer create permission, endpoints, output structure or
negative oracle outcomes from an advertised family. Unsupported oracle types do
not become runnable predicates. The four evidence classes describe receipt-scoped
live feedback, with the existing assurance/availability limits; administrative
post-run exports are not additional harness-readable classes in this MVP.

The schema closes all contract records; only embedded delivery schemas/example
values use their native data shape. Adapter mapping must validate those using the
native delivery profile before publication. Native fields not listed above are
not exposed. Unknown native enum values are incompatibilities, not copied metadata.

## 4. Admission: compatibility by default, exact match by request

1. Verify submitted bundle structure, unique IDs, references and artifact inventory.
   Resolve its source digest to the retained authoring export for the selected
   logical target and verify both source and public projection integrity. The
   source digest is required provenance; it does not require the live export to
   have the same digest. Never accept a bare hash without its verified source.
2. Obtain and verify the current export for that same configured logical target.
   Resolve current policy, active native feedback profile and effective harness
   feedback. Both authoring and live bindings are retained in validation records.
3. With no bundle `capability_projection_digest`, check only explicit dependencies
   and general runtime readiness against the live projection. Added unrelated
   capabilities, release/environment changes and changed delivery descriptions
   do not themselves block admission. Even a changed input schema can be accepted
   when the operation is still runnable: Attack Harness uses the current contract, and every
   concrete attempt must validate against it. Prose is never treated as a schema pin.
4. If a projection digest is supplied, it must match the verified authoring
   projection AND the current static projection. This is the author's opt-in exact
   mode. A mismatch blocks admission before target effects and asks for a newly
   validated submission; the host never rewrites or silently drops the pin.
5. Top-level required refs and refs on required objectives/scenarios must exist
   and be usable under live policy. Action requirements also require at least one
   currently selectable route; evidence requirements need effective feedback
   permission. Generic service/namespace refs assert availability of that scope,
   not support for an unstated endpoint or mutation. Concrete selectors still
   require per-attempt validation. Missing required dependencies block admission.
6. Refs on optional objectives/scenarios that are missing/unusable produce explicit
   gaps and disable those routes, not the campaign. Unknown optional refs are
   reported, not invented. Guidance action refs and requested evidence classes
   are optional suggestions unless also listed as explicit required dependencies;
   their absence or withholding produces gaps, even on a required scenario.
   Known required refs absent from the authoring export are invalid; optional ones
   remain reported gaps. An author must obtain a fresh export to assert a new
   required dependency. All internal objective/artifact links must resolve.
7. Freeze the live execution projection, the effective policy and the validation
   record with the immutable campaign inputs. Attack Harness receives the current execution
   context plus the unchanged authored bundle and its source provenance. It must
   not compare the bundle source digest directly to a live-source digest and reject
   an accepted compatibility-mode binding. Preparation-to-start changes trigger
   the same checks again; no already-mounted input is rewritten. Healthy restore
   keeps the established live projection and existing restore compatibility rules.

Compatibility is an admission rule, not runtime authorization. The current
TargetProfile and typed per-attempt checks remain the source of permission. No
new approval workflow, ownership model, harness tool or remote lookup is added.

## 5. Fixtures and implementation boundary

[capability-chain/README.md](fixtures/capability-chain/README.md) describes a real
native exporter fixture, its digest preimage, public projection and submitted
bundle. Run `python3 schemas/validate_capability_fixtures.py` with `jsonschema`.
Positive/negative checks include objectives-only input, required/optional refs,
namespace mistakes, source/projection integrity, unrelated live changes and exact
pins. The fixture mapper is a conformance aid, not a production authorization layer.
Native runtime policy/route checks and Go/Python structural, semantic and JCS
validation are implemented. Public capability-export CLI integration, complete
Operator/Attack Harness process tests, live target qualification and release
publication remain outstanding. See [implementation status](../docs/IMPLEMENTATION_STATUS.md).


## HTTPS adapter projection

For `https/v1`, Operator MUST derive capabilities from the private mapping defined
in [HTTPS targets](../docs/HTTPS_TARGETS.md). The source companion MUST use
`operator.dev/https-capabilities/v1alpha1` and contain only the logical target ID,
canonical private mapping digest and sorted operation delivery declarations.
Its source digest MUST be SHA-256 of its canonical bytes. Public operation input
media types and byte bounds MUST match the mapping; action, service, filesystem
and feature arrays MUST be empty. Source and projection adapter IDs MUST agree.

This declaration MUST NOT claim observed remote capabilities. Its delivery input
basis is `provider-declared`; output structure has `unknown` basis. Feedback
profiles MUST be limited to black-box/diagnostic, with only target output and
operation errors advertised. Private URLs, headers, fixed request data,
credentials and CA configuration MUST NOT be exported. Import MUST reproduce and
compare the exact public projection. Campaign startup MUST independently
rederive execution capabilities from the frozen private mapping. The shared
[HTTPS fixture](fixtures/https-capability-chain.json) MUST pass Go/Python checks.

The host's existing source-companion storage fields MAY retain their native
names for compatibility; consumers MUST interpret provenance using `adapter`.
An HTTPS execution binding's opaque session field identifies local campaign
attribution only. It MUST NOT imply a remote session, readiness lease or reset.
