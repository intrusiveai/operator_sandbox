# Native Interceptor fixtures

Copied unchanged from `interceptor_sandbox/internal/adaptive/testdata` at commit
`567e6e0546a8f797c1673f90ba749e907b3dc56f`:

- `operation-request-v1alpha2.json`: native serializer golden bytes and raw body
  digest. Its legacy bind action is used only to check byte compatibility; Operator
  performs public binding through `/v1/attach`.
- `capability-delivery.json`: real native capability-export fixture used to construct
  attachment responses. These tests check routing identity agreement; full native
  capability integrity/projection remains an independent adapter gate.

Keep native and shared Operator/harness canonicalization rules separate. Do not
regenerate these fixtures using Operator's own encoder as the expected oracle.

## Checkpoint fixtures

`checkpoint-v1.json` and `checkpoint-legacy-v1.json` were generated using
Interceptor's `internal/snapshot` constructor, hash function, verifier and serializer
at the same commit above. They cover current campaign/description metadata and
legacy omission, HTML escaping, nanosecond timestamps and a native uint64 journal
sequence above the shared-contract safe-integer range.

The generator is retained as `native_checkpoint_fixture_test.go.txt`. To regenerate,
create a Go overlay JSON file mapping the absolute virtual path
`<interceptor-repo>/internal/snapshot/operator_fixture_test.go` to that generator's
absolute path. Then run from Interceptor:

```sh
OPERATOR_SNAPSHOT_FIXTURE_DIR=<operator-repo>/internal/interceptor/testdata \
  go test -overlay <overlay.json> ./internal/snapshot \
  -run '^TestOperatorCheckpointFixtures$' -count=1
```

The overlay adds only a test at build time; it does not edit Interceptor. The test
assigns a fixed checkpoint ID before recomputing/verifying the native hash so both
fixtures are reproducible. Operator tests must consume these fixtures unchanged.
