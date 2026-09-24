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
