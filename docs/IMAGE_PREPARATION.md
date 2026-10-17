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

The pin MUST store the configured selector, endpoint, daemon ID, native platform,
platform-specific Docker `Id`, repository inventory, and verified immutable lookup
reference separately. Docker's selected `Id` can be a config digest on classic
storage or a platform manifest digest on containerd storage. It is the identity
compared throughout preparation; it is not necessarily a usable Docker selector.

Resolution MUST first inspect the configured selector with `--platform`. It MUST
then verify a usable immutable reference against that same selected identity and
platform. Classic stores normally accept the bare ID. If Docker reports that ID
absent, Operator MAY try repository-qualified digests from the inspected inventory
whose normalized repository matches the configured selector. Candidates MUST be
sorted, deduplicated and independently inspected; an unrelated first inventory
entry MUST NOT be selected. An explicitly configured digest reference is checked
first. A mismatch, timeout or non-absence failure MUST fail preparation. There is
no tag fallback, pull or registry request. The complete resolution has a
five-second budget, shortened by the caller's deadline.

Before accepting prepared inputs, `VerifySelection` MUST inspect the original
selector again and reject changed image or daemon identity. `VerifyImage`, volume
inspection, stopped-file inspection and launch MUST use only the frozen immutable
reference plus explicit platform. Container creation MUST retain `--pull=never`.
An image pin without `reference` is a legacy bare-ID pin; failed lookup MUST NOT
silently upgrade it or select a different reference.

New container bindings use `operator.dev/docker-binding/v1alpha2`: compare the
container's selected `ImageManifestDescriptor.digest` when present, otherwise the
classic store's `.Image`. An index-backed container's `.Image` can be the index
rather than the selected manifest. Neither accepting an arbitrary inventory digest
nor weakening equality to a substring/prefix is allowed. Full container ID, saved
daemon and labels remain mandatory. Version 1 bindings retain their original
literal `.Image` comparison. Recovery uses the recorded binding version and never
creates, starts, or resumes execution. The start intent records binding version,
lookup reference, platform, and selected image identity before create.

These host selection rules correct the bare-ID-only instructions in the pinned
shared package's `ENGINE_RELEASE_CONTRACT.md` §1. The compatibility file and all
host/Harness wire fields are unchanged; the published `0.0.4` package remains
byte-for-byte unchanged. No Harness rebuild is required solely for this fix.

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

## Troubleshooting and native reproduction

Preparation failures identify the Docker operation and allowlisted reason, such as
`immutable image reference inspection: image reference is not available in the local Docker store (exit 1)`
or `selected image inspection: Docker command timed out`. Raw stderr, command
arguments, endpoints and credentials MUST NOT be copied into diagnostics. The same
bounded message is retained in the start completion and exposed by `inspect --run`
and `campaign status --run`. Cleanup failures MUST NOT replace the original error.

A successful tag lookup alone does not establish that its returned bare ID works:

```sh
docker image inspect --platform linux/amd64 IMAGE_TAG
docker image inspect --platform linux/amd64 RETURNED_ID
docker image inspect --platform linux/amd64 REPOSITORY_AT_DIGEST
```

Use the platform installed on the test host. These are read-only local operations.
For a repeatable production-reader test without registry access or campaign replay:

```sh
OPERATOR_NATIVE_DOCKER_ENDPOINT=unix:///var/run/docker.sock \
  go test ./internal/imagerelease -run '^TestNativeLocalImageCompatibility$' -v -count=1
```

On macOS, set the explicitly selected Docker Desktop Unix endpoint instead. This
opt-in test builds a scratch fixture with valid embedded metadata and resources,
uses the real stopped-container reader and compatibility validator, checks exact
cleanup, and removes its fixture image. It runs no Harness code or campaign.
See [native selection evidence](IMAGE_SELECTION_VALIDATION.md) for tested scope.
