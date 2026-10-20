# Simple campaign execution and troubleshooting

A Harness can send an ordinary target input directly, read the returned permitted
observations, and finish with a short assessment. Operator computes input identity,
retains bytes before dispatch, and assigns attempt/lineage metadata. The model
needs no artifact digest, thread, generation or prior hypothesis registration.
Large/reusable inputs may still use a committed artifact receipt. Optional injection,
snapshot, restore and historical-record tools retain their existing safeguards.

```json
{"operation_id":"invoke","input":{"encoding":"json","value":{"prompt":"Reply exactly QA_CONFIG_OK. Do not call tools."}}}
```

The result's `receipt_id` identifies an executed attempt; it is not interchangeable
with an artifact receipt, historical-record receipt, or a rejected local call ID.
Read evidence using `observation_read`, its returned entry ID, offset and maximum
bytes. Omitted observation selection uses all feedback allowed by the current
campaign profile. This does not expand visibility permissions.

```json
{"finish_reason":"objectives-addressed","assessment":{"summary":"Observed the target response.","objectives":[{"objective_id":"configuration","outcome":"supported","summary":"The permitted response contained the expected marker.","attempt_receipt_refs":["actual-execution-receipt"]}]}}
```

Use only actual submitted objective IDs and returned execution receipts. A supported
outcome needs evidence. For no attempts, use an honest `not-tested` or `inconclusive`
outcome and empty references. If no assessment can be constructed, finish with an
explicit unavailable reason. No synthetic history is added. Accepted finish closes
admission; its pending acknowledgement is **not** proof that cleanup finished.
Inspect the final campaign receipt/report for cleanup and evidence availability.

## Correctable failures and stopping reasons

Argument errors identify the rule and field. For example, missing direct/artifact
input reports `/input`; input above 32,768 decoded bytes directs the caller to
artifact publication. A corrected submission uses a new operation ID. Duplicate
delivery of the original operation reuses its result; changed content under that
ID conflicts. A rejection before dispatch has no execution receipt.

When Harness reports invalid-call exhaustion, foreground and supervised execution
retain the same bounded cause. Representative output after confirmed cleanup is:

```text
Campaign execution failed during Harness arguments (execution_failed):
Harness stopped: invalid-call-limit (observed 5; configured limit 5). Tool arguments do not match the required structure; check the indicated field. Field: /input.
Operation: local-report
Tool: attempt_execute
No target attempt was dispatched.
Assessment accepted: false
Cleanup completed: true
Call: call-5
Execution will not resume.
```

Unsafe submitted diagnostic prose is omitted and that omission is indicated.
Host dispatch counts and final cleanup facts are independent of Harness claims.
Earlier corrected rejections remain history; they do not turn a later successful
campaign into a failure. An explicit Harness stopping condition remains a failure
even if it subsequently finishes honestly and exits normally.

Use separate invocations to retrieve retained diagnostics and produce evidence:

```sh
operatorctl inspect --state-root /path/to/operator-state --campaign CAMPAIGN_ID
operatorctl report --state-root /path/to/operator-state --campaign CAMPAIGN_ID
operatorctl export --state-root /path/to/operator-state --campaign CAMPAIGN_ID \
  --output /path/to/new-export-directory
```

Inspection exposes `workflow_diagnostic` and the saved start completion. Reports
include workflow diagnostics, accepted assessments, actual dispatch/usage records,
and final closure/cleanup. Export includes that projection and permitted evidence.
A cleanup or completion-write failure does not replace the original stopping cause.
If saving completion failed, the CLI explicitly reports that secondary failure.

## Local regression workflow

After `make setup`, `make test` includes a real Python process talking to the Go
service through an atomic file spool. It executes direct input against a controlled
local target, reads observations, finishes, confirms duplicate delivery, and runs
separate inspect/report/export commands. It also exercises correction and the
zero-attempt invalid-call-limit case. To run just these cases:

```sh
OPERATOR_CONTROLLED_PEER_TEST=1 go test ./internal/preparation \
  -run '^TestSimplePythonSpoolAndSeparateCLI$' -v -count=1
```

This test peer is not the production Attack Harness. Matching production Harness
integration and native/runtime qualification remain distinct gates; see the
[Harness integration](HARNESS_INTEGRATION.md).
