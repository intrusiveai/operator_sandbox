# Installed shared-contract loading

Status: implemented in `internal/contractstore` and `operatorctl contract check`.
This connects the existing Go/Python [package integrity contract](../schemas/PACKAGE_INTEGRITY_CONTRACT.md)
to Operator's host filesystem. Shared message schemas, package identities and
digest recipes are unchanged.

## Trust and installation layout

The caller supplies an absolute package directory and the exact expected
`contracts.PackageIdentity{Version, Digest}` from trusted installation metadata.
The expected pin must be independently selected; deriving it from the package being
checked does not authenticate that package. The loader never selects a version,
downloads a package or executes its source files.

The installed runtime directory contains exactly:

```text
package.json
catalog.json
operations.json
<every inventoried payload file, at its declared relative path>
<only the parent directories needed by those files>
```

This is a content-only installation. Detached publication/signature material stays
outside this directory. The package contract still reserves `signatures` and
`package.json` outside the payload inventory; neither is admitted as an additional
payload. Extra files, hidden files and empty extra directories fail loading.

Files and directories must be owned by root or the executing service user and not
be writable by group or others. Regular payloads may be readable by other users
(for example, installed `0644` files in `0755` directories). Private `0600`/`0700`
installations also work. Symlinks, hard-linked files, special files and special
permission bits are rejected. The selected directory's ancestors are trusted
installation paths. Do not install packages in guest-writable trees or modify them
in place during loading.

## Verification sequence and bounds

`contractstore.Load(ctx, directory, expected)`:

1. Opens and pins the selected directory; checks its type, permissions and owner.
2. Reads `package.json` as a regular file, bounded to 8 MiB. Validates it using
   Operator's embedded manifest schema, before accepting any incoming schema.
3. Checks its canonical digest and package version against the independent expected
   pin. A mismatch fails before any payload file is read.
4. Walks exactly the declared inventory. Each child directory is independently
   pinned; directory reads use batches of at most 128 names. Files require exact
   declared sizes and SHA-256 digests. Before/after metadata checks reject observed
   file replacement or modification during a read.
5. Calls `LoadVerifiedProtocol` with those captured bytes to verify the entire
   payload and compile its catalog/registry offline. External schema references
   fail. Inventoried Go/Python code is data and is never imported or executed.
6. Returns a frozen manifest, a protocol carrying the verified package pin, and a
   verification report. No package files or directory handles remain open.

The existing bounds apply before payload allocation: 4,096 files, 16 MiB per file,
128 MiB aggregate content, path depth 16 and the shared normalized path rules.
Missing/extra files, declared size violations and inventory collisions fail;
links are not used to satisfy declared paths. An actual read is bounded even if
a file grows after its initial size check. The report's file count/content bytes
exclude `package.json`; `manifest_digest` hashes its exact bytes, while the package
pin hashes its canonical object.

Cancellation is checked between reads, directory entries and validation phases.
The loader checks cancellation again before returning a successful result. Local
filesystem syscalls and schema compilation are synchronous; a context cannot
forcibly interrupt a stalled filesystem call or an in-progress compilation.

`Loaded.Protocol()` returns the verified protocol, `Loaded.Manifest()` returns a
copy of the exact manifest, and `Loaded.Report()` returns a value copy. Later disk
changes cannot replace the frozen protocol's schema resources. Keep that loaded
protocol throughout preparation/start instead of reopening unverified schema files.
Filesystem checks do not protect against a hostile service user or administrator.

## Read-only administrative command

```sh
go build -o build/operatorctl ./cmd/operatorctl
build/operatorctl contract check \
  --package-dir /absolute/installed/operator-contracts \
  --package-version "$TRUSTED_CONTRACT_VERSION" \
  --package-digest "$TRUSTED_CONTRACT_DIGEST"
```

Set the expected values from the installation's supported-package metadata. All
three options are required; the command does not default to or infer a package pin.
It is an installation diagnostic, not a required manual step for each campaign.
It uses no host configuration, Docker, HTTPS, state directory or release cache.
It creates no files and does not modify the installation.

Success emits `operator.dev/contract-check/v1alpha1` JSON with `status: verified`,
the directory, package version/digest, raw manifest digest, canonical catalog and
operations digests, payload file count and content byte count. Exit status is **0**
on success, **2** for missing/invalid command arguments, and **1** for failed loading,
verification, cancellation or output. Failure produces no success report, and
diagnostics do not echo package contents.

## Preparation integration and remaining gates

The future distribution/launcher must select its supported package directory/pin,
call this loader, and take `imagerelease.Requirements.Contract` from the verified
protocol's `PackageIdentity()`. The running Operator application version must also
come from the installed executable. Neither an image manifest nor an HTTPS
response can independently select arbitrary installed validators or executable code.

The release response must match that verified package pin. Before guest execution,
also inspect the immutable image's embedded package/engine metadata and enforce
the fixed runtime policy. This loader establishes byte integrity and offline schema
loading; it does not establish source-code conformance, release approval or runtime
qualification. Production package publication, supported-package metadata/build
version packaging, image-file inspection and launch integration remain separate
implementation work. Tests use explicitly identified `0.0.0` fixtures.

Tests cover real development-schema loading, independent pin failures before payload
reads, malformed manifests, source immutability, same-size tampering, missing/extra
entries, links/special files, unsafe permission modes, oversized files, equivalent
JSON integer spellings, offline schema rejection, cancellation, concurrent loads
and the read-only CLI.
