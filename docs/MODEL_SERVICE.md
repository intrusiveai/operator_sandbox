# Campaign model relay

The campaign service implements `engine.model_generate` with the shared
`openai-chat-text-tools-v1` and `anthropic-messages-text-tools-v1` codecs. The launcher supplies a trusted `ModelConfig`;
this service does not discover endpoints or resolve production credentials.

- The configuration MUST bind a provider to the manifest's model-profile digest,
  an installed native tool projection and a qualified maximum input-token count.
  The provider MUST issue exactly one non-streaming request without retries,
  honor cancellation, bound returned bytes, and exclude private transport headers
  and credentials from returned native response bytes and errors exposed to guests.
- Admission MUST derive model policy from verified EngineContext, exact prompt
  bytes and the native tools digest. Requests MUST match that frozen policy.
- A new generation MUST reserve the profile input-token ceiling plus requested
  maximum completion tokens, and charge one model turn, before provider contact.
  Both campaign and harness model-turn limits MUST apply. Known usage MUST settle
  the reservation; missing usage, cancellation, transport ambiguity or invalid
  native responses MUST retain it and terminate execution without retry.
  Shared codec accounting MUST derive native token fields; Anthropic input MUST
  include cache-creation and cache-read tokens in addition to uncached input.
- A response exceeding the configured per-response tool-call limit MUST be
  rejected as a whole after accounting for its generation. It MUST enter bounded
  finalization; no prefix of that batch may execute.
- Native request bytes, validated successful response bytes, timing, profile binding
  and usage MUST be retained before a model receipt is returned. Failed, malformed
  or contract-invalid provider response bodies MUST NOT be journaled. Their bounded
  host-selected diagnostic MUST be retained instead; arbitrary provider error text
  MUST NOT appear in guest replies or journal diagnostics.
- Exact duplicate operations MUST replay the committed result without another
  generation. Healthy target restores MUST preserve model charges and receipts.
- The provider callback MUST NOT control Docker termination. Administrative stop
  MUST cancel a blocked generation and start independent termination immediately.

Tests use a scripted provider with real launch validation and durable journals.
They cover frozen-policy rejection, usage settlement, duplicate replay across
restore, turn exhaustion, lost replies, missing usage, response-model mismatch,
input-token bound violations, oversized responses and independent cancellation.
The [host provider transports](MODEL_PROVIDERS.md) and [credential resolver](CREDENTIALS.md) now implement private profile loading and native transport dependencies. Installed service startup wires both dependencies. All five native Go/Python codec
families are implemented; live provider qualification remains outstanding.

## Model failure diagnostics

Provider failures MUST retain a host-only diagnostic in `model.exchange` metadata,
with the operation ID, failure category, provider/model when known, and observed
HTTP status. A missing HTTP status MUST mean none was observed, not an inferred
provider rejection. The native HTTP client MUST return selected response facts on
success too, so later contract-validation failures can retain HTTP status/request ID.

Request IDs MUST come only from the provider's designated request-ID header and
match the supported bounded formats (UUID or `req_` followed by 16–64 hex digits).
Unrecognized formats MUST be omitted. Provider codes MUST come from a finite
allowlist. Arbitrary provider messages MUST be omitted; `message` MUST be a fixed
host-authored explanation selected by `reason`. Error-body parsing MUST enforce
both the configured response limit and a 16 KiB ceiling, reject incomplete/invalid
JSON, and retain no body text or unrestricted headers. Non-JSON, oversized and
truncated errors MUST still retain an observed status and safe request ID.

Categories MUST distinguish credential preparation, local request validation,
connection, TLS, timeout/cancellation, HTTP rejection, response reading, response
validation, and unknown failures. These diagnostic facts MUST NOT alter the
existing guest error/effect classification, validation, termination or no-replay
rules. An HTTP rejection MUST NOT be relabeled as a connection failure because
its optional error body could not be read.

The first diagnostic MUST survive in memory independently of journal recording
and cleanup failures. The worker MUST include it in the failed start completion
under `diagnostic.model` and preserve `execution_failed`. Foreground and supervised
workers MUST use the same retained diagnostic and safe renderer. Secondary cleanup
or completion-publication failures MUST NOT replace the model failure. Earlier
records without these optional fields MUST remain readable.

`inspect` and `campaign status` MUST expose a valid journal diagnostic as
`model_diagnostic`; `--run` also exposes the retained start completion.
[Reports and exports](REPORTING.md) MUST carry the same selected facts and keep
reserved allowance separate from confirmed token usage.
