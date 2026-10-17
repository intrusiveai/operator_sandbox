# Local image preparation and compatibility

`internal/dockercontrol` resolves exact local image identity;
`internal/imagerelease` validates the compatibility record and embedded resources.
The installed preparation path uses both before Interceptor attachment or Harness
execution. The administrator supplies `engine.image` and installs the image into
the configured Docker daemon.

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
attribution, never replacements for the config ID. Local inspection
does not pull, start or otherwise execute the image. An untagged name retains
Docker's local `latest` selection behavior. Image inspection has a five-second
budget, shortened by the caller's deadline; it uses bounded output.

Before accepting prepared inputs, `VerifySelection` resolves their original selector
again. A different image or daemon requires re-preparation and revalidation.
Once a campaign is accepted, `VerifyImage` checks availability by its immutable ID
and saved daemon. It does not follow subsequent tag changes. The launcher
MUST create by that ID with pulling forbidden and inspect the created container's
image before start.

## Compatibility and embedded resources

`imagerelease.Prepare` MUST resolve the image and inspect its fixed files through
`ReadReleaseFiles`. The stopped inspection container MUST remain networkless,
nonroot, read-only and unstarted. Reads and cleanup MUST use its full ID and pinned
local daemon; no image-supplied executable is run. The result MUST be rejected if
cleanup cannot be confirmed.

The image MUST include `/opt/operator/engine/share/compatibility.json`, following
[the local compatibility contract](../schemas/ENGINE_RELEASE_CONTRACT.md). Its
minimum Operator version, exact contract package version/digest, runtime profile,
and native Linux image platform MUST match the installed host. The compatibility
record MUST omit its own image digest. The image pin binds it independently.
The manifest, prompt, loader and complete native tool catalog MUST still satisfy
the existing embedded metadata checks. No alternate startup command, mount or
security policy may come from the image metadata.

`Compatibility.Bytes()` returns a copy of the exact inspected compatibility file;
`Digest()` is its raw SHA-256. `Compatible(imageID, requirements)` also checks the
independent image binding. `Embedded.Matches` MUST prevent mixing inspected
resources with another image, daemon, platform or compatibility record.

The host MUST retain these bytes/digest with campaign records. The existing
`release_record_digest` wire name now identifies the embedded compatibility bytes;
`generator.release_digest` MUST match that frozen startup value. The image ID
remains separate. Operator requires no release endpoint or approval cache to
prepare an otherwise compatible local image. Legacy `cache.release_directory`
configuration is accepted but unused and can be removed.

## Validation

Tests cover strict metadata, minimum-version and exact contract/platform/runtime
checks, image and daemon pinning, metadata consistency, resource digests, stopped
inspection cleanup, missing/oversized files, and fail-closed admission. CLI tests
exercise local preparation followed by independent retained-record inspection.
These controlled tests do not qualify a production image or native Docker host.
See [the Attack Harness handoff](ATTACK_HARNESS_COMPATIBILITY_HANDOFF.md) for the
matching image-build requirements and package pin.
