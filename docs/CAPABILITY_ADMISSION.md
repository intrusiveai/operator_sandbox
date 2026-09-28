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
specified hash-derived filename; filesystem export writing remains a CLI
integration responsibility; `LoadExport` implements safe companion import. Keep native companions in host provenance storage,
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

## Host preparation flow

1. `LoadExport` safely reads a submitted public file and its deterministic native
   companion. It uses the existing bounded, no-follow regular-file reader and
   rejects hard links, special files, missing companions and hash mismatches.
   `Import` is the equivalent for already captured host-retained bytes.
2. `ParseBundle` checks schema, unique IDs, internal links, objective coverage,
   UTF-8 byte limits and artifact descriptor consistency. Supporting artifact
   bytes still need hash/size verification and immutable staging. A required
   item's artifact reference cannot point at a declared omission.
3. `BindLive` consumes native attach/status results and the host's pinned instance
   ID. It verifies readiness, campaign/session/revision, capability/environment/
   application hashes and advertised native feedback profile. It preserves worker
   attribution without making another worker's attribution an access restriction.
4. The trusted TargetProfile/route resolver supplies a `Policy` snapshot. Explicit
   allowed references constrain dependencies; action families also need at least
   one selectable route. Missing policy permissions deny access. The resolver
   must evaluate actual scopes/routes; it must not populate decisions simply by
   copying every exported capability. `attemptadapter.Resolve` now implements
   concrete operation/injection scope resolution and derives this policy. Its
   configuration and campaign preparation wiring are implemented by the host service.
5. `Check` compares the unchanged bundle and verified authoring/live exports.
   Required dependencies must be usable. Optional dependencies and suggestions
   yield sorted, explained gaps; unavailable optional objective/scenario routes
   are also listed explicitly. Compatible mode permits unrelated live changes;
   an exact projection pin must match both exports.
6. Persist `Compatibility.RecordJSON()` with the immutable campaign inputs and
   verified contract-package pin. The host-only record includes input/source/raw/
   projection hashes, process/session binding, policy and policy digest, native
   and effective feedback profiles, allowed kinds and gaps. Returned buffers are
   copies. This record is not a replacement for durable campaign admission.

The effective profile is the least permissive native, requested and host-ceiling
profile. `FeedbackKinds == nil` adds no kind restriction; an explicit empty slice
withholds all kinds. Excluding an evidence reference from `AllowedRefs` also
withholds that kind. The native profile remains unchanged for native requests.
The implementation tests the profile/allowed-kind portions of all shared feedback
translation vectors. The [feedback projection](FEEDBACK_PROJECTION.md) additionally
tests all complete vectors and implements selection, byte filtering and immutable
receipt reads. The [attempt broker](ATTEMPT_BROKER.md) implements durable receipt publication.

Recheck readiness, current policy and compatibility before launch; keep the result
and live execution projection with immutable inputs. Later effects still require
artifact staging, effective limits, typed selector/input checks, durable journal
admission and terminal fences. A successful library check does not execute a
request, freeze a running target, mount inputs or launch a harness. CLI capability
export, full TargetProfile routing and campaign orchestration remain integration
work.
