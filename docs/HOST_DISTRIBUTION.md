# Host distribution and installation

## Release format and integrity

Administrators MUST obtain release archives from the project's GitHub releases.
Packages MUST include a file manifest, and publication MUST provide archive
checksums. These checks detect corruption; they do not authenticate the publisher.

`operatorctl release manifest` MUST bind the host version, platform, source commit,
Go toolchain, exact shared-contract pin and every payload file's name, size, mode
and SHA-256 digest. The four platforms are `linux/amd64`, `linux/arm64`,
`darwin/amd64` and `darwin/arm64`. Host release versions MUST be three numeric
components. Candidate builds MUST NOT imply native qualification.

The directory MUST include `bin/operatorctl`, the complete `contract/` package,
configuration/profile templates, `sbom.spdx.json` and `provenance.json`.
`release.json` MUST be the only file outside the manifest inventory. Content MUST match the installed contract loader as well as
the manifest. Release checks MUST inspect files without executing their contents.

`operatorctl release check --directory DIR` MUST validate the manifest, file
inventory and contract contents. `--archive FILE` MAY inspect a local archive.
`operatorctl release archive --directory DIR --output NEW.tar.gz` MUST produce
reproducible gzip/tar framing with fixed timestamps and ownership. Extraction MUST
reject links, special files, duplicate paths, executable data files, traversal,
unsupported entries and truncated/oversized archives. Bounds are 8,192 payload
files, 128 MiB per file and 256 MiB total, with separately bounded metadata.

## Installation and updates

Installation MUST be an explicit administrator action using a local archive.
There MUST be no automatic release download or age-based expiration. Activation
MUST reject a lower version unless the administrator explicitly selects downgrade.
The installer MUST preserve versioned binaries/contracts and existing configuration,
skills, campaign pins, journals, reports and evidence. It MUST NOT resume campaigns.

Linux installation MUST provision a dedicated `operator` service account and the
private filesystem paths. The optional `--service` provisioning flag MUST enable
systemd user-manager prerequisites; foreground operation MUST NOT require a service manager. Granting access to the Docker socket MUST be
an explicit administrator choice. macOS MUST use the logged-in Docker Desktop
user. Both profiles MUST retain the existing private configuration/state locations.
Installation MUST NOT embed credentials in templates.


## Installing an archive

Download the archive and matching checksum file for your host from the same
GitHub release. Check the archive checksum, then extract it into a private staging
directory. The installer MUST validate the internal manifest and every file before
activation. Subsequent installations MAY use the already installed `operatorctl`.

For Linux AMD64, with both downloaded files in the current directory:

```sh
sha256sum --check linux-amd64-SHA256SUMS
umask 077
mkdir operator-staging
tar -xzf linux-amd64.tar.gz -C operator-staging
./operator-staging/bin/operatorctl install \
  --archive "$PWD/linux-amd64.tar.gz" \
  --image registry.example.com/attack_harness:local
```

On macOS, select `darwin-arm64` or `darwin-amd64` and use
`shasum -a 256 --check <platform>-SHA256SUMS`. Complete Linux provisioning below
before running `install` as the `operator` account; ensure that account can read
the archive and access the staged executable.

Default installation roots MUST be `/opt/operator` on Linux and
`~/Library/Application Support/Operator/installation` on macOS. `--root`, `--config`
and `--state-root` MAY explicitly select other absolute paths. Installation and
campaign state MUST NOT overlap. The installer MUST run as the intended service
user, with private owned directories. Linux provisioning is the separate root step:

```sh
sudo /path/to/staged/bin/operatorctl install provision-linux --grant-docker-access
```

Provisioning MUST create a missing `operator` system account with home
`/var/lib/operator` and shell `/usr/sbin/nologin`. An existing conflicting account
MUST fail without modification. It MUST prepare `/opt/operator`, `/etc/operator`,
`/var/lib/operator` and `/run/operator`. Only `--service` MUST enable systemd
lingering and start the user manager. It MUST install `/etc/tmpfiles.d/operator.conf` so `/run/operator` is
recreated privately after reboot. Docker group membership MUST be changed only with the explicit flag;
Docker itself MUST already be installed. Adding membership to an existing running
user manager may require an administrator-controlled restart when campaigns are
idle. Provisioning MUST NOT restart or terminate existing campaign services.

Linux administrators MUST run campaign/install commands as the configured account
(`operator` for the provisioned installation). Optional service execution requires
`XDG_RUNTIME_DIR=/run/user/<operator-uid>` and
`DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/<operator-uid>/bus`. For example:

```sh
sudo -u operator env XDG_RUNTIME_DIR=/run/user/991 \
  DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/991/bus \
  /opt/operator/operatorctl doctor --offline
```

The UID above is illustrative; use the UID returned by provisioning. Archives MUST be readable by that account. Runtime commands MUST NOT
invoke sudo themselves. macOS installation MUST reject root execution.

The installer MUST retain `releases/<version>-<os>-<arch>/`, atomically switch the
`current` symlink and provide a stable `operatorctl` launcher that resolves the
selected concrete release before execution. Files/directories MUST be synchronized
before activation. Concurrent installers MUST be excluded by a private lock.
Incomplete staging MUST be discarded on retry. A committed activation with a lost
acknowledgement MUST be safely repeatable. Same-version changed contents MUST fail.

A fresh install MUST create a private configuration selecting the packaged contract
pin and explicit local image. Every install MUST emit a versioned `.yaml.example`
configuration. Existing configuration MUST remain byte-for-byte unchanged;
administrators MUST review the example and update contract/profile selections
before starting campaigns with an incompatible new release. Profile templates are
under the selected release's `templates/`; copy and edit them in the private config
directory, then set `model.profile_file`, `target.profile_file` and credential
references. Templates MUST contain no usable credentials.

The installer MUST NOT garbage-collect older releases. Existing workers retain
concrete executable paths and frozen protocol/input bytes. A queued start whose
administrator configuration changes MUST fail its existing input-fingerprint check;
it MUST NOT execute with silently substituted inputs or resume an earlier campaign.

## Reproducible candidates and publication

`python3 scripts/build_host_release.py --version 0.1.0 --output dist/candidate`
MUST build all four host platforms from a clean committed checkout with Go 1.26.5,
CGO disabled, fixed architecture baselines, trimmed paths and VCS/time-independent
build flags. `go mod download` is the explicit dependency-preparation step; the
builder MUST verify module sums and disable dependency network access during build.
`--platform` MAY select a subset for development. Build output MUST be a new directory.

The builder MUST reproduce `release/contract-lock.json`, using the existing contract
publisher and exact matching schema bytes. The host executable MUST embed this
supported pin and source commit; `operatorctl version` MUST expose them. Runtime
submission, skills, capabilities and campaign preparation MUST reject another
configured pin. Explicit `contract check` and `release check` MUST remain usable for
inspecting a future package before activation. The current `0.0.0` package is a
development candidate; tooling MUST NOT silently label it an approved `0.1.0` package.

Every candidate MUST include an SPDX 2.3 dependency inventory and an in-toto/SLSA
provenance statement binding the binary digest, source commit, toolchain, target
platform and contract. Unknown dependency licensing MUST remain `NOASSERTION`.
The inventory describes compiled Go modules and the toolchain, not a vulnerability scan or
an inventory of the host OS. Locally generated provenance does not independently attest a trusted CI builder.

CI MUST run host/shared tests and build each platform twice, comparing the entire
candidate payload. Executables, contracts and payload inventories MUST match.
CI artifacts MUST preserve file modes and MUST remain labeled candidates.
The workflow MUST use pinned setup-go, checkout and upload action revisions.
Cross-compilation MUST NOT claim native qualification.

To package a reviewed build using a native `operatorctl`:

```sh
/absolute/operatorctl release archive --directory dist/candidate/linux-amd64 \
  --output linux-amd64.tar.gz
sha256sum linux-amd64.tar.gz > linux-amd64-SHA256SUMS
```

The publisher MUST validate each manifest and payload before archiving it and
emit a corresponding `<platform>-SHA256SUMS` file for each archive.

GitHub Actions MUST publish a rolling prerelease for each successful push to
`candidate-v<major.minor.patch>`, including merge commits. The branch version MUST
supply the package version. Publication MUST require Operator CI, reproducible
four-platform packages and matching file inventories. Attack Harness reports and external
runtime qualification MUST remain independent of this publication gate. Runtime
startup MUST continue enforcing the image-release and shared-contract checks.
`check_release_pair.py` remains an optional diagnostic for host/harness pairing.

The rolling release MUST use tag `v<version>` and prerelease status. Updates MUST
replace only workflow-managed prereleases, using a draft while replacing assets;
partial uploads MUST remain unpublished and repairable by an explicit rerun. A
superseded candidate build MUST NOT overwrite the current candidate. Stable
releases MUST NOT be overwritten or returned to prerelease status.

When a same-repository candidate PR merges into `main`, the workflow MUST promote
that existing release without rebuilding, replacing package assets or moving its tag.
It MUST verify the candidate head, matching merge tree, complete uploaded asset
inventory and SHA-256 digests. Missing, incomplete or mismatched prereleases MUST
block promotion. Repeated promotion of the same release MUST be harmless.

The publisher MUST retain the four archives, per-platform checksums,
and a `candidate.json` inventory identifying source/version/contract and
archive digests. A separate mutable `release-index-entry.json` MUST describe the
four actual GitHub archive URLs, filenames and hashes. The workflow MUST publish
that entry through the committed `tools/release-publisher` in its own ordinary
`publish-catalog.yml` job, using Operator's `production` environment and scoped
OIDC role from the protected release tag: `rc` on candidate publication, `release`
after the agreed CI and candidate merge gates. It MUST wait for successful catalog
publication and preserve the package identities on promotion. The workflow MUST
serialize publication and promotion per version. The workflow MUST validate before
acquiring AWS credentials, fetch no private publisher code at runtime, and use
the custom OIDC subject documented in the maintainer guide.
Release status MUST NOT claim native qualification or grant harness runtime qualification.
See [candidate release automation](../.github/maintainers/CANDIDATE_RELEASES.md) for setup and recovery.

## Validation and limits

Host-release tests MUST cover altered payloads and manifests, malformed archives,
exact contract contents, fresh installation,
repeat installation, upgrades/downgrades, incompatible platforms, corrupted input,
interrupted publication/activation, retained evidence and unchanged configuration.
Worker registration tests MUST demonstrate that changing the selected release does
not redirect a previously registered command. An already-canceled worker MUST
perform prelaunch cleanup without attempting container creation.

`python3 scripts/test_installed_release.py --candidate DIR [--upgrade NEWER_DIR]`
MUST exercise real native binaries and packages in temporary directories. It MUST NOT provision machine accounts, alter
system paths or contact Docker. `--upgrade` MUST additionally check the newly
selected version, usability of the old binary and explicit downgrade enforcement.
Linux account/service provisioning MUST use command fixtures in ordinary tests;
actual native Linux provisioning and all four production runtime tuples still
require external qualification.

See [recorded release validation](RELEASE_VALIDATION.md) for tested source identities,
reproducibility results and the remaining external qualification boundaries.
