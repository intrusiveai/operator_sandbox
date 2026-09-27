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
- Native request/response bytes, timing, profile binding and usage MUST be retained
  before a model receipt is returned. Private provider error text MUST NOT appear
  in guest replies or journal diagnostics.
- Exact duplicate operations MUST replay the committed result without another
  generation. Healthy target restores MUST preserve model charges and receipts.
- The provider callback MUST NOT control Docker termination. Administrative stop
  MUST cancel a blocked generation and start independent termination immediately.

Tests use a scripted provider with real launch validation and durable journals.
They cover frozen-policy rejection, usage settlement, duplicate replay across
restore, turn exhaustion, lost replies, missing usage, response-model mismatch,
input-token bound violations, oversized responses and independent cancellation.
The [host provider transports](MODEL_PROVIDERS.md) and [credential resolver](CREDENTIALS.md) now implement private profile loading and native transport dependencies. Service startup still needs their concrete wiring. Additional Go/Python codecs and live provider qualification remain integration work.
