# Installation and release-tooling validation

Recorded 2026-09-28. This record covers implemented host distribution tools and
local tests. It does not grant production image approval or native runtime qualification.

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

Use [the distribution guide](HOST_DISTRIBUTION.md) for the build, signing and install
commands. Build two separate outputs from the tested commit and compare them.
Then run `scripts/test_installed_release.py` on a native candidate, optionally
providing a newer native candidate with `--upgrade`.

CI is configured but was not observed executing here. Linux provisioning still
needs a native administrator-run qualification. All four production host/container
tuples, live provider/secret-store/target routes, refreshed harness images and
publisher-approved contract/image/executable publication remain external gates.
A draft-release preflight cannot substitute for those gates.
