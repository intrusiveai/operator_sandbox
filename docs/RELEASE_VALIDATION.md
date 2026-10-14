# Installation and release-tooling validation

This document retains dated validation evidence. Earlier entries describe the
implementation tested at that time; current requirements are in
[host distribution](HOST_DISTRIBUTION.md). It does not grant production image approval or native runtime qualification.

## Tested identities

- Operator source: `2b00d4e38f3bccb73546acf8e6e0476e06045297`.
- Host candidate version: `0.1.0`; native update test candidate: `0.2.0`.
- Shared contract: development `0.0.0`,
  `sha256:be32c73554eab728df22bd51940b20ce670bd8442a827f3d6bfb2fd34f106c61`.
- Build/test host: macOS ARM64; Go 1.26.5, Python 3.14.6 and GnuPG 2.4.7.
- Attack Harness's existing `build/contract-lock.json` matches this contract pin.
  Actual refreshed image build reports were not produced in this stage.

| Platform | Executable digest | Signed-manifest payload digest |
|---|---|---|
| darwin/amd64 | `sha256:66a9c0d18e15a1851e73f2c7924470a9ca177eeb9822fab2256855b5c43444b7` | `sha256:66c4572475782bbb474f8159cfeae26ab328cd5fac6c49b1f622feb0440ee0f5` |
| darwin/arm64 | `sha256:4a046874a18c2027622586994e8bde76717d61a9558a17abf7304b624863cad6` | `sha256:568b8a09630725ac953e8cc2e04845e125ec1c146fe9fb859b4219e65f8a4c64` |
| linux/amd64 | `sha256:d7ef4ddf1203ad7c83cc03039e09b07131d1360c1c662252a9fca1acb45c996c` | `sha256:91c19b6a896bf4ee66ef6611163372d343cf9c70df8522dc6b57467d19425d17` |
| linux/arm64 | `sha256:e4f977a33d2b4298fa60696a913ac95217f048b6657b61c542770ace8dd92e20` | `sha256:d39ad3a941056d72ae7815c04f1a370703bfba355792431c2d77ab2b6677960e` |

Two independent invocations built all four platform directories from the same
clean source revision; recursive comparison found every payload byte identical.
Each package contains 319 inventoried files. These are unsigned candidates under
ignored `dist/stage5-final-a` and `dist/stage5-final-b`; the table binds the manifest
bytes used by the signing workflow, not a publisher-approved signature.

## Checks performed

- Full `go test ./...` and `go vet ./...` passed after correction of an existing
  prelaunch cancellation race discovered during the regression run.
- Focused race checks covered release verification/installation and supervisor/worker
  behavior. The prelaunch cancellation regression passed five race-enabled runs.
- Generated contract packages passed their own Go tests and all 28 Python tests.
- Three release-script tests cover dependency inventory and host/harness publication
  compatibility rejection. GPG tests exercised actual temporary keys and signatures.
- Both build trees matched using `diff -r`; the matching-harness-lock check passed.
- Real macOS ARM64 `0.1.0`/`0.2.0` packages passed the installed-release test: actual
  detached signatures, bounded archive verification, fresh installation, contract
  loading, repeat install, upgrade, old-binary retention, explicit downgrade,
  corrupt-archive rejection, preserved configuration/evidence and a launcher path
  containing spaces and a quote. All writes stayed in temporary directories.
- Unit tests cover interrupted staging/publication/activation, lost activation
  acknowledgement, conflicting account refusal and Linux provisioning command plans.

The installed-release test generated and deleted an ephemeral signing key. It did
not use a production publisher key, configure this host, provision a Linux account,
contact Docker, publish a release or change campaign state outside test directories.

## Reproduction and remaining qualification

Use [the distribution guide](HOST_DISTRIBUTION.md) for current build and install commands. Build two separate outputs from the tested commit and compare them.
Then run `scripts/test_installed_release.py` on a native candidate, optionally
providing a newer native candidate with `--upgrade`.

CI is configured but was not observed executing here. Linux provisioning still
needs a native administrator-run qualification. All four production host/container
tuples, live provider/secret-store/target routes, refreshed harness images and
publisher-approved contract/image/executable publication remain external gates.
A draft-release preflight cannot substitute for those gates.

## Candidate publication workflow — 2026-10-11

The rolling prerelease and candidate-to-main promotion path was exercised locally
against a simulated GitHub service. Tests cover publication, replacement, exact
asset preservation on promotion, repeated promotion, invalid branch versions,
stale builds, interrupted uploads/retry, changed assets/tags and mismatched merge
trees. Actionlint 1.7.7 passed for the updated workflow.

A package smoke test reused all four real `stage5-final-a` candidates identified
above, signed them with a disposable passphrase-protected GPG key using the new
publisher, and passed the resulting 14 assets through simulated publication and
promotion. All asset bytes were preserved, and the signed candidate inventory
verified. This exercised packaging/signing with actual binaries; it did not rebuild
or qualify their runtime and did not contact GitHub to publish them.

Live Actions publication requires configured publisher credentials and repository
settings from [candidate releases](../.github/maintainers/CANDIDATE_RELEASES.md). No live release was
created or promoted during this implementation.

## Initial catalog integration — 2026-10-12 (superseded publisher)

The initial integration dispatched the catalog caller at the release tag and
waited for its completion. The local-publisher integration below replaces its
centralized workflow dependency. Candidate/stable entries generated by the local GitHub fixture
passed the reviewed service's actual `releasectl catalog merge` validator, including
promotion with unchanged archive identities. The service pin is
`fc4618b56a7862a410558e7824b3f5681e426714`; its bundled skill references matched the
clean checkout, and GitHub confirmed that the commit is available.

Local tests cover the full candidate/catalog/stable path, byte preservation, failed
and skipped publication, timeout and cancellation, stale source/metadata, incorrect
product URLs, and stable promotion evidence. Both Actions workflows pass actionlint.
No GitHub release, S3 catalog, AWS role or environment setting was changed. Live
publication still requires Operator's production environment variables and tag policy
documented in [the maintainer guide](../.github/maintainers/CANDIDATE_RELEASES.md).

## Product-local catalog publication — 2026-10-13

Operator now carries the complete catalog-only export from reviewed releases
commit `c651c5fa47346f1ca02abe33791667f838b59e03`, with its source and file hashes in
`tools/release-publisher/publisher-source.json`. The ordinary catalog job checks
its actual tagged GitHub assets and validates the entry before obtaining AWS
credentials. It uses Operator's production environment and catalog-only role,
with no private publisher download or reusable workflow at runtime.

The 22 release-script tests passed, including an integration case carrying
Operator-generated four-platform entries through the actual local Go merger and
shell publisher with simulated GitHub/S3 and localhost HTTP delivery. It exercised
candidate creation/replacement, promotion with unchanged signed-asset fixture
bytes and tag, preservation of older versions, stable retry, and rejection of
stale demotion and changed final artifacts. This is fixture evidence, not a new
binary build or native qualification. The provenance test verified every exported
file against the reviewed source manifest.

The exported publisher's Go race tests and vet passed, as did all 12 publication
tests. Actionlint 1.7.12 passed for both release workflows. GitHub/AWS deployment readiness is separate from these local checks; use
the numbered setup and first-candidate walkthrough in
[the maintainer guide](../.github/maintainers/CANDIDATE_RELEASES.md). No live release
or catalog was published during this update.

Read-only deployment checks on 2026-10-13 confirmed that AWS role
`releases-operator_sandbox-catalog-publisher` already trusts the required local
workflow/custom subject. Operator's `production` environment has the matching
four service variables, the `v*` tag-only rule, and a required reviewer. GitHub
still reports `use_default: true` for its OIDC subject: the maintainer must apply
step 6 before publication. The GitHub default branch currently exposes only
`verify-release.yml`; the new catalog workflow and local publisher must be pushed
before dispatch is available. No repository-level Actions secrets or signing
fingerprint variable were returned; configure step 3 or confirm equivalent
organization-scoped signing settings are available to this repository. These are
point-in-time observations, not permanent deployment requirements. No settings
were changed by these checks.
