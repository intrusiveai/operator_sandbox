# Native model-response evidence fixtures

Captured from Interceptor source `e90d3140795dcaa1416a080559423dc06cd1f84f`
using `../model_response_fixture_test.go.txt` in its `cmd/interceptor` package.
Set `OPERATOR_EVIDENCE_FIXTURE_DIR` to this directory and
`OPERATOR_MODEL_RESPONSE_CAPABILITY` to the synthetic capability export under
`schemas/fixtures/model-response-injection/interceptor-export.json`.

The actual injection manager records an applied mutation, a completed response
write, and ordinal/consumption state. A synthetic model exchange references distinct
original and delivered blobs. The native exporter produces complete, partial and
restored-session archives including the source checkpoint/journal. No model, target
container or cloud account is contacted. Operator verifies the unchanged exports
and rejects modified delivered bytes. These fixtures do not establish live SDK or
tool execution success.
