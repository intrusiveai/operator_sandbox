# Startup and manifest validation

This document fixes the startup messages and manifest inventories implemented in
the Go/Python shared library. It supplements [SHARED_CONTRACT.md](SHARED_CONTRACT.md).
It does not publish package `0.1.0` or implement a launcher or harness runtime.

## Control messages

The closed [host](engine-host-control.schema.json) and
[guest](engine-guest-control.schema.json) unions use the common envelope fields
`api_version`, `kind`, `seq`, `campaign_id`, `launch_id`, `run_revision` and `body`.
Every encoded message is at most 64 KiB. Sequence numbers are independent in each
direction. A successful startup consists of exactly these messages:

| Direction / sequence | Kind | Body |
|---|---|---|
| Host / 0 | `bootstrap` | Full Docker `container_id`, `contract`, `release`, `runtime_profile`, `host_platform`, `transport`, `run_manifest_digest`, `timeout_ms`. |
| Guest / 0 | `confinement_ready` | Echo all bootstrap body fields except `timeout_ms`. |
| Host / 1 | `initialize` | `input_tree` and `skill_set` descriptors, initial `binding`, `engine_context_object_digest`, `timeout_ms`. |
| Guest / 1 | `initialized` | Echo the initialize body except `timeout_ms`, plus the bootstrap `contract`. |
| Host / 2 | `admission_open` | Verified initial `binding`, sorted advertised `operations`, `limits`, `remaining_limits`. |

`contract` contains `version`, `digest`, `catalog_digest` and `operations_digest`;
`release` contains `image_digest` and `release_record_digest`. Runtime profile is
`operator-container/v1`. Host platforms are `linux/amd64`, `linux/arm64`,
`darwin/amd64` and `darwin/arm64`; Linux uses `fifo`, macOS uses `spool`.
Each supplied startup timeout is 1–60,000 ms, additionally bounded at runtime by
remaining campaign time. The complete example is in
[startup-example.json](fixtures/startup-example.json).

All five messages retain the same campaign, launch and initial revision. The
initial [binding](conclusion-binding.schema.json) must match these envelope fields,
the bootstrap package/release pins and the manifest descriptors. Healthy target
restores retain the harness and do not repeat startup.

Admission `limits.harness` contains the resolved
[effective harness limits](harness-loop-limits.schema.json). `limits.campaign` and
`remaining_limits` both use the closed [remaining limits](remaining-limits.schema.json)
shape: the former supplies campaign ceilings, the latter supplies current
allowances. Every allowance must fit its ceiling. Campaign model turns and
observation bytes must also fit the corresponding harness limits. These are
explicit projections; no missing-field defaults are inferred. Advertised operations
must be sorted, unique and present in the installed registry. The current development
registry has 13 operations, including model generation.

Host `terminate` is valid throughout the launch, including startup. Its body has
`reason`, optional `stop_receipt`, and `exit_required: true`. Reasons are
`user-request`, `deadline-exceeded`, `protocol-error`, `containment-failure`,
`resource-limit`, `host-error`, `target-failure`, `runtime-interrupted` or
`campaign-stopped`. Termination applies to the campaign/launch even if its revision
is stale. It is best effort, requires no acknowledgement and never delays the
independent Docker termination path. It cannot complete a successful startup.

## Fixed manifest locations

Descriptors contain `schema_id`, `size_bytes`, raw-byte `digest`, canonical
`object_digest`, and either `path_id` or an indexed `slot`. They never accept an
arbitrary filesystem path. The mapping is:

| Descriptor | Read-only guest path |
|---|---|
| `path_id: input-tree` | `/run/operator/manifests/input-tree.json` |
| `path_id: skill-set` | `/run/operator/manifests/skill-set.json` |
| Skill `slot: 0` through `15` | `/run/operator/manifests/skills/0000.json` through `0015.json` |

The host stages complete inventories separately from control frames. Manifest
reads begin only at `initialize`, after confinement and host admission gates.

[InputTreeManifest](input-tree-manifest.schema.json) inventories files beneath
`/run/operator/input/`. Entries have `entry_id`, `root_kind: input`, `path`, `role`,
`media_type`, `size_bytes`, `digest` and, where applicable, `schema_id`. IDs are
unique. Exactly one of each core role is required:

| Role | Relative path | Content |
|---|---|---|
| `engine-context` | `run-context.json` | JSON, schema ID `urn:operator:schema:engine-context:v1alpha1`. |
| `scenario-bundle` | `scenario-bundle.json` | JSON, existing ScenarioBundle schema, at most 4 MiB. |
| `system-prompt` | `system-prompt.txt` | Plain text, nonempty, at most 128 KiB. |
| `reference` | `artifacts/sha256-<raw digest hex>` | Optional reference files, at most 1 MiB each. |

The [input-content contract](ENGINE_INPUT_CONTRACT.md) defines EngineContext and
inline prompt provenance. Its launch-content validator checks actual supplied
context, bundle and effective prompt bytes against this inventory; manifest-only
validation does not check those file contents.

[SkillSetManifest](skill-set-manifest.schema.json) contains `loader_schema`,
`loader_digest`, `loading_digest` and `skills`. Loader schema is
`operator.dev/instruction-skill-loader/v1alpha1`. Each selected skill has `skill_id`,
`bundle_digest` and its manifest descriptor. Skills sort by ID, IDs cannot collide
under case folding, and slots are consecutive starting at zero. An empty skill set
is valid and still has an explicit manifest.

Each [SkillManifest](skill-manifest.schema.json) contains `skill_id`, `name`,
`description`, `entrypoint: SKILL.md` and its complete `files` inventory. Files
have `path`, `media_type`, `size_bytes` and raw `digest`; paths resolve beneath
`/run/operator/customer-skills/<skill_id>/`. `SKILL.md` must be nonempty Markdown.
Allowed media types are Markdown, plain text, JSON and YAML, as enumerated in the
schema. Files remain passive instructions/references.

## Bounds and portable paths

| Inventory | Encoded ceiling | Data ceiling |
|---|---|---|
| Input tree | 8 MiB | 4,096 files / 64 MiB |
| Skill set | 64 KiB | 16 skills / 64 MiB across all selected skills |
| Each skill | 2 MiB | 1,024 files / 8 MiB; 1 MiB per file |
| All manifest files | 40 MiB combined | Count separately from input/skill data |

File entries sort by relative-path UTF-8 bytes. Paths are at most 1,024 UTF-8 bytes
and 16 components. Reject absolute paths, backslashes, empty components, `.` or
`..`, ASCII controls and DEL. Paths use NFC and the Unicode 15.0 assigned-character
repertoire, with at most 30 consecutive nonstarters in their canonical decomposition.
Reject duplicate paths, file/directory conflicts and NFC/full-case-fold collisions
at every directory prefix as well as the complete filename.

This fixed Unicode profile keeps validation identical across host and Python
versions; it constrains inventory paths, not document contents. The Python repertoire
and case-fold table are generated from the pinned Go text library by
`scripts/generate_path_unicode.go`; `make test` checks the generated bytes.

## Identities and validation boundaries

Descriptor `digest` hashes exact encoded file bytes; `object_digest` hashes the
parsed object using the shared `jcs-v1` recipe. `run_manifest_digest` identifies
the host RunManifest bytes without exposing its private contents. Initial binding
`engine_context_digest` and `prompt_digest` identify raw input bytes;
`engine_context_object_digest` separately identifies the canonical EngineContext.

Skill `bundle_digest` identifies the selected published bundle. `loader_digest`
pins its loading implementation. `loading_digest` is the canonical digest of the
SkillSetManifest object with only `loading_digest` omitted; the descriptor's
`object_digest` covers the complete object. The
[canonical identity contract](CANONICAL_IDENTITY_CONTRACT.md) implements these
computations and launch checks. Local skill signing/verification and pinned instruction-loader checks are
implemented. Executable release signing/publication remains outstanding.

The shared library exposes matching Go / Python entry points:

- `ValidateControl` / `validate_control`: one direction's control schema and
  field consistency, including budget ceilings and registered operations.
- `ValidateStartup` / `validate_startup`: a complete successful five-message
  transcript, including sequence, echoes and initial identity consistency.
- `ValidateInputTree`, `ValidateSkillManifest`, `ValidateSkillSet` / their
  snake-case equivalents: complete inventory structure, paths and declared bounds.
- `ValidateManifestSet` / `validate_manifest_set`: inventories plus exact raw
  per-skill manifest sizes/digests, selected IDs and aggregate bounds.
- `ValidateStartupInputs` / `validate_startup_inputs`: the transcript and manifest
  set, exact input-tree/skill-set raw descriptors, and context/prompt digest links.

These checks do not open actual input files, verify their contents or canonical
digests, install confinement, enforce live deadlines or establish host attestation.
Operator's [host staging layer](../docs/INPUT_STAGING.md) now materializes and
verifies physical input/skill/manifest trees; the shared APIs remain filesystem-independent.
The runtime must use bounded reads, reject links/undeclared files, verify actual
file sizes/hashes and passive skill content, enforce read-only mounts and gates,
and maintain live transport sequences. Guest echoes never replace those checks.
The input-content stage implements EngineContext and prompt provenance. The
canonical identity stage adds digest verification through its separate API;
the package integrity stage implements verification against a caller-approved pin.
Host-private RunManifest, all five native model codec families and package
build/check tooling are implemented. Production distribution/publication and
complete process/native qualification remain outstanding.

Both language runners consume the same 102 cases in
[startup-manifests.json](fixtures/startup-manifests.json), including oversized-frame
inventories delivered separately, identity mismatches, path collisions, Unicode
portability, empty/populated skill sets and declared data limits.
