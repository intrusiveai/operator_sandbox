# Discoverable Interceptor injection targets

## Contract and projection

Operator MUST adopt the administrator-selected Interceptor OpenAPI bytes through
the existing comparison procedure, storing the exact identity in
`internal/interceptor/contract.json`, bytes in `internal/interceptor/openapi.json`
and retrieval provenance in `release/interceptor-contract-source.json`.

Native endpoint structures MUST preserve the authoritative field ordering and
omission behavior of `methods`, `paths`, `keys` and `mutable_fields`. Strict parsing
and native typed hashing MUST remain enabled. Public capability schemas and compact
detail references MUST preserve these selector facts without exposing private
response bodies, identities' credential values, or runtime capture data.

The shared interface and Go/Python validators MUST describe both new endpoint
forms. `schemas/asyncapi.json` and `release/contract-lock.json` MUST be generated
and explicitly approved together. Matching Harness adoption is required through
the existing exact version/hash check; no compatibility bypass is permitted.

## Authorization and inputs

- `http-route-set` MUST use independent exact method/path allowlists. Empty or
  omitted lists impose no endpoint restriction. Operator MUST still require a
  concrete administrator-selected method/path. It MUST reject `HEAD` and avoid
  treating route-set paths as templates. Existing `http-route` behavior stays intact.
- `state-collection` MUST require an advertised collection, existing record key,
  and a placement pointer into an advertised mutable top-level field. Unknown
  keys, root-record replacement and immutable-field writes MUST be rejected.
  No implicit creation, deletion or cross-service permission is granted.
- Route resolution MUST use endpoint metadata, not hard-coded service identifiers
  or collection names. Actual admission MUST retain existing scope, placement,
  pointer, payload, turn, budget and ownership checks.
- The model selector MUST accept a string for a state-collection key or an object
  for a database table key. Both normalizers MUST validate database key columns
  from trusted context and serialize them as before. A string MUST NOT bypass
  database key validation; an object without a database policy MUST be rejected.
- Tool guidance MUST explain discovery, explicit authorization, and that delivery
  schemas describe full records while injection content supplies the selected
  field value. Injection deletion MUST NOT be described as mutation rollback.

## Verification and documentation

Tests MUST use native fixtures captured from the selected Interceptor exporter
and compare exact native hash input, public projection and detached metadata.
They MUST cover restricted, unrestricted and partly restricted route sets, static
and HEAD-only sinks, existing identity records, cross-issuer selections, unknown
keys, immutable fields, malformed metadata and hash tampering.

A controlled workflow MUST exercise export verification, live binding, bundle
and policy admission, injection creation, target invocation, feedback and deletion
through the actual adapter, retaining native results and rejecting replay.
OpenAPI comparison MUST validate the exchanged requests/responses. Shared tests
MUST verify compact references, model key normalization and provider projections
in Go and Python. Existing database, MCP, filesystem and snapshot safeguards
MUST continue to pass. Controlled tests MUST be distinguished from live campaign
qualification.

Administrator route documentation and shared tool guidance MUST describe these
forms and link the identity files rather than duplicating release pins.
