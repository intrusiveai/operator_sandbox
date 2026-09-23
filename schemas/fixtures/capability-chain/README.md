# Interceptor → public capabilities → ScenarioBundle

This fixture uses actual `BuildCapabilityManifest` output from Interceptor's
`examples/delivery-contract-environment`. Its source data is a synthetic example,
not a running customer target; its application digest is the example's zero digest.
It demonstrates serialization and reference interoperability, not runtime readiness.

- `interceptor-export.json`: byte-for-byte copy of Interceptor's committed
  `internal/adaptive/testdata/capability-delivery.json`.
- `native-digest-input.json`: exact Go JSON digest input with `digest: ""`, captured
  from the supported native type. Its hash equals the native export's `digest`.
- `public-capabilities.json`: deterministic allowlisted projection for the
  administrator-selected logical target `delivery-example`.
- `projection-canonical.json`: exact JCS-compatible bytes for the static projection.
- `submitted-bundle.json`: scenario submission using real references/source digest,
  intentionally omitting an exact projection pin so admission checks compatibility.

## Verify

From Operator, with `jsonschema` installed:

```sh
python3 schemas/validate_capability_fixtures.py
```

The runner validates both closed schemas, native/public digest preimages, projection
mapping, references, required/optional behavior, policy denial, compatible live
changes and exact pins. Mutation cases model live changes for admission tests;
they are not separately signed or captured native deployments. It also validates
the two existing illustrative bundle examples structurally.

## Recapture after reviewing an intentional native change

1. Make a disposable copy of Interceptor's current source. Preserve the original
   example; the native test seals its own temporary copy.
2. Copy `capture_native_test.go.txt` to `internal/adaptive/operator_capture_test.go`
   in that disposable checkout and run:

```sh
OPERATOR_NATIVE_PREIMAGE=/tmp/operator-native-digest-input.json go test ./internal/adaptive -run 'TestDeliveryExampleNativeFixtureAndVerification|TestCaptureOperatorNativeDigestInput' -count=1
```

3. Only after the test passes, copy that checkout's
   `internal/adaptive/testdata/capability-delivery.json` to `interceptor-export.json`
   and the captured preimage to `native-digest-input.json` in this fixture directory.
4. From Operator, explicitly regenerate the derived golden files and validate:

```sh
python3 schemas/generate_capability_fixtures.py
python3 schemas/validate_capability_fixtures.py
```

The native test proves the sealed and verified example generates the captured
export. Tests never regenerate expected public outputs implicitly. The fixture
canonicalizer supports ASCII property names and safe integer numbers only; these
fixtures meet that restriction. It is not a substitute for the full shared JCS
implementation or Unicode/number conformance suite. Production native verification
must use the typed native digest recipe, not this fixture's stored preimage.
