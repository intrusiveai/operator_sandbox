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

The implementation and lifecycle test commands below will be completed alongside
the installer; signature/archive validation is the first tooling boundary.
