# Attack Harness implementation handoff: local compatibility

## Objective and Operator counterpart

Operator now admits an administrator-selected local image using compatibility
metadata inside that image. It does not verify the publisher or call a releases
service for admission. Implement the matching Attack Harness image/build changes
against Operator branch `feat/local-harness-compatibility` (target:
`candidate-v0.1.0`). Use that branch's committed `release/contract-lock.json` as the
source of truth for the new shared package version, digest and source revision.

This decision supersedes the releases skill's external `/sha256/<image-ID>`
approval prerequisite for Operator startup. Ordinary image and release-catalog
publication remains a separate distribution workflow. Do not change Operator's
image pinning, installed contract checks, network isolation or confinement policy.

## Exact shared package pin

- Source commit: `cb43a919bda9b8689c11e20b7a09db35d2a98f89`.
- Source tree: `1fcded221a5fa02d2cb60f493e57dcd8150adcc1`.
- Package version: `0.0.4`.
- Package digest: `sha256:750deac00ebece408dd7514ceae75e090f458f07ed1988b26859f7332ade32be`.

Build the contract from this immutable Operator source commit. The branch's
`release/contract-lock.json` also contains the catalog/operation digests and
inventory counts required by the Harness lock. The source commit predates the
follow-up lock-file commit so the lock does not refer to its own commit ID.

## 1. Build and validate the embedded compatibility file

Update `build/prepare_release.py` to generate and verify this regular immutable
file in the release tree before the final Docker image is built:

`/opt/operator/engine/share/compatibility.json`

It MUST contain exactly:

```json
{
  "api_version": "operator.dev/harness-compatibility/v1alpha1",
  "minimum_operator_version": "0.1.0",
  "contract_package_version": "0.0.4",
  "contract_package_digest": "sha256:750deac00ebece408dd7514ceae75e090f458f07ed1988b26859f7332ade32be",
  "runtime_profile": "operator-container/v1",
  "platform": "linux/amd64"
}
```

Use `linux/arm64` for the ARM64 variant. Derive the package fields from the actual
verified package installed in that image. Emit deterministic UTF-8 JSON, at most
64 KiB, with no duplicate or extra fields. `minimum_operator_version` must be a
stable SemVer; Operator compares by precedence. The record MUST NOT contain an
image/config/manifest/index digest or its own digest. Those would introduce a
self-reference. The final Docker image ID independently binds the file bytes.

Keep the existing `engine-manifest.json` format and the resource digest checks for
prompt, skill loader and tool catalog. Its contract, platform and runtime profile
must agree with the compatibility file. The existing Dockerfile copies `/release`
into the final image; ensure the new file is included with immutable regular-file
permissions and is readable by the nonroot runtime user.

## 2. Install the matching shared contract

The normative package documents changed, so the package is now `0.0.4`; this is a
coordinated package update, not a change to runtime message shapes. Copy Operator's
locked `source` and `package` values into `build/contract-lock.json`, preserving
Attack Harness's `operator.dev/harness-contract-lock/v1alpha1` wrapper. Update the
publisher command to `--package-version 0.0.4`. Rebuild the package from the exact
source revision and verify the complete receipt against the lock. Do not retain
the old `0.0.3` digest or invent a new identity in the Harness checkout.

Use the published Go/Python validators and regenerate the image's tool catalog
from that same package. Update acceptance evidence and build reports to identify
the actual matching package and source commits. Existing released images need a
rebuild; images lacking `compatibility.json` will fail the new Operator admission.

## 3. Preserve runtime digest semantics

Keep the existing wire field names. Their new provenance is:

- `image_digest`: the independently resolved full Docker image/config ID.
- `release_record_digest`: raw SHA-256 of the exact embedded compatibility file,
  including its whitespace and terminal newline.
- `generator.release_digest`: the frozen
  `EngineContext.release.release_record_digest` supplied by Operator.

The Harness MUST continue echoing the frozen startup/conclusion bindings and
using the frozen context value in generated attempts. Do not substitute the image
ID, hash reserialized JSON, or retrieve a record from the releases service.
Operator retains the exact file bytes as campaign evidence. Retained older
campaigns keep their original identities; do not rewrite historical records.

## 4. Build inspection, release automation and documentation

Update `build/inspect_stopped_image.py`, `build/inspect_oci.py`,
`build/operator_stopped_image_test.go`, and `tests/test_release_build.py` as needed:

- Verify the actual embedded file and consistency with each platform's image and
  shared package. Build reports should record its raw digest separately from the
  final image/config, platform-manifest and index digests.
- Replace post-build external `compatibility_candidate` generation with facts
  derived from the file already built into the image.
- Remove external compatibility approval publication as a build/test/admission
  prerequisite, including the dedicated approval workflow where applicable.
  Preserve normal image builds, GHCR publication and release catalog publication.
- Update the Harness specs, release guide and acceptance matrix to match the
  embedded compatibility contract. No live releases-service data needs deletion
  or alteration to make Operator use this new path.

The canonical record and stopped-container inspection rules are in Operator's
[`schemas/ENGINE_RELEASE_CONTRACT.md`](../schemas/ENGINE_RELEASE_CONTRACT.md).
Operator reads five fixed files, creates only a stopped inspection container,
executes no Harness code during inspection, and requires confirmed cleanup.

## 5. Verification and completion criteria

Add deterministic tests for both image architectures, byte-for-byte reproducible
metadata, package/runtime/platform agreement, missing/malformed/oversized records,
and incompatibility with Operator. Confirm resource-digest and tool-catalog
checks still fail closed. Run the matching Go/Python contract fixtures.

Use the updated Operator's actual stopped-image reader against each built image.
With no approval cache and release-service access unavailable, demonstrate that a
compatible local image passes image preparation; missing or incompatible metadata
must fail before Interceptor attachment or Harness execution. Verify the retained
compatibility bytes/digest, attempt generator binding and conclusion binding in a
representative Operator–Harness campaign. Use local fixtures; no paid calls or
cloud writes are necessary for deterministic validation. Record native host/image
qualification separately from fixture results.
