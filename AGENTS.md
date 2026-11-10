# Repository instructions

## Preparation diagnostics

Error producers MUST expose safe, actionable failure facts through
`DiagnosticMessage() string` when those facts can be reported. Fixed public
descriptions use `internal/diagnostictext.New`; shared contract code keeps its
equivalent self-contained for distribution. Do not pass arbitrary error strings,
credentials, response bodies, or submitted values to these constructors.
Preparation forwards these diagnostics with common redaction and bounds. Do not
add a central list of recognized product errors or replace a known cause with a
generic recovery message. Keep primary and cleanup failures separate, preserve
`errors.Is`/`errors.As`, and test saved completion plus independent CLI inspection.

## Interceptor OpenAPI contract adoption

`internal/interceptor/contract.json` is Operator's approved Interceptor contract
version and SHA-256 of the exact OpenAPI bytes. `internal/interceptor/openapi.json`
contains those bytes. `release/interceptor-contract-source.json` records the
administrator-selected release tag/revision and resolved source commit used to
retrieve them. Source provenance is not a runtime product-build restriction.

1. Use the user-selected Interceptor revision. Run
   `python3 scripts/interceptor_contract.py adopt --ref <tag-or-commit>`.
   The command retrieves the OpenAPI file and compares it with Operator's actual
   client types, operation inventory, request builders, response decoders and
   adapter exchanges before saving an approval. Do not manually mark a failed
   comparison as passed or change a schema locally to hide an upstream conflict.
2. Review the contract changes and adjust the integration and tests as needed.
   If Interceptor's documentation and implementation conflict, ask the user for
   resolution. Keep a dependent PR draft until the selected contract is correct
   and comparison passes.
3. Commit the exact OpenAPI file, identity JSON and source selection together.
   Never substitute the hash of a release archive, executable or normalized JSON.
   A formatting-only contract change still changes the approved hash.
4. Run `python3 scripts/interceptor_contract.py verify --fetch` and the affected
   tests. Candidate CI repeats the comparison against that immutable contract
   source; it never automatically updates committed approvals.
5. Do not refresh the pin merely because Interceptor publishes another build.
   The same contract bytes/version remain compatible across product revisions.
   Runtime checks require both values from `/v1/contract-version` to match.

Campaign tests exercise behavior but do not establish contract compatibility.
The release process does not require an Interceptor executable/archive or Harness
image for its OpenAPI comparison. Follow the releases skill and
[candidate release guide](.github/maintainers/CANDIDATE_RELEASES.md). This procedure
does not authorize changes or publication in another repository.

## Operator–Attack Harness contract adoption

`schemas/asyncapi.json` is the self-contained interface. `release/contract-lock.json`
approves its `info.version` and SHA-256 of exact bytes. Existing package version/digest
fields carry this pair, not an implementation-package or source-revision identity.

For interface changes, update authoring schemas/registry/semantic rules, generate
and review the document, then explicitly approve it with
`.venv/bin/python scripts/generate_asyncapi.py --approve`. Commit the document and
lock together. `make check-generated` never refreshes approvals. Run the shared
and affected stateful tests; `make test-contract` provides a focused controlled
conformance workflow. Follow [contract conformance](docs/CONTRACT_CONFORMANCE.md).
Implementation-only changes MUST NOT force a new counterpart source pin. Package
integrity, build provenance and optional integration-test revisions are separate.
Do not claim document validation alone proves functional conformance, or publish
another product's releases as part of this procedure.
