# Engine release discovery and compatibility

Status: accepted MVP design, 2026-09-16; local identity resolution and HTTPS/cache
validation, embedded-file inspection and executable startup integration implemented.
Approved release publication and full runtime qualification remain outstanding.
Operator owns this host-only discovery contract for local Attack Harness images and their
release approval. The shared host/harness contract separately governs runtime
messages and data exchange.

The [host image preparation implementation](../docs/IMAGE_PREPARATION.md) provides
local pinning, strict compatibility checks, atomic caching and stopped-image file
inspection. Configuration and launcher integration are implemented. Approved image publication
and full production runtime qualification remain outstanding.

## 1. Local image identity

The administrator installs the Attack Harness image into the same local Docker installation
that Operator uses: Docker Engine on Linux or Docker Desktop on macOS. The required `engine.image` configuration selects a local Docker name/tag,
repository digest reference already present locally, or a full image ID.
An omitted tag means the local `latest` tag only. Missing configuration or missing
local content fails preparation/start with an instruction to install the image.

Resolve the selected native platform locally and record its full Docker image
`Id`, `sha256:` plus 64 lowercase hexadecimal characters. This is the image config
content identity, which binds the root filesystem's uncompressed layer identities.
It is **not** a registry manifest/index digest, short display ID or container ID.
For this Docker MVP, the release key and response `image_digest` mean this full
Docker image ID. Resolve it directly from the local Docker Engine.
Record repository/manifest/index digests separately when locally available; do not
substitute a `RepoDigests` entry for the release key. See Docker's
[content-addressable image description](https://docs.docker.com/reference/cli/docker/image/pull/)
and [image inspection](https://docs.docker.com/reference/cli/docker/image/inspect/).

Use the resolved full image ID for Docker container creation, with pull forbidden
(`--pull=never` or equivalent API behavior with no image-create/pull request).
Inspect the created container to confirm the same image ID before start. Tags
changing during startup cannot change the chosen bytes. If the pinned local image
is removed/unavailable, fail rather than substitute another image. A prepared run
whose local tag resolves differently at start must rebuild and revalidate all
image-dependent preparation before acceptance; an accepted start retry keeps its pin.

## 2. HTTPS discovery and response

Installed trust is the fixed HTTPS origin `https://releases.intrusive.ai`, ordinary
system CA trust and hostname validation. Image labels, campaign content and guest
requests cannot select an alternate origin, key, redirect or TLS policy. The release service exposes this metadata through an unauthenticated HTTPS GET.

For image ID `sha256:<hex>`, the host issues:

```http
GET /sha256/<hex> HTTP/1.1
Host: releases.intrusive.ai
Accept: application/json
```

The response must be HTTP 200, `application/json`, and one strict UTF-8 JSON object
with these required fields and no unknown fields:

```json
{
  "api_version": "intrusive.ai/engine-release/v1alpha1",
  "image_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "minimum_operator_version": "0.1.0",
  "contract_package_version": "0.1.0",
  "contract_package_digest": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
  "runtime_profile": "operator-container/v1",
  "platform": "linux/amd64"
}
```

Both digests require the full lowercase SHA-256 form. Versions use SemVer 2.0.0;
compare minimum Operator version by semantic precedence, not lexicographic text.
MVP published Operator versions and minimums must be stable versions; unqualified
prerelease hosts do not satisfy a production release requirement. Contract package
version **and** content digest must exactly match a locally installed supported
package; a higher minimum Operator version check is not a substitute. Platform
is the **image** platform: `linux/amd64` or `linux/arm64`. Match CPU architecture
to the physical host; do not compare the image OS to macOS. Record the Operator
host platform separately as `linux/amd64`, `linux/arm64`, `darwin/amd64` or
`darwin/arm64`. No emulation qualification is implied. `operator-container/v1`
is the common ABI family; the installed host profile selects Linux Docker/FIFO
or macOS Docker Desktop/spool and must pass its actual prerequisites. One Linux
image ID can be used on both host OSes of the same architecture when its release
and installed profile implement both transports. The response does not need to ship
the contract package or enumerate Docker flags. See [host profiles](../HOST_RUNTIME_PROFILES.md).
The confirmed supported OS/Docker versions are informational. Do not reject an
otherwise compatible installation for unlisted versions, patch age or lack of a
recorded version-combination test. Require actual runtime capabilities, the image
release approval, minimum Operator application version and exact shared contract.

The embedded engine manifest/package identities must agree with the response;
inspect required immutable release files without executing guest code. A release
response names installed policy, never supplies arbitrary Docker flags, mounts,
executables or security settings. The complete shared contract is not downloaded;
its version/digest identifies the copy already installed in Operator and the image.

Bound lookup to 10 seconds total and 64 KiB decoded response bytes; send `Accept-Encoding: identity` and reject non-identity content encoding; reject redirects, duplicate JSON keys, excessive depth (32), invalid
fields/types/versions and trailing content. HTTP 404 means unapproved/unknown image.
Other HTTP failures, TLS/network/timeout failures without a valid cached response,
identity mismatch or unmet compatibility requirements fail before guest execution
and campaign acceptance with a clear error/nonzero CLI result. There is no unapproved
fallback or acceptance based solely on a tag, label or HTTP 200 without valid data.

## 3. Cache, campaign pin and trust semantics

Store the exact successfully validated response bytes, their raw SHA-256, origin,
image ID and retrieval time in a service-owned cache keyed by origin plus image ID.
Only a validated HTTPS response populates it, using an atomic write. Check cache
integrity and re-evaluate compatibility against the currently installed Operator,
contract package, platform and runtime profile whenever selecting that image.
Malformed or corrupt cached entries are unusable and require a fresh valid lookup.

A valid cache entry may be reused while that image is installed/in use, including
new campaigns and offline starts. No per-start refresh or expiry is required by
this MVP. A different image ID needs its own response. Retain the accepted response
and image identity with campaign records even after runtime cache eviction, so
reports remain attributable. Do not change an accepted campaign's compatibility
record or image pin. Live target snapshot restores leave the existing harness alone.

Approval comes from control of the HTTPS release service.
This cache policy is not immediate revocation: removing a release from the site
does not invalidate an already cached approval. Administrators can evict cached
metadata or remove/disable the local image to prevent subsequent selection; active
campaign termination remains a separate host operation. Publish immutable metadata
for each image ID; changed engine requirements normally accompany a new image.

The Attack Harness release pipeline publishes one response per supported platform image ID,
after its image/package/runtime qualification gates pass. Archive image/config/
manifest identities, embedded metadata, build provenance, SBOM and test evidence
with the release. Verify upstream build inputs according to the release pipeline requirements.

## 5. Embedded image files

After HTTPS/cache approval, Operator MUST inspect the same pinned local image
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
- `platform`: the approved image's `linux/amd64` or `linux/arm64`.
- `entrypoint`: `['/usr/bin/python3', '-I', '-S', '-B', '/opt/operator/engine/bootstrap.py']` as a JSON array of strings.
- `transports`: `['fifo', 'spool']` in this order.
- `prompt`, `skill_loader`, `tool_catalog`: each a closed object containing
  `size_bytes` and `digest`, the actual file length and raw SHA-256 digest.

The host MUST reject missing/extra fields, duplicate JSON keys, identity mismatch,
changed file bytes, invalid prompt bytes or a loader containing invalid UTF-8/NUL.
The full approved image ID binds this manifest; the manifest MUST NOT contain its
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
