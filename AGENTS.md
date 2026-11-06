# Repository instructions

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
