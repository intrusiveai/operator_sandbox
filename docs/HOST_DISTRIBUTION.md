# Host distribution and installation

## Release trust and format

Host releases MUST use publisher-controlled OpenPGP detached signatures. The
administrator MUST obtain the trusted public keyring independently of incoming
release files. Verification MUST use only that explicitly selected keyring,
without ambient keys, key discovery or network access. SHA-1/MD5 signatures MUST
be rejected. Private signing keys MUST remain outside packages and images.

`operatorctl release manifest` MUST bind the host version, platform, source commit,
Go toolchain, exact shared-contract pin and every payload file's name, size, mode
and SHA-256 digest. The four platforms are `linux/amd64`, `linux/arm64`,
`darwin/amd64` and `darwin/arm64`. Host release versions MUST be three numeric
components. Candidate builds and signatures MUST NOT imply native qualification.

The directory MUST include `bin/operatorctl`, the complete `contract/` package,
configuration/profile templates, `sbom.spdx.json` and `provenance.json`.
`release.json` and its detached `release.sig` MUST be the only files outside the
manifest inventory. Content MUST match the installed contract loader as well as
the manifest. Executable bytes MUST never be run to inspect an unverified release.

`operatorctl release check --directory DIR --keyring /absolute/trusted.gpg`
MUST authenticate exact manifest bytes before validating the inventory.
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
systemd user-manager prerequisites. Granting access to the Docker socket MUST be
an explicit administrator choice. macOS MUST use the logged-in Docker Desktop
user. Both profiles MUST retain the existing private configuration/state locations.
Installation MUST NOT embed credentials in templates.


## Installing a verified archive

The first executable MUST be authenticated before running it: verify the
publisher's detached signature on `SHA256SUMS`, check the archive's checksum, then
extract it into a private staging directory. Subsequent installations MAY use an
already trusted `operatorctl`. The installer MUST independently authenticate the
internal manifest and every file before activation. GnuPG (`gpgv`, and `gpg` for
publication/key export) MUST be installed by the administrator.

```sh
gpgv --keyring /absolute/publisher.gpg SHA256SUMS.asc SHA256SUMS
sha256sum --check SHA256SUMS # macOS: shasum -a 256 --check SHA256SUMS
# After checksum verification, unpack the selected archive into a private directory.
/path/to/verified/bin/operatorctl install \
  --archive /absolute/operator-host.tar.gz --keyring /absolute/publisher.gpg \
  --image registry.example.com/attack_harness:local
```

Default installation roots MUST be `/opt/operator` on Linux and
`~/Library/Application Support/Operator/installation` on macOS. `--root`, `--config`
and `--state-root` MAY explicitly select other absolute paths. Installation and
campaign state MUST NOT overlap. The installer MUST run as the intended service
user, with private owned directories. Linux provisioning is the separate root step:

```sh
sudo /path/to/verified/bin/operatorctl install provision-linux --grant-docker-access
```

Provisioning MUST create a missing `operator` system account with home
`/var/lib/operator` and shell `/usr/sbin/nologin`. An existing conflicting account
MUST fail without modification. It MUST prepare `/opt/operator`, `/etc/operator`,
`/var/lib/operator` and `/run/operator`, enable systemd lingering and start the user
manager. It MUST install `/etc/tmpfiles.d/operator.conf` so `/run/operator` is
recreated privately after reboot. Docker group membership MUST be changed only with the explicit flag;
Docker itself MUST already be installed. Adding membership to an existing running
user manager may require an administrator-controlled restart when campaigns are
idle. Provisioning MUST NOT restart or terminate existing campaign services.

Linux administrators MUST run campaign/install commands as `operator`, with
`XDG_RUNTIME_DIR=/run/user/<operator-uid>` and
`DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/<operator-uid>/bus`. For example:

```sh
sudo -u operator env XDG_RUNTIME_DIR=/run/user/991 \
  DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/991/bus \
  /opt/operator/operatorctl doctor --offline
```

The UID above is illustrative; use the UID returned by provisioning. Archives and
the trusted keyring MUST be readable by that account. Runtime commands MUST NOT
invoke sudo themselves. macOS installation MUST reject root execution.

The installer MUST retain `releases/<version>-<os>-<arch>/`, atomically switch the
`current` symlink and provide a stable `operatorctl` launcher that resolves the
selected concrete release before execution. Files/directories MUST be synchronized
before activation. Concurrent installers MUST be excluded by a private lock.
Incomplete staging MUST be discarded on retry. A committed activation with a lost
acknowledgement MUST be safely repeatable. Same-version changed contents MUST fail.

A fresh install MUST create a private configuration selecting the signed contract
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
an inventory of the host OS. The publisher's signature authenticates these files;
locally generated provenance does not independently attest a trusted CI builder.

CI MUST run host/shared tests and build each platform twice, comparing the entire
unsigned candidate payload. Archive/signature envelopes MAY differ when signing
occurs at different times; the executable, contract and payload inventories MUST
match. CI artifacts MUST preserve file modes and MUST remain labeled candidates.
The workflow uses pinned [setup-go](https://github.com/actions/setup-go/commit/d35c59abb061a4a6fb18e82ac0862c26744d6ab5)
and checkout/upload action revisions. Cross-compilation MUST NOT claim native qualification.

After reviewing a candidate, the publisher MAY sign it using an explicit full key
fingerprint and its independently installed public keyring:

```sh
python3 scripts/sign_host_release.py --directory dist/candidate/linux-amd64 \
  --output dist/signed/linux-amd64 --operatorctl /absolute/trusted/operatorctl \
  --signing-key FULL_FINGERPRINT --keyring /absolute/publisher.gpg
```

The signing tool MUST sign `release.json`, verify it, archive the exact payload,
and emit independently signed `SHA256SUMS` for first-install bootstrap. It MUST
not export private keys or publish an image approval. Key rotation MUST be an
explicit administrator update to the trusted keyring, independently authenticated.

`check_release_pair.py` MUST compare host and harness contract pins. The final
`publish_host_release.py` preflight MUST require four signed host archives plus
both architecture harness build reports, identical host source/version/contracts,
matching harness compatibility pins and compatible minimum Operator versions.
By default it MUST only validate. `--create-draft` MAY create a GitHub draft for an
existing version tag; it MUST NOT publish the draft or the HTTPS image approval.
Pass each platform using repeated `--signed-directory` and both reports using
repeated `--harness-report`, plus `--operatorctl` and `--keyring`. Draft checksum
assets MUST be named `<platform>-SHA256SUMS[.asc]` to avoid collisions.

Administrators MUST supply publisher keys, protected release credentials and final
qualification evidence before approved publication. Release tooling MUST not
fabricate those external approvals or upgrade development contract status.
