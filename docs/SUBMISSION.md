# Offline bundle submission

The executable supports reusable submission and read-only validation:

```sh
operatorctl submit --config /absolute/config.yaml \
  --bundle ./scenario-bundle.json --capabilities ./capabilities.json \
  --artifacts ./bundle-artifacts --output ./runs/example
operatorctl validate --config /absolute/config.yaml --run ./runs/example
```

The output's parent MUST already exist; the output directory MUST be new. Omit
`--artifacts` for an empty or explicitly omitted inventory. The public capabilities
file MUST have its exact hash-named native companion alongside it, as defined by
[the bundle contract](../schemas/SCENARIO_BUNDLE_CONTRACT.md).

The administrator configuration MUST select an independently installed package:

```yaml
engine:
  image: intrusive/attack_harness:local
contract:
  directory: /absolute/installed/operator-contracts
  version: '0.1.0'
  digest: 'sha256:<publisher-provided-64-lowercase-hex-digest>'
```

Submission MUST verify the installed package, bundle schema and semantics,
authoring provenance, required capability references and exact artifact bytes.
Optional authoring gaps MUST appear in the receipt. Artifact sources MUST contain
exactly the declared direct regular files; links, directories, extra entries and
files on another device MUST fail. Unique artifact bytes MUST total at most 64 MiB.
A declared `schema_id` MUST resolve to the installed offline catalog and validate;
no schema or citation may trigger a network fetch. Without a declared schema,
artifact contents retain their original bytes, including JSON formatting.

The new private output contains `run.json`, `input/scenario-bundle.json`,
`input/capabilities.json`, the native companion, `input/validation.json`,
`input/provenance.json` and hash-named files under `input/artifacts/`.
Inputs MUST be flushed before `run.json` is published as the completion marker.
A failed write may leave an incomplete directory; it MUST NOT be treated as a run
or overwritten by another submission. Validation MUST reread and verify the input
bytes and independently installed package, rather than trusting saved status text.

Success emits one JSON receipt with `status: validated-offline`; errors emit no
success receipt. Receipts MUST fit within 4 MiB. Exit codes are 0 for success,
2 for argument errors and 1 for validation, storage or output failure.
These commands MUST NOT contact Docker, Interceptor, providers or release services,
create authoritative campaign state or grant permission to execute. Live target
compatibility and administrator policy MUST be checked separately at preparation
and admission. Editing reusable inputs requires a new submission.
