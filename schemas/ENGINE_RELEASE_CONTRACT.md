# Engine release discovery and compatibility

Status: accepted MVP design, 2026-09-16; implementation and qualification pending.
Operator owns this host-only discovery contract for local Attack Harness images and their
release approval. The shared host/harness contract separately governs runtime
messages and data exchange.

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
