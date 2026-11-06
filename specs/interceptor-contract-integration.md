# Interceptor OpenAPI compatibility

## Required result

Operator MUST approve an Interceptor integration by comparing the selected OpenAPI
contract with its implementation. It MUST NOT infer compatibility from a successful
campaign, matching product releases, or an Interceptor executable/archive hash.
During campaign preparation it MUST compare both the running Interceptor's contract
version and the SHA-256 of its contract bytes with that approved identity before
recording attachment intent or dispatching target operations.

## Contract ownership and source

Interceptor owns the OpenAPI 3.1.0 JSON document at
`internal/integrationcontract/contract.json` in its repository. Implementers MUST
consult that document together with its `specs/interceptor-operator-contract.md`,
`docs/local-api.md`, discovery handler and operation handlers at the selected
revision. Conflicts between the document and handlers MUST be resolved before
approval; Operator MUST NOT invent permissive replacements for upstream schemas.

Operator MUST maintain:

- `internal/interceptor/openapi.json`: an exact byte copy of the selected document.
- `internal/interceptor/contract.json`: only `interceptor_contract_version` and
  `interceptor_contract_sha256`. The hash MUST be 64 lowercase hexadecimal
  characters representing SHA-256 of the entire file, including whitespace and
  final newline. It MUST NOT include a `sha256:` prefix or hash normalized JSON.
- `release/interceptor-contract-source.json`: fixed upstream repository/path,
  user-selected ref and resolved immutable commit, for retrieval and audit.

The identity JSON MUST be embedded in the executable and be the single source for
runtime admission, `operatorctl version`, and packaged release metadata. The full
OpenAPI document MUST remain a build-time reference, not a Harness input or a
runtime negotiation channel. Old retained records and installed release manifests
without a hash MUST remain readable; a newly built release MUST include the hash.

## Explicit adoption and release verification

`python3 scripts/interceptor_contract.py adopt --ref <tag-or-commit>` MUST resolve
an explicitly selected upstream ref, retrieve the bounded document from that
immutable commit, and compare it before writing any approval files. Comparison
failure MUST leave the prior approval unchanged. The exact document, provenance
and pin MUST be committed together; an interrupted partial write MUST fail the
next verification rather than silently approve inconsistent files.

`python3 scripts/interceptor_contract.py verify --fetch` MUST retrieve that same
immutable source, compare its bytes to the approved copy and pin, and run the
implementation comparison. Verification MUST NOT rewrite source files or follow
a moving branch to a new revision. Offline verification MAY use the committed
copy. Builds MUST reject inconsistent document/pin bytes.

The comparison MUST cover:

- Public route/method and native operation inventories against actual client
  route literals and its operation registry, including lifecycle and evidence.
- Actual Go JSON field names and wire types against OpenAPI components, including
  capability decoding. Explicit routing projections and opaque application JSON
  MUST be identified rather than mistaken for complete typed decoding.
- Production request encoding against request schemas and operation-specific
  success responses against the documented schema and Operator decoders.
- Attachment, status, stop/restore, snapshots, closure, operation reconciliation,
  artifact/attempt/injection handling, observations and evidence transfer.
- Supported running/completed/unknown operation records, including legitimate
  empty pending responses; no pending result may become execution success.
- Negative tests for changed routes, fields, types, required values, mismatched
  identities and rejected adoption. Fixtures MUST exercise actual implementation
  paths; generating a request solely from the schema is not implementation proof.

This is a bounded structural and consumer-behavior comparison, not a proof of
all possible server behavior. Existing lifecycle, authorization, timeout and
integrity tests MUST remain separate safeguards. Campaign success MUST NOT
substitute for any contract comparison check.

The candidate workflow MUST run an `interceptor-contract` job before release
packaging/publication and retain a source-bound comparison result. It MUST NOT
install Docker, retrieve an Interceptor executable, pull a Harness image or run a
campaign for this gate. The result MUST identify Operator source, approved
contract, retrieval provenance, checks and workflow run/attempt. Stable promotion
MUST require a successful trusted candidate workflow and its matching unexpired
comparison evidence. Packaging MUST verify the same embedded contract identity on
all four native platforms. Promotion MUST preserve the already-built bytes.

Optional campaign regression tooling MAY accept explicitly supplied test builds
and retain their identities as evidence, but MUST NOT be a compatibility gate or
force an Operator pin change when Interceptor rebuilds unchanged contract bytes.

## Runtime admission and diagnostics

Operator MUST send `POST /v1/contract-version` with `{}` using its existing bounded
local client. It MUST require a successful response containing a well-formed exact
version and hash and compare both with the embedded pin. Missing endpoint, malformed
response, mismatch, timeout or cancellation MUST prevent attachment. There MUST
be no bypass, administrator version override, learned pin, CLI fallback, registry
lookup or product-source-commit restriction.

The same check MUST guard recovery and explicit late-evidence collection before
claiming new native work. A failed check MUST leave dispatch unclaimed, preserve
local evidence and never resume a campaign.

Preparation diagnostics MUST retain bounded expected/observed versions and hashes
when valid, reason and observed HTTP status. Missing/malformed/untrusted fields
MUST NOT leak raw response content. Existing CLI, start records, journal, inspection
and export paths MUST preserve these facts. Successful compatibility events MUST
record both equal hashes and the check timestamp using explicit export fields.

## Verification and documentation

Tests MUST demonstrate that version-only equality is insufficient, that a changed
hash fails before attachment, and that an Interceptor rebuild exposing unchanged
version/hash does not require Operator changes. A representative controlled HTTP
workflow MUST exercise actual request builders, response decoding, state binding
and evidence transfer. No paid provider or cloud write is required.

The release guide MUST describe selection, adoption, review, CI verification and
promotion. Runtime documentation MUST explain compatibility failures and show how
to inspect the expected identity. The Operator–Attack Harness contract identity
MUST remain independent and unchanged unless its own interface changes.
