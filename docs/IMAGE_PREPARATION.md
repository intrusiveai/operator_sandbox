# Local image preparation and release approval

Status: implemented in `internal/dockercontrol/image.go` and
`internal/imagerelease`. This stage resolves local image identity and obtains a
validated compatibility response. It does not create/start a container or establish
runtime confinement. The accepted [release contract](../schemas/ENGINE_RELEASE_CONTRACT.md)
remains authoritative. These host-only records do not change the shared wire package.

## Preparation interface

The [host configuration loader](HOST_CONFIGURATION.md) supplies installation
settings to the future launcher/preparation command:

- The configured `engine.image` selector and selected local Unix Docker endpoint.
- The actual host platform and installed `operator-container/v1` policy identity.
- The running stable Operator version and verified installed contract package
  version/digest. These must come from the application/package installation,
  not a campaign bundle, image label or HTTPS response.
- An existing private, administrator-owned release-cache directory.

`imagerelease.Open` opens that cache and creates a fixed-origin HTTPS client.
`Client.Prepare` resolves the image through `dockercontrol.Client.ResolveImage`
before checking approval. Its `Prepared` result retains both the image pin and an
immutable copy of the approved response bytes. A cached approval alone cannot
satisfy a missing local image. Close the client after preparation calls finish.

The [installed contract loader](INSTALLED_CONTRACT.md) now supplies filesystem
verification and a pinned protocol. Distribution-supported package/version selection
and exposing preparation through the campaign CLI remain integration work. No caller
can configure a different release URL or TLS policy through this API.

## Read-only Docker resolution

`ResolveImage` uses the explicit local endpoint with the existing bounded Docker
subprocess wrapper. It reads the daemon ID, inspects the requested image with an
explicit native `--platform`, then rereads the daemon ID. Daemon changes, missing
content, malformed responses and wrong-platform images fail preparation.

The image platform is `linux/amd64` for either supported x86_64 host OS, and
`linux/arm64` for either supported ARM64 host OS. Platform-specific image inspection
is a required Docker capability; unsupported clients/daemons fail rather than
selecting an arbitrary variant. This is a capability check, not an OS/Docker version
allowlist. See [Docker image inspection](https://docs.docker.com/reference/cli/docker/image/inspect/).

The pin stores configured selector, endpoint, daemon ID, full `sha256:` image/config
ID, host/image platforms and available repository digests. Repository digests are
attribution, never release keys or replacements for the config ID. Local inspection
does not pull, start or otherwise execute the image. An untagged name retains
Docker's local `latest` selection behavior. Image inspection has a five-second
budget, shortened by the caller's deadline; it uses bounded output.

Before accepting prepared inputs, `VerifySelection` resolves their original selector
again. A different image or daemon requires re-preparation and revalidation.
Once a campaign is accepted, `VerifyImage` checks availability by its immutable ID
and saved daemon. It does not follow subsequent tag changes. The future launcher
must create by that ID with pulling forbidden and inspect the created container's
image before start.

## HTTPS approval and compatibility

The only lookup is `GET https://releases.intrusive.ai/sha256/<image-ID-hex>`, with
`Accept: application/json` and `Accept-Encoding: identity`. The client uses system
CA trust and hostname validation, rejects redirects, and requires HTTP 200 JSON
with no non-identity content encoding. HTTP 404 produces an explicit unapproved-image
error. Response headers are bounded to 16 KiB and decoded response bytes to 64 KiB;
the lookup has a ten-second deadline, shortened by the caller. Invalid content,
network/TLS/timeout errors and unmet requirements fail preparation.

The record parser enforces the seven required fields in the release contract,
unknown/duplicate-key rejection, strict UTF-8 JSON, no trailing content, depth 32,
full lowercase SHA-256 digests and supported Linux image platforms. Versions are
bounded to 128 characters and use [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html).
Operator/minimum versions must be stable. Numeric precedence handles `1.10.0` versus
`1.9.0` correctly and ignores build metadata; numeric components cannot overflow.
The contract version and digest must match exactly, including version spelling.

`Approval.Bytes()` returns an independent copy of the exact HTTPS response, including
whitespace; `Digest()` hashes those exact bytes. `Record()`, `Source()` (`network` or
`cache`) and `RetrievedAt()` expose validated metadata and retrieval provenance.
Campaign persistence must retain these bytes/digest and the image pin independently
of subsequent cache eviction. Metadata approval alone is not launch admission.

## Private cache

Each filename combines the SHA-256 of the fixed origin and the image/config ID.
The cache's host-only `operator.dev/image-release-cache/v1alpha1` entry contains:

- Fixed origin and full image ID.
- Original UTC retrieval time.
- Exact response bytes as base64 and their raw SHA-256 digest.

Entries are bounded to 128 KiB including base64 overhead. Readers require private
regular files, reject links and special files, bound actual reads and validate
identity, bytes, digest, timestamp and response structure. Writes use private
exclusive temporary files, file sync, atomic rename and directory sync. Concurrent
preparations cannot expose a partially written record. Cache persistence failure
fails preparation; no valid approval object is returned from that failed call.

Every hit rechecks current Operator/contract/platform/profile compatibility. A valid
but incompatible record fails without attempting to replace it. Missing or corrupt
entries require a fresh validated lookup; no network failure can make corrupt bytes
usable. A fresh valid lookup can atomically replace a corrupt cache entry without
following its old link target. Cache directories themselves must be private and
are not repaired by this component.

There is no automatic expiry or refresh. A valid compatible entry supports later
offline preparation while Docker still has the image. Different image IDs have
different entries. Publisher metadata is immutable by image ID; administrative
cache eviction follows the release contract. Cache publication is independent of
campaign acceptance: a late successful write cannot make a canceled preparation
an accepted campaign. Local filesystem calls still depend on a responsive host
filesystem; context checks reject late completion but do not forcibly interrupt
a stalled filesystem syscall.

## Validation and remaining gates

Tests cover all four host platforms, exact ID versus repository digest, missing
images, daemon drift, tag changes across acceptance, strict records, semantic version
boundaries, exact package pins, fixed URL/headers, redirects, malformed/oversized
responses, canceled lookups, cache corruption, link handling, failed publication,
concurrent writes and compatible offline reuse. Docker and HTTPS are controlled
test doubles; they do not qualify a production image, Docker host or release service.

Before launch, implement immutable engine-file inspection without guest execution,
verify the embedded manifest/package against this approval, prepare/freeze campaign
inputs through the [host staging layer](INPUT_STAGING.md), enforce fixed Docker
policy and complete confinement/startup checks. The
image build, live Python confinement and full host/runtime qualification remain
separate work. A release record identifies installed runtime policy; it supplies
no executable code, Docker flags, filesystem mounts or downloaded shared contract.
