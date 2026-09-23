# Shared validation foundation

This is the first implementation stage of `operator-contracts`. It validates the
12 schemas currently listed in [the catalog](../schemas/catalog.json) in Go and
Python. It is a development library, not the published `0.1.0` contract package.
The authoritative design remains [SHARED_CONTRACT.md](../schemas/SHARED_CONTRACT.md).

## Implemented

- Strict UTF-8 JSON decoding with duplicate-key, trailing-value, malformed Unicode,
  unsafe-number and nesting checks before structural validation.
- Caller-selected byte ceilings, with constants for 4 MiB ordinary messages and
  64 KiB control messages. Callers must bound transport reads before constructing
  the byte buffer passed to these functions.
- Offline Draft 2020-12 validation against an explicit installed schema catalog.
  Missing resources, unknown schema IDs and references outside the catalog fail.
- Go-embedded schemas and generated Go/Python schema-ID constants.
- One set of 151 accepted/rejected byte vectors used by both language runners,
  plus tests for unavailable external references and invalid catalog inventories.

Schemas use portable `\xHH` regex escapes for control characters. Constrained
single-line fields also reject CR/LF explicitly, avoiding differences in how
regex engines interpret a final `$` anchor. IDs, digests and path fields therefore
reject a trailing newline in both languages.

## Development

Use Go 1.26 and Python 3.12 or later. This stage was tested with Go 1.26.5 and
Python 3.14.6 on macOS ARM64; it does not qualify container/runtime support.

```sh
make setup
make test
```

`make setup` creates `.venv` and downloads dependencies. `make test` checks generated
schema IDs, runs both byte-vector suites, and runs the existing cleanup, feedback
and capability fixture checks. Python runtime dependencies are pinned in
`python/requirements.lock`; Go dependencies are recorded in `go.mod`/`go.sum`.

After changing `schemas/catalog.json`, run `make generate`. Both languages use
the catalog as the authority for schema identity; generated constants are a
convenience, not a second registry.

## Go usage

```go
catalog, err := contracts.LoadCatalog(schemas.Files)
if err != nil {
    return err
}
value, err := catalog.Validate(contracts.EngineObservationReadRequestSchema,
    rawBytes, contracts.OrdinaryLimit)
```

Import `contracts` and `schemas` from `github.com/intrusive-ai/operator-sandbox`.
Numbers in returned objects are `json.Number`; validate before converting them
to application types. Public errors are bounded categories, without submitted
values or native validator diagnostics.

## Python usage

Until package publication, run with `PYTHONPATH=contracts/python` and explicitly
provide the trusted local schema directory:

```python
from pathlib import Path
from operator_contracts import Catalog, ORDINARY_LIMIT
from operator_contracts.schema_ids import ENGINE_OBSERVATION_READ_REQUEST_SCHEMA

catalog = Catalog(Path("schemas"))
value = catalog.validate(ENGINE_OBSERVATION_READ_REQUEST_SCHEMA, raw_bytes, ORDINARY_LIMIT)
```

Decoded numbers retain exact decimal values (`Decimal`, with zero normalized to
integer zero). This prevents near-integer fractions from satisfying integer
schemas through floating-point rounding. Both decoders reject nonfinite values,
nonzero values that underflow binary64, and integer-valued binary64 numbers beyond
the safe integer range. Neither decoder performs canonicalization. Do not serialize
these values with a generic JSON encoder and assume the result is `jcs-v1`.

## Remaining stages

Structural validation is one gate. Receipt lookup, operation identity, digests,
cross-field constraints, authorization, profile filtering and lifecycle rules
still require semantic validators. Schema `format` annotations are not a substitute
for those checks. This library is not yet sufficient to admit campaign execution.

Before publishing `0.1.0`, implement the remaining startup/envelope/control,
manifest, artifact, model, conclusion and stop schemas; the operation registry;
canonicalization and identity helpers; typed message bindings; the immutable
package manifest/digest; and the full shared conformance suite. FIFO/spool codecs
and fake-broker/fake-harness integration tests are also outstanding. No changes to
Interceptor's native API are needed for this foundation.
