# Shared package manifest and integrity

This stage defines the closed [package manifest](contract-package.schema.json) and
matching Go/Python construction, verification and loading APIs. It implements the
package-content identity required by [SHARED_CONTRACT.md](SHARED_CONTRACT.md).
It does not publish `0.1.0`, complete model codecs or certify runtime conformance.

## Manifest and payload

The package root contains `package.json`, with these required fields:

| Field | Content |
|---|---|
| `api_version` | `operator.dev/contract-package/v1alpha1`. |
| `package_version` | Stable `major.minor.patch`, matching the existing contract package version field. |
| `files` | Sorted complete payload inventory: `path`, `size_bytes`, raw SHA-256 `digest`. |
| `catalog` | `path: catalog.json` and its canonical `object_digest`. |
| `operations` | `path: operations.json` and its canonical `object_digest`. |
| `semantic_profiles` | Sorted entries containing `profile_id`, payload `path` and raw file `digest`. |

The package digest is `sha256(jcs-v1(complete manifest))`. The manifest has no
self-digest field. Formatting changes in `package.json` do not change this object
identity; changes to any inventoried file's raw bytes do, even whitespace changes
inside schema or registry files. The builder emits canonical manifest bytes.

`package.json` and the reserved top-level `signatures` subtree are excluded from
the payload inventory. Neither is an unverified payload escape hatch: verifier
arguments contain **only** inventoried payload resources, with the manifest passed
separately. No other missing or extra payload is allowed. Reserved names also
reject case-fold aliases and use as parent directories. The current release
approval mechanism remains the host's pinned HTTPS release record; this schema
does not introduce a signature distribution or authorization mechanism.

The catalog mapping and registry remain at the package root. Schema filenames
referenced by the catalog are unique, bare `*.schema.json` names at that same root,
matching the existing offline loader. Supporting resources may use subdirectories:

```text
package.json
catalog.json
operations.json
*.schema.json
semantics/...
go/...
python/...
fixtures/...
```

Use the existing manifest path profile: NFC, Unicode 15.0 repertoire, 1,024 UTF-8
bytes and depth 16, no traversal or links, and no file/directory or case-fold
collisions at any prefix. Inventory order is UTF-8 path order. Semantic profile
IDs use the existing ASCII ID syntax and must be unique and sorted.

The manifest binds at least `jcs-v1`, `manifest-paths-v1` and `harness-loop-v1` to
explicit nonempty semantic-profile resources. Every profile's path must occur in
the inventory with the same raw digest. Additional named profiles are permitted
and bound by the package identity. They are content identities, not an executable
plugin registry or a runtime negotiation mechanism. Package verification does not
determine whether arbitrary profile prose correctly describes implementation.

Bounds are 8 MiB encoded manifest, 4,096 payload files, 16 MiB per file and 128 MiB
aggregate payload bytes. There are 3–64 semantic profile entries. `catalog.json`
and `operations.json` additionally retain the shared 4 MiB JSON parsing ceiling.
Payload limits apply independently of campaign input/skill limits. Host installers
must enforce these bounds while reading or unpacking, before supplying buffers to
these APIs; the APIs do not implement archive extraction or filesystem staging.

## APIs and verification order

The existing installed protocol supplies the trusted manifest schema. Do not use
a schema taken from an unverified incoming package to validate that package.

| Go method on `Protocol` | Python method | Purpose |
|---|---|---|
| `ValidatePackageManifest(raw)` | `validate_package_manifest(raw)` | Closed metadata, path/profile ordering, declarations and bounds. |
| `BuildPackageManifest(version, files, profiles)` | `build_package_manifest(version, files, profiles)` | Produce canonical manifest bytes and package identity from explicit frozen payloads. |
| `VerifyPackage(manifest, files, expected)` | `verify_package(manifest, files, expected)` | Check an exact payload set against the caller's trusted expected identity. |
| `LoadVerifiedProtocol(manifest, files, expected)` | `load_verified_protocol(manifest, files, expected)` | Freeze payloads, verify, then load the offline schema catalog and registry. |
| `PackageIdentity()` | `package_identity()` | Return a copy of the verified package pin, or indicate that no package pin was verified. |

Go uses `map[string][]byte` for payloads, `map[string]string` for profile-ID-to-path
bindings and `PackageIdentity{Version, Digest}` for the pin. Python uses dictionaries
of immutable `bytes`, profile-ID-to-path strings and
`{"package_version": ..., "package_digest": ...}` respectively. Builders return
manifest bytes plus the computed identity. They select no ambient files, choose
no release version, perform no network calls and publish nothing.

Verification performs these checks in order:

1. Validate the manifest with the already-installed schema and inventory rules.
2. Match both its version and canonical digest to the expected pin supplied by the
   trusted caller. No fallback to another package version or digest is allowed.
3. Require exactly the declared payloads, checking every size and raw digest.
4. Check canonical catalog/registry identities and ensure catalog entries resolve
   only to unique inventoried schema files.
5. For the verified loader, compile the checked schema resources and operation
   registry from the same frozen payload snapshot. Existing closed schemas,
   operation references and offline resolution rules still apply.

The metadata/payload verifier proves byte integrity; it does not compile all
schemas. Therefore a byte-consistent but invalid catalog can pass integrity
verification and still fail loading. Both steps must succeed before using the
returned protocol. Payload validator source files are hashed as data, never
executed or imported by this loader. Distribution of matching executable Go/Python
library builds remains a packaging responsibility.

Go copies the bounded payload buffers before verification and compilation. Python
snapshots the mapping and requires immutable byte values. Callers must not mutate
Go input buffers concurrently during the call. Changes to caller mappings/buffers
after loading cannot replace the compiled catalog or registry.

## Launch pin and trust boundary

`LoadProtocol` / ordinary Python `Protocol(...)` remain development loaders for
trusted local schema directories; they do not claim package verification. A verified
loader records the exact package pin. Its `ValidateLaunchIdentities` additionally
requires the startup/context contract version and package digest to match that pin,
alongside the existing catalog, registry and canonical input identities.

An expected digest copied from the incoming manifest is not an independent trust
decision. Runtime callers must obtain the expected version/digest from their
installed supported-package set and the verified release compatibility record.
Image/release authenticity, executable-build provenance, actual filesystem
permissions/no-follow reads, capability admission and runtime gates remain separate.
The verifier is not a substitute for those checks or for release qualification.

The current development library can create and verify manifests; no production
package manifest or release is checked in by this stage. Publication still requires
the advertised codec/tool profiles, full conformance and packaging work. Tests use version
`0.0.0` and explicitly identified fixture semantic resources.

## Tests and regeneration

[package-integrity.json](fixtures/package-integrity.json) contains 39 shared cases
for manifest structure, expected pins, payload tampering, missing/extra files,
reserved/unsafe/colliding paths, semantic bindings and size bounds. Its small
payload is a byte-integrity fixture, not an executable contract release.

Both language suites separately build and load the actual development schema
catalog, check a real control message, reject external schema references and a
launch naming another package, and verify isolation from caller mutation.

Regenerate golden package vectors with
`python3 scripts/generate_package_fixtures.py`. Like the canonical identity fixture
authoring script it imports, this uses Node only as an independent fixture oracle;
ordinary tests and installed libraries do not depend on Node. Adding the package
schema changes the installed catalog identity, so this stage also refreshes the
existing launch identity fixtures through their generator.
