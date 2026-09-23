# Canonical JSON and identity verification

This stage implements `jcs-v1` serialization and canonical identity checks in the
shared Go/Python library. It adds no wire fields and does not publish the contract
package or establish trust in a caller-supplied launch description.

## Canonical byte profile

`jcs-v1` follows [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785.html) within
Operator's stricter JSON input domain. Object keys sort recursively by unsigned
UTF-16 code units; array order is retained. Strings preserve Unicode spelling,
with JSON control/quote/backslash escaping and no Unicode normalization or HTML
escaping. Numbers use shortest binary64 round-trip digits with ECMAScript decimal/
exponent layout; negative zero becomes `0`. There is no formatting whitespace or
terminal newline. Scalars, arrays and objects are supported.

The existing strict decoder runs first: duplicate keys, invalid UTF-8, lone
surrogates, nonfinite numbers, nonzero binary64 underflow, unsafe integer-valued
binary64 numbers and nesting beyond 32 are rejected. Consequently some numbers
permitted by general RFC 8785 are outside this contract. Input and canonical
output must both fit the caller's explicit byte ceiling.

**Validate the applicable schema before canonicalizing.** Decoders retain exact
decimal values for schema checks. Canonicalization intentionally rounds to
binary64 for identity. For example, a fractional number that rounds to `1` must
not become an accepted integer field by canonicalizing it before validation.

The generic canonicalizer never drops fields. A field named `digest`, `signature`
or `object_digest` remains ordinary content unless the specific schema recipe
below explicitly omits it. There is no caller-supplied list of exclusions.

## Implemented recipes

All digests are lowercase `sha256:<hex>`. Raw and canonical identities are distinct.

| Identity | Exact preimage |
|---|---|
| File/artifact descriptor `digest` | Original file bytes. |
| Manifest descriptor `object_digest` | Canonical complete parsed manifest. |
| `engine_context_object_digest` | Canonical complete EngineContext. |
| SkillSetManifest `loading_digest` | Canonical SkillSetManifest with only the top-level `loading_digest` omitted. |
| `capability_projection_digest` | Canonical `capabilities` object only, using the existing public capability recipe. |
| Bootstrap `catalog_digest` | Canonical complete installed `catalog.json` mapping. |
| Bootstrap `operations_digest` | Canonical complete installed `operations.json`. |

Catalog identity binds the URI-to-file mapping, not the contents of all referenced
schemas. The [package inventory](PACKAGE_INTEGRITY_CONTRACT.md) separately binds
every declared schema, validator/profile and fixture resource. Native Interceptor source digests and OCI
image identities retain their own recipes; these helpers do not reinterpret them.
Canonicalizing ScenarioBundle or other schema-defined objects is available through
the generic helper but does not invent a new wire field for their object identity.

## Library entry points

| Go | Python | Result |
|---|---|---|
| `RawDigest(raw)` | `raw_digest(raw)` | SHA-256 of exact bytes. |
| `Canonicalize(raw, maximum)` | `canonicalize(raw, maximum)` | Strict decode and bounded canonical bytes. |
| `CanonicalDigest(raw, maximum)` | `canonical_digest(raw, maximum)` | SHA-256 of canonical bytes. |
| `Protocol.RegistryDigests()` | `Protocol.registry_digests()` | Copy of the installed catalog/registry identities captured during loading. |
| `Protocol.ValidateLaunchIdentities(...)` | `Protocol.validate_launch_identities(...)` | Launch-content checks plus canonical bindings listed below. |
| `Protocol.ValidateArtifactContent(beginRequest, content)` | `Protocol.validate_artifact_content(begin_request, content)` | Raw artifact identity and claimed canonical-form checks. |

Python exports the generic helpers from `operator_contracts`; its default byte
ceiling is the ordinary-message 4 MiB limit. Larger input contexts must supply the
appropriate explicit limit. Go always requires that argument. APIs accept bytes,
not arbitrary language objects, so JSON validation precedes serialization and no
object hooks, implicit coercions or native serializer defaults enter identity.

`ValidateLaunchIdentities` takes the same arguments as `ValidateLaunchContent`.
It runs that complete validation first, then checks input-tree/skill-set canonical
descriptor digests, EngineContext canonical identity, every selected skill manifest's
canonical descriptor, the skill loading digest, the public capability projection,
and the loaded catalog/registry identities. A matching raw digest cannot substitute
for an incorrect canonical digest. The older content/transcript APIs retain their
documented scope; callers needing these additional checks must use the identity API.
When invoked on a protocol returned by the verified package loader, this API also
requires the launch's package version/digest to match the loaded package pin.

`ValidateArtifactContent` takes the original validated `engine.artifact_begin`
request from trusted upload state and the complete stored bytes. It verifies
declared size and raw digest. If `canonicalization` is `jcs-v1`, it also requires
`application/json` through the existing request schema and checks that the bytes
already equal their canonical form. It rejects a noncanonical upload instead of
rewriting bytes or changing its digest. `raw` preserves arbitrary bytes. Content
schemas, upload-offset tracking, commit persistence, campaign membership and
receipt issuance remain separate checks.

These helpers do not authenticate metadata. The caller must resolve original
messages from trusted launch/upload records, verify the release and supported
package digest, check native source compatibility, and verify actual reference and
skill-file bytes. Loader/bundle authenticity, package publication and live runtime
gates remain required. Matching self-consistent attacker-supplied hashes proves
neither publisher identity nor campaign admission.

## Conformance and regeneration

Both language suites consume:

- [306 canonicalization vectors](fixtures/canonicalization.json), pinning exact
  canonical UTF-8 bytes and SHA-256 values, including UTF-16 key order, numeric
  boundaries, 256 deterministic binary64 samples and rejected input/output bounds.
- [21 identity vectors](fixtures/identity-validation.json), covering a complete
  startup with selected skills, individually corrupted canonical pins, and raw/
  canonical artifact uploads. Each negative launch-identity vector first passes
  the prior content validator, proving that the additional check detects it.

The authoring script `python3 scripts/generate_identity_fixtures.py` uses Node's
ECMAScript serializer as an independent golden-output oracle. It imports neither
production canonicalizer. Node is not a library or `make test` dependency. Fixtures
were generated with Node v22.13.1. Registry-dependent launch vectors must be
regenerated and reviewed when installed catalog/registry contents change. Normal
tests compare committed bytes, verify canonicalization idempotence and reject
stale/mismatched expected identities; they do not silently rewrite fixtures.
