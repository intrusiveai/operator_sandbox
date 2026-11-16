# Model conversation relay

The interface approved in the [release contract lock](../release/contract-lock.json) defines a provider-independent relay over the existing ordinary
transport. The approved identity is the version and SHA-256 of exact
[AsyncAPI bytes](asyncapi.json), recorded in [the lock](../release/contract-lock.json).
Both components MUST match that identity before execution.

## Request and result

`engine.model_generate` MUST accept exactly these body fields:

```json
{"purpose":"generation","conversation":[{"role":"user","content":"Inspect the admitted target."}]}
```

- `purpose` MUST be `generation` or `compaction`.
- `conversation` MUST be a JSON array. Its provider-native contents are opaque to
  shared schema validation. JSON validity, duplicate-key rejection, nesting and
  whole-message transport limits still apply.
- Requests MUST NOT carry model, endpoint, authentication, profile, tool declarations,
  provider options, or output allowances as top-level configuration.

Successful results MUST contain `receipt_id`, opaque `response` JSON, and
`context_budget`. Existing envelope identities MUST correlate the request/result.
The response does not authorize a tool or establish objective success.

## Configuration ownership

Operator MUST construct each provider request using the frozen host profile,
trusted prompt and installed tool projection. It MUST place conversation only in
that provider's history field; it MUST NOT merge entries into top-level options.
Endpoint, credentials, selected model, output ceiling, storage/streaming settings,
reasoning options and tool capabilities MUST remain host-controlled. A compaction
request MUST have no available tools. Both purposes consume normal model budgets.

Startup `model` MUST expose `codec_id` (the conversation format), informational
`model_id`, supported features, logical tool selection and `context_limits`.
Provider configuration and private profile identity belong to Operator's retained
host state, not Harness-generated model calls.

## Provider interpretation and execution

Shared Go/Python validators MUST validate the relay and host accounting, not an
exhaustive native provider request or response schema. Additional metadata,
annotations and informational model labels MUST NOT by themselves reject a response
or require a new interface identity.

Operator adapters MUST validate only the fields needed for accounting and bounded
execution. Missing/malformed usage MUST NOT be represented as zero usage. Invalid
consumed fields MUST produce specific safe diagnostics. New provider fields MUST
remain opaque. Harness adapters MUST interpret completion status and tool calls,
retain opaque native continuation/reasoning content, and validate tool arguments
against the actual Operator tool contract before dispatch. Unknown completion
states MUST fail explicitly rather than guessing whether tool execution is safe.

The same operation ID MUST NOT invoke the provider twice. Operator MUST durably
retain intent before calling and the result before returning its receipt. Budget,
deadline, evidence-visibility and no-replay semantics remain mandatory. Invalid
provider responses MUST follow [private evidence and diagnostic retention](../docs/MODEL_SERVICE.md).

## Conformance

Go and Python MUST consume the shared `model-relay.json` examples. Controlled
product tests MUST cover all supported provider formats, unknown metadata, opaque
continuations, host-owned settings, no-tools compaction, usage accounting, rejected
tool correction, evidence inspection, accepted finish and cleanup. Provider tests
belong to the product adapters; interface conformance does not imply live-provider
qualification.
