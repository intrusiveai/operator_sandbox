# Capability verification and bundle admission

`internal/capabilities` is the host adapter for the accepted
[public capability contract](../schemas/CAPABILITY_EXPORT_CONTRACT.md).

## Verified exports

Pass the installed trusted `contracts.Catalog`, exact native export bytes, and the
configured logical target ID to `FromNative`. It checks strict JSON, native typed
hashing, supported versions/vocabulary, duplicate identifiers and delivery
contracts before creating the allowlisted public projection. `Export` keeps its
state private and returns copies of JSON buffers. Public projection hashes use
shared JCS; native hashes retain Interceptor's typed serialization and null rules.

`Import` takes public JSON and its exact native companion. It rebuilds the public
projection and checks the whole document, including both digests and logical
identity. A source digest on its own is insufficient. `CompanionName` supplies the
specified hash-derived filename; filesystem import/export wiring remains a CLI
integration responsibility. Keep native companions in host provenance storage,
not harness inputs.

`internal/nativedelivery` preserves Interceptor's closed offline delivery-schema
profile, contract/body/example limits and strict decoder. The native structs and
profile implementation record their source revision. Changes to Interceptor's
serialization/profile require an explicit adapter update and native fixture
comparison; they must not be guessed from unfamiliar fields or enum values.

The committed native export, exact Go hash preimage, JCS projection and public
export are independent golden files for the Go tests. Tests also cover altered
source/public bytes, unsupported vocabulary, duplicate IDs, Unicode, legacy
missing delivery information and source-only changes that preserve projection.

An export describes available capabilities. It grants no execution permission and
does not assert that a session is ready. Operation methods/paths, network/provider
configuration and native provenance stay out of public execution choices.
