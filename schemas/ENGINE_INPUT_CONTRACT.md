# Immutable EngineContext and prompt content

This document defines the implemented launch-input schemas and Go/Python validation.
It supplements [startup/manifests](STARTUP_MANIFEST_CONTRACT.md) and the
[shared contract](SHARED_CONTRACT.md). Runtime preparation, provider codecs,
contract package publication remain separate work. Canonical verification is
implemented by the additional [identity validation layer](CANONICAL_IDENTITY_CONTRACT.md).

## EngineContext

[engine-context.schema.json](engine-context.schema.json) is the closed schema for
`/run/operator/input/run-context.json`, with API version
`operator.dev/engine-context/v1alpha1`. The complete original ScenarioBundle remains
at `/run/operator/input/scenario-bundle.json`. Context fields are:

| Field | Meaning |
|---|---|
| `campaign_id`, `launch_id`, `run_revision` | Initial launch identity; must match startup. |
| `attempt_index_high_watermark` | Host-ledger campaign allocator seed, 0 through 2^53−1. |
| `contract`, `release` | Exact package/catalog/registry and image/release pins echoed in bootstrap. |
| `scenario_bundle` | Complete input-manifest entry for the separate ScenarioBundle file. |
| `references` | Complete manifest entries for included reference files, sorted by relative path. |
| `artifact_bindings` | Sorted `{artifact_id, entry_id}` mappings from bundle references to staged files. |
| `omissions` | Sorted `{artifact_id, reason}` records for permitted absent references. |
| `prompt` | The prompt manifest `entry_id` plus inline `provenance`, defined below. |
| `skills` | Exact complete SkillSetManifest, including the empty-set case. |
| `target` | Existing public TargetCapabilityManifest, including bounded delivery descriptions. |
| `feedback` | Effective `profile` and `allowed_kinds`; never a native session-profile override. |
| `model` | Safe selected codec/profile identity, model identifier and permitted features. |
| `operations` | Sorted installed operation names advertised at admission. |
| `limits`, `remaining_limits` | Resolved campaign/harness ceilings and initial remaining allowances. |

The complete [example](fixtures/engine-context-example.json) uses fixture model
identities; it does not advertise a qualified provider route.

Context contains neither its own digest nor future input-tree/RunManifest digests.
It excludes native session and worker IDs, host routes, credentials and policy
objects. Input paths come from the existing fixed root and inventory rules; no
additional source-path or URL field is introduced. Public capability delivery
selectors remain data for the reviewed adapter. They do not grant network access.

`model` contains `codec_id`, `profile_id`, `profile_digest`, `model_id` and
`features`. The first two are bounded symbolic IDs; the digest pins the installed
safe profile. `model_id` is a bounded non-whitespace string supporting native names
such as names with slashes. MVP features are exactly `text`, `function-tools`, in
that order. This projection contains no provider endpoint or credential and does
not implement a provider request schema. The runtime must resolve these identities
against its installed codec registry, selected host route and release compatibility
record before admission. Unknown/unqualified profiles cannot be made executable
by putting them in a structurally valid context.

Feedback kinds use the order `target_output`, `operation_error`,
`injection_delivery`, `oracle_outcome`. The allowed list may be empty; otherwise it
must be an ordered subset permitted by the effective profile. Launch validation
rejects a profile broader than the bundle requests. The host separately computes
the effective profile from the native profile and policy, following the
[feedback contract](FEEDBACK_CONTRACT.md).

Target capability groups have sorted unique references. Operation references must
match their operation IDs. The existing `missing-input-contract` status remains
valid descriptive metadata; it does not authorize executing that operation. Host
preparation must establish required delivery contracts and capability compatibility.
The input-content validator does not replace the source/projection digest and
dependency checks in the [capability contract](CAPABILITY_EXPORT_CONTRACT.md).

## Reference completeness and limits

Each included bundle artifact maps to exactly one manifest entry; several artifact
IDs may share the same immutable bytes. Every reference entry must be used. Reject
conflicting size, digest, media type or schema claims. Included references cannot
also have omission reasons. Each absent artifact must be optional and have the
same explicit omission reason in the frozen bundle and context. Artifacts used by
required objectives or scenarios must be included. There are no unlisted mappings
or omissions, and IDs cannot repeat within either list or appear in both lists.

Manifest entry IDs are distinct across the bundle, prompt and included references.
The context has at most 4,000 reference entries, bindings and omissions per list,
matching the bundle's artifact ceiling. The existing inventory limits still apply:
1 MiB per reference, 4 MiB ScenarioBundle, 128 KiB effective prompt and 64 MiB total
input bytes including the encoded EngineContext. Selected skills are accounted
separately. Context parsing is bounded at 64 MiB, with the complete declared input
sum checked against the same aggregate ceiling. Callers must bound reads before
passing bytes to the validators.

The immutable remaining allowances describe input preparation time. Admission may
narrow them as startup consumes campaign time or resources; it must not increase
them. Campaign/harness ceilings and advertised operations must match exactly.
Healthy restores update the active revision through the correlated restore result;
they do not rewrite this context, reset its allocator seed or reload the prompt.

## Prompt provenance and composition

[prompt-provenance.schema.json](prompt-provenance.schema.json) defines the inline
`prompt.provenance` object, API version `operator.dev/prompt-provenance/v1alpha1`:

- `mode`: `default`, `replacement` or `extension`.
- `base`: raw `size_bytes`/`digest` of the immutable release default, in every mode.
- `replacement`: raw descriptor present only in replacement mode.
- `appends`: ordered raw descriptors, empty outside extension mode; 1–16 in extension.
- `effective`: raw size/digest of the exact staged effective prompt.

The enclosing context's release pins identify the source release. The host must
verify the base descriptor against that release's embedded default asset. Source
paths and prompt bodies are not part of public provenance. Host preparation may
retain private source diagnostics separately without exposing them to the guest.

`ComposePrompt` / `compose_prompt` consumes frozen byte buffers. Default copies the
base, replacement uses only the replacement, and extension joins the base and
ordered appends with exactly two LF bytes between each input. Existing leading
or trailing newlines, CRLF, Unicode spelling and BOM bytes are preserved. There is
no template expansion, hidden prefix or source-file reread.

Every source must be valid UTF-8, contain no NUL, and contain at least one character
outside Unicode's fixed White_Space set. Empty/whitespace-only sources fail, including
empty replacements and append files. Every source and the composed effective prompt
must fit 131,072 bytes; the separators count toward the effective ceiling. Replacement
and extension cannot be combined. Returned provenance is generated from those exact
bytes. The guest verifies and uses the effective prompt without recomposing it.

The standalone provenance validator checks the closed shape, mode, default/replacement
descriptor equality and extension size arithmetic. Without source bytes it cannot
verify an extension's effective digest; composition with the original frozen buffers
establishes that relationship. Full launch validation verifies the effective staged
bytes against both provenance and InputTreeManifest. All model turns use these same
effective bytes with the separate fixed tool catalog.

## Library entry points and verification boundary

| Go | Python | Scope |
|---|---|---|
| `ValidatePrompt` | `inputs.validate_prompt` | Exact UTF-8 text and byte ceiling. |
| `ComposePrompt` | `inputs.compose_prompt` | Frozen source composition and returned provenance. |
| `Protocol.ValidatePromptProvenance` | `Protocol.validate_prompt_provenance` | Standalone provenance structure and consistency. |
| `Protocol.ValidateEngineContext` | `Protocol.validate_engine_context` | Closed context and inventory, profile, ID and budget consistency. |
| `Protocol.ValidateLaunchContent` | `Protocol.validate_launch_content` | Startup plus manifests and supplied context/bundle/prompt bytes. |

Launch validation first runs the existing startup/manifest checks. It then verifies
actual context, bundle and prompt sizes/raw SHA-256 digests against the inventory,
checks context startup bindings, exact skill metadata, prompt identity, reference
completeness, feedback narrowing and admission budgets. The original bundle bytes
are validated against ScenarioBundle without rewriting or canonicalizing them.

These functions accept bytes supplied by the caller; they do not open files or
decide which local source is trusted. They do not verify actual reference/skill
contents, canonical object/projection/package digests, signatures, source-bound
capability admission or actual model support. Immutable staging, bounded no-follow
reads, provenance verification against release assets and all live host gates remain
required runtime work. Structural validity alone does not admit a campaign.
Use `ValidateLaunchIdentities` / `validate_launch_identities` to add canonical
object/projection and installed catalog/registry checks to the content API;
package authenticity and source-bound capability admission remain separate gates.

The shared [input fixtures](fixtures/engine-inputs.json) cover 90 cases in both
languages, including source-byte preservation, launch mismatches, references and
omissions, profile narrowing and budget consistency. Additional tests cover exact
prompt byte boundaries, including multibyte UTF-8.
