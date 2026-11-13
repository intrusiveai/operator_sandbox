# Target model-response injection

Operator can authorize Interceptor to change a model response before the target
application receives it. This tests how the application handles altered text or
tool calls. It does not change Attack Harness's own model responses.

The advertised action is `action:injection:model_response`. Its `model_codecs`
identify application-facing APIs, even when the target routes them to a different
upstream provider. Select one codec explicitly in the private target profile:

```json
{
  "surface": "model_response",
  "target": {"model_codec": "openai_chat_completions"},
  "scopes": ["once"],
  "placements": ["replace"],
  "pointers": ["/choices/0/message/content"]
}
```

Add this object to `scopes.routes` alongside the permitted invocation operation.
This grants only the listed codec, scopes and pointers. A bundle can require the
advertised action, but cannot broaden the administrator's permissions. The codec
must also be advertised by the attached Interceptor. Standing injections require
explicit route permission; keeping them after an attempt also requires the existing
retained-injection permission.

Harness uses the existing `attempt_execute` setup and injection content:

```json
{
  "origin": "exploratory",
  "operation_id": "invoke",
  "json": {"prompt": "Inspect the support request."},
  "injection": {"json": "Controlled replacement text"},
  "setup": [{
    "surface": "model_response",
    "mode": "simulation",
    "scope": "once",
    "selector": {"model_codec": "openai_chat_completions", "call_ordinal": "first"},
    "placement": {"operation": "replace", "pointer": "/choices/0/message/content"}
  }]
}
```

Use the target's documented input fields and returned evidence handles. Optional
request-body predicates inspect the original application request; `turn_id` must
come from an observed turn. Placement pointers address the whole response JSON.
A JSON payload can deliberately contain malformed tool names or argument strings;
it must still satisfy JSON size, placement and authorization checks. Streams,
embeddings, raw invalid JSON, and HTTP status/header changes are unsupported.
Missing pointers or incompatible placement types remain unapplied native results;
they are not silently repaired or retried.

Diagnostic/oracle-assisted feedback distinguishes mutation (`applied`) from response
writing (`response_written` or `write_failed`). A successful write does not establish
SDK acceptance or tool execution. Inspect permitted target/oracle observations for
that evidence. Black-box feedback withholds these delivery facts. Native evidence
retains original and delivered response references separately, with integrity
checks; it does not expose private model bodies to Harness.

The approved Interceptor identity is in
[`internal/interceptor/contract.json`](../internal/interceptor/contract.json).
Harness must consume the matching interface approved in
[`release/contract-lock.json`](../release/contract-lock.json), including its shared
validators, selected-surface projections and deferred tool guidance. Detailed
instructions remain in on-demand guidance rather than recurring tool descriptions.
