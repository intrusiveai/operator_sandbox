# Host native model-provider transports

The private `model.profile_file` selected by host configuration MUST describe one
fixed provider route. Its canonical SHA-256 identity binds the public model policy;
its endpoint and credential configuration MUST NOT enter guest files or campaign
journals. Profile loading MUST reject unknown fields, bad identities, unsupported
provider/codec/authentication combinations and out-of-bounds limits.

```json
{
  "api_version": "operator.dev/model-provider-profile/v1alpha1",
  "id": "campaign-model",
  "provider": "openai-chat",
  "codec_id": "openai-chat-text-tools-v1",
  "model": "administrator-selected-model",
  "endpoint": "https://api.openai.com/v1/chat/completions",
  "authentication": "secret-store",
  "credential_id": "model-key",
  "maximum_prompt_tokens": 32768,
  "maximum_completion_tokens": 4096,
  "maximum_response_bytes": 1048576
}
```

`maximum_prompt_tokens` MUST be a qualified upper bound, including message/tool
framing; it is used for conservative campaign reservations. Profiles MUST set
positive token bounds and a response-byte limit at most 4 MiB. Secrets MUST resolve
through the [host credential resolver](CREDENTIALS.md).

| Provider | Native codec | Authentication |
| --- | --- | --- |
| `openai-chat` | `openai-chat-text-tools-v1` | Secret-store bearer key |
| `openai-responses` | `openai-responses-text-tools-v1` | Secret-store bearer key |
| `anthropic-messages` | `anthropic-messages-text-tools-v1` | Secret-store `X-Api-Key`; explicit `anthropic_version` date |
| `gemini-api` | `gemini-text-tools-v1` | Secret-store `X-Goog-Api-Key` |
| `vertex-gemini` | `gemini-text-tools-v1` | Google workload identity |
| `azure-openai` | Chat or Responses codec | Secret-store `Api-Key` or Azure workload identity |
| `litellm` | Chat or Responses codec | Secret-store bearer key |
| `bedrock-converse` | `bedrock-converse-text-tools-v1` | AWS workload identity and explicit `region` |

HTTP profiles MUST supply the complete HTTPS request endpoint, including the
selected deployment/model path where required. Its suffix MUST match the native
operation; Gemini/Vertex paths MUST bind the exact configured model. Only Azure MAY include a query,
consisting of exactly one `api-version`. Profiles MUST NOT contain user-info,
fragments, credential query parameters or guest-controlled path placeholders.
Bedrock MUST use the SDK's native regional endpoint, with no configured endpoint
overrides. Generation MUST disable retries. HTTP MUST also
disable redirects, ambient proxies, compression and connection reuse; requests
MUST be non-rewindable. All response streams MUST be bounded before decoding.

Generation MUST validate the selected model, generation ceiling and non-streaming/
non-persistent controls before contact. Shared codec validation MUST independently
bind prompt, tools, native history, allowed fields and result semantics. The
transport MUST preserve exact HTTP native request/result bytes for every route,
including Bedrock. Bedrock MUST use the AWS SDK workload identity, regional
endpoint resolver and SigV4 signer without decoding/reconstructing native bodies. Cancellation, HTTP errors, malformed or oversized
results MUST produce a sanitized uncertain failure and MUST NOT trigger a retry.

Transport implementations and synthetic TLS/signing tests cover all listed families.
All five native codec families have shared Go/Python semantic validators.
Executable profile/startup integration and the Attack Harness loop MUST be
completed before campaign admission can advertise these routes. Synthetic
transport/contract coverage alone does not qualify live providers,
model versions, authentication or reasoning continuation.

Native API references: [OpenAI Responses](https://developers.openai.com/api/reference/resources/responses),
[Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create),
[Gemini generateContent](https://ai.google.dev/api/generate-content), and
[Bedrock Converse](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html).
