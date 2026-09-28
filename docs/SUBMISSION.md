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

## Administrator target selectors and capability export

The CLI MUST accept `--environment DIR` as an administrator-owned directory with
`target-profile.json`, `capabilities.json` and the public export's hash-named native
companion. The directory MUST be owned by the service user or root and MUST NOT be
writable by other users. The profile MUST satisfy the existing private TargetProfile
file checks. This is Operator's selector directory, not an Interceptor Environment
Blueprint directory; no environment scripts or configuration are executed.

Alternatively, submission MUST accept `--target-profile FILE --capabilities FILE`.
`--environment` MUST be exclusive with both explicit selectors. Plain
`--capabilities FILE` MUST continue to use the installed target profile at preparation.
An explicitly selected profile MUST match the bundle target ID. Submission MUST
retain its absolute file path and canonical digest in `run.json`; validation MUST
recheck that private file and digest. Campaign preparation MUST use this selection
and bind it into the immutable start-input fingerprint. Changed profiles MUST
require a new submission. Bundle fields MUST NOT select profile paths.

```sh
# First create the native file using Interceptor's read-only capabilities command.
interceptor capabilities --environment ./interceptor-environment > ./native.json
operatorctl capabilities export --config /absolute/config.yaml \
  --target-profile /absolute/target-profile.json --native ./native.json \
  --output ./capabilities.json
operatorctl capabilities export --environment ./environment --output ./export.json
operatorctl submit --bundle ./scenario-bundle.json --environment ./environment \
  --output ./runs/example
```

Interceptor capability export MUST verify the installed contract and native manifest, then
produce only the existing allowlisted public projection. It MUST support
`--native FILE` with an explicit or installed target profile, or verify/re-export
`--capabilities FILE` with its companion. `--native` MUST be exclusive with
`--environment` and `--capabilities`. The native source remains host-side.

For `https/v1`, export MUST derive the public projection and its source companion
from the private profile, or verify/re-export an existing public export with its
companion. `--native` MUST be rejected with an HTTPS profile. See
[HTTPS targets](HTTPS_TARGETS.md) for the complete administrator workflow.

Publication MUST write and sync the companion before the new public file, sync
the parent directory and never overwrite a public export. An existing companion
MUST be a safe regular file with identical bytes. Failure MAY leave an incomplete
output; import MUST reject unverified content. Success MUST emit one bounded
`operator.dev/capability-export-receipt/v1alpha1` JSON object with target and digest
identities. Export MUST NOT attach to a target, resolve credentials, contact Docker
or confer execution permission. The output parent MUST already exist.
