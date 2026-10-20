# Local Harness compatibility contract

This host-only contract defines admission of an administrator-selected local Attack
Harness image. The shared host/harness wire fields remain unchanged.

## 1. Local image identity

The administrator MUST install the image into Operator's configured local Docker
installation. `engine.image` selects a local name/tag, repository reference or
full Docker image ID. An untagged name selects the local `latest` tag. Missing
content fails startup; Operator never pulls or contacts a registry.

Resolve with the native platform explicitly selected. Pin Docker's selected `Id`
(`sha256:` plus 64 lowercase hex characters) separately from the immutable lookup
reference and repository inventory. Classic stores can return a config ID;
containerd stores can return a platform-manifest ID that is not a usable bare
selector. Operator MUST independently verify an immutable lookup reference
against the same selected ID, platform and local daemon. A repository-qualified
digest candidate must match the configured repository; an unrelated first
RepoDigests entry or a mutable-tag fallback is not acceptable.

Subsequent inspection and creation MUST use that frozen reference and explicit
platform with `--pull=never`. Before acceptance, recheck the original selector;
a changed selector requires fresh preparation. After acceptance, tag changes MUST
NOT redirect execution. Legacy pins without a reference retain bare-ID semantics;
failed lookup must not silently reinterpret them.

Container bindings verify the full container ID, saved daemon and launch labels.
For `operator.dev/docker-binding/v1alpha2`, compare the selected
`ImageManifestDescriptor.digest` when present, otherwise `.Image`. Legacy v1
bindings retain their literal `.Image` check. Preserve the saved binding version
through cleanup and recovery; recovery never creates or resumes execution.
Target snapshot restores retain the running Harness and its image/compatibility
pins. See [image selection](../docs/IMAGE_PREPARATION.md) for resolution bounds.

## 2. Embedded compatibility record

The image MUST contain `/opt/operator/engine/share/compatibility.json` as one
regular immutable file. Operator MUST read it from the exact pinned image using
the stopped-container inspection below, without executing image code.
It MUST be strict UTF-8 JSON, at most 64 KiB and depth 32, with exactly these fields:

```json
{
  "api_version": "operator.dev/harness-compatibility/v1alpha1",
  "minimum_operator_version": "0.1.0",
  "contract_package_version": "0.2.1",
  "contract_package_digest": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
  "runtime_profile": "operator-container/v1",
  "platform": "linux/amd64"
}
```

The example digest is illustrative; builds MUST use the actual package identity
from the [release contract lock](../release/contract-lock.json). Required fields, duplicate/unknown keys, invalid types/UTF-8, trailing
content, unsupported versions and oversized content MUST be checked. The file
MUST NOT contain its image digest: adding it would change that digest. The Docker
image identity independently binds the file's bytes and the other image contents.

Operator MUST enforce:

- Stable SemVer minimum Operator version, compared by semantic precedence.
- Exact installed shared-contract package version and full lowercase SHA-256 digest.
- `operator-container/v1` runtime profile and the supported native image platform.
- `linux/amd64` for Linux/macOS x86_64 hosts, or `linux/arm64` for Linux/macOS ARM64
  hosts, with no emulation implied. Confirmed OS/Docker versions are informational;
  actual launcher/transport capabilities remain mandatory.
- Agreement with the embedded engine manifest's contract, runtime and platform.

The image MUST NOT supply Docker flags, mount selections, executable overrides,
policy settings, a contract download location, or a replacement trust source.
Operator MUST perform compatibility admission entirely from installed image and
contract bytes, without requiring publisher identity verification or a release
service/cache. Missing or incompatible metadata MUST fail before native attachment,
guest execution or campaign acceptance.

## 3. Campaign provenance

Operator MUST retain the exact compatibility file bytes and their raw SHA-256
alongside the independently resolved image pin. JSON whitespace is significant to
this raw digest; do not hash a reserialized object. Existing wire fields
`release_record_digest`, `EngineContext.release.release_record_digest`, and
`generator.release_digest` MUST identify these exact file bytes. The separate
`image_digest` continues to identify Docker's image/config ID. The Harness MUST
use the frozen startup binding for generated attempts and conclusions.

An accepted campaign MUST keep these bytes and identities unchanged. Retained
historical campaigns keep their original records/digests; reporting MUST NOT
rewrite or re-admit them. Image publishers SHOULD archive image identities,
compatibility bytes, build provenance, SBOMs and test evidence with each release.

## 5. Embedded image files

Operator MUST inspect the selected pinned local image
without starting guest code. It MUST create a stopped, nonroot, networkless,
read-only inspection container with capabilities dropped, no-new-privileges,
restart disabled, a private empty Docker CLI configuration and pull forbidden.
Images declaring volumes MUST be rejected. All subsequent reads and removal MUST
use the full returned container ID and saved local daemon binding. Operator MUST
verify the image ID, inspection label and never-started state before and after
reading. It MUST remove that exact stopped container before accepting the files.
An uncertain create/removal MUST fail preparation; a known full ID MUST remain
available for administrative cleanup. Inspection MUST NOT start, execute in, or
force-remove a container.

The required fixed paths are:

| Path | Content |
| --- | --- |
| `/opt/operator/engine/share/compatibility.json` | Closed compatibility record below, at most 64 KiB. |
| `/opt/operator/engine/share/engine-manifest.json` | Closed manifest below, at most 64 KiB. |
| `/opt/operator/engine/share/default-system-prompt.txt` | Exact default prompt satisfying the shared prompt contract. |
| `/opt/operator/engine/lib/attack_harness/skill_loader.py` | Fixed loader implementation; its raw digest binds SkillSetManifest. |
| `/opt/operator/engine/share/tool-catalog.json` | Shared package's complete native tool projections. |

Docker archive output MUST be bounded to 1 MiB plus 64 KiB of framing per file.
The host MUST decode it in memory and accept exactly one regular file bearing the
expected basename, at most 1 MiB. It MUST reject links, special files, sparse
representations, extended attributes, set-ID/sticky bits, alternate paths,
additional entries and nonzero trailing data. No archive path may be extracted
onto the host filesystem. The whole inspection MUST have a 60-second deadline;
cleanup MUST have its own five-second deadline after cancellation/failure.

`engine-manifest.json` MUST contain exactly:

- `api_version`: `operator.dev/engine-manifest/v1alpha1`.
- `contract`: exact installed `{version, digest}` package identity.
- `runtime_profile`: `operator-container/v1`.
- `platform`: the pinned image's `linux/amd64` or `linux/arm64`.
- `entrypoint`: `['/usr/bin/python3', '-I', '-S', '-B', '/opt/operator/engine/bootstrap.py']` as a JSON array of strings.
- `transports`: `['fifo', 'spool']` in this order.
- `prompt`, `skill_loader`, `tool_catalog`: each a closed object containing
  `size_bytes` and `digest`, the actual file length and raw SHA-256 digest.

The host MUST reject missing/extra fields, duplicate JSON keys, identity mismatch,
changed file bytes, invalid prompt bytes or a loader containing invalid UTF-8/NUL.
The full pinned image ID binds this manifest; the manifest MUST NOT contain its
own image ID.

`tool-catalog.json` MUST contain exactly `api_version`, set to
`operator.dev/model-tool-catalog/v1alpha1`, and `codecs`. The latter MUST map all five
supported codec IDs to the native tool arrays defined by
[MODEL_TOOL_CATALOG.md](MODEL_TOOL_CATALOG.md), generated with the complete installed
operation registry. Operator MUST compare each canonical array with its own
verified package projection. Campaign startup MUST then derive the narrower
projection from the actual admitted operation set. Image files MUST NOT alter
host commands, mounts, tool handlers, policy settings or credential selection.

Tests MUST cover tampered bytes/metadata, catalog divergence, daemon/image drift,
archive rejection, incomplete create replies, independently cancelled cleanup and
removal failure. Passing these host tests does not replace native Docker image
qualification.
