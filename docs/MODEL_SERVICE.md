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
  host-selected diagnostic MUST be retained instead. Selected provider error fields
  MUST be bounded and credential-redacted before retention. Raw error bodies and
  unrestricted provider text MUST NOT appear in guest replies or journal diagnostics.
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

For HTTP rejections, diagnostics MUST retain `provider_message`, `provider_type`,
`provider_code` and `provider_parameter` when supplied. The original `message`
MUST remain Operator's fixed explanation; selected provider text MUST remain
separately labeled. Unknown provider codes MUST be accepted as bounded text.
Numeric codes MUST be stored as their JSON number text. These are host evidence
fields, not additions to the Harness protocol or changes to retry/effect semantics.

The parser MUST support the following advertised routes:

| Routes | Selected error fields |
| --- | --- |
| OpenAI Chat/Responses, Azure OpenAI Chat/Responses, LiteLLM | `error.message`, `error.type`, `error.code`, `error.param` (or `parameter`/`target`); compatible string `error`/`detail` can supply the message. |
| Anthropic Messages | Nested `error.message` and `error.type`; code/parameter only if provided, never inferred from the type. |
| Bedrock Converse | Top-level `message`/`Message`, `type`/`__type`, `code`/`Code`, and parameter if present; the designated `X-Amzn-Errortype` header supplies a missing type, including when the body cannot be read. |
| Gemini Developer API and Vertex | Nested `error.message`, numeric `error.code`, `error.status` as type, and `param`/`parameter`/`target` or the joined `field` names from `google.rpc.BadRequest.fieldViolations`. Other detail objects MUST NOT be retained. |

Error-body reads MUST obey the smaller of the configured response ceiling and
16 KiB. Parsing MUST accept JSON and Bedrock's `application/x-amz-json-1.1`, reject
duplicate keys, invalid UTF-8 and incomplete/invalid JSON, and retain no body text
on failure. Content encoding other than identity MUST be rejected. Independently
observed HTTP status and safe request ID MUST survive body failures.

After redaction, message MUST be bounded to 1,024 UTF-8 bytes, type/code to 128
bytes each, and parameter to 256 bytes. Overlong selected strings MUST retain only
a complete UTF-8 prefix and record `truncated`. CR/LF/tab MUST become spaces;
remaining control, Unicode format and line/paragraph separator characters MUST
cause omission with `unsafe_control`. Redaction MUST precede truncation so a
credential prefix cannot escape at a size boundary.

Redaction MUST remove the actual API key/bearer token, selected credential-bearing
request header values, and Bedrock access key, secret key, session token and
signature. These values MUST remain request-local. Exact raw, URL-encoded and
base64 forms MUST be replaced with `[REDACTED]`. Recognizable key/JWT tokens,
bearer/basic values, credential assignments and HTTP(S) URLs MUST also be removed.
A wholly redacted field MUST be omitted with `credential_redacted`; partial
redaction MUST retain visible replacement markers. Selected provider text remains
untrusted provider content and MUST NOT be forwarded to the Harness as instructions.
Console values MUST be quoted; Markdown reports MUST render them as code, not HTML
or links. No complete request/response headers MAY be copied into diagnostics.

`omissions` MUST identify each missing/shortened selected field (`message`, `type`,
`code`, `parameter`, and request ID) and its reason: `not_provided`,
`unsupported_value`, `truncated`, `unsafe_control`, `body_size_limit`,
`unsupported_content_type`, `unsupported_encoding`, `body_read_failed`,
`invalid_json`, `unrecognized_format`, or `credential_redacted`. Absence MUST NOT
be presented as a provider-confirmed empty value. Existing records without this
optional object MUST remain readable. Request IDs MUST come only from the
provider's designated request-ID header and match UUID or `req_` followed by
16–64 hex digits; unsupported formats MUST be explicitly marked as omitted.

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
