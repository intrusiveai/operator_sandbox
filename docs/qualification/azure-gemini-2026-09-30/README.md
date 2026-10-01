# Azure OpenAI and Gemini qualification — 2026-09-30

All five selected production host-adapter conversations passed on macOS ARM64.
Evidence timestamps use UTC (2026-10-01); the record uses the runner's local date.
These are host-adapter checks, not full Docker campaigns or runtime qualification.

## Passed conversations

Each probe completed text, one client function call, and text after an inert
simulated tool result. Checks covered native schema validation, exact model
binding, token accounting, conversation/tool correlation and pre-canceled
execution without dispatch. No target or actual snapshot operation ran.

| Route | Model | Authentication | Input/output tokens | Evidence |
|---|---|---|---|---|
| Azure Chat | gpt-5-mini, 2025-08-07 | Explicit Azure CLI identity | 985 / 180 | [CLI](azure-chat-cli-corrected.jsonl) |
| Azure Chat | gpt-5-mini, 2025-08-07 | API key from Azure Key Vault, resolved with Azure CLI | 983 / 235 | [Key](azure-chat-key-corrected.jsonl) |
| Azure Responses | gpt-5-mini, 2025-08-07 | Explicit Azure CLI identity | 942 / 306 | [CLI](azure-responses-cli-corrected.jsonl) |
| Azure Responses | gpt-5-mini, 2025-08-07 | API key from Azure Key Vault, resolved with Azure CLI | 805 / 173 | [Key](azure-responses-key-corrected.jsonl) |
| Gemini Developer API | gemini-3.1-flash-lite | API key from Google Secret Manager, resolved with local ADC | 1,126 / 28 | [Gemini](gemini-api-key-corrected.jsonl) |

Azure used the administrator's deployment and v1 Chat/Responses endpoints. Requests
used its exact deployment name; the response allowlist explicitly included the
permitted model aliases and, for Responses, the deployment echo. Private endpoint,
deployment and secret locators are excluded here. Responses selected low reasoning
effort; Chat used model defaults. Gemini selected `thinkingLevel:MINIMAL`.
All key-bearing secrets were read only. No credentials, IAM policies, billing,
service settings or secret versions were changed.

## Call budget and failures

Each probe allowed three generations, at most 1,024 output tokens per generation,
and a 120-second timeout. There were no automatic retries. Exactly **23 generation
invocations** occurred: five initial one-call failures, three separately authorized
one-call diagnostics, and fifteen calls in the corrected conversations. The final
18 calls match the additional authorization. A separate read-only Gemini model
catalog request made no generation call. Pre-canceled checks dispatched nothing.
Successful conversations account for **5,763 validated tokens** (4,841 input and
922 output); initial/diagnostic usage was not retained as validated evidence and
is excluded from these totals.

Initial evidence is preserved: [Azure Chat CLI](azure-chat-cli.jsonl),
[Chat key](azure-chat-key.jsonl), [Responses CLI](azure-responses-cli.jsonl),
[Responses key](azure-responses-key.jsonl), and [Gemini](gemini-api-key.jsonl).
Each initial probe stopped after its first failure.

Azure authentication reached native responses, but the strict contract rejected
Azure-specific response metadata. One diagnostic per Azure codec identified the
missing shapes. Shared Go/Python contract correction `7f779b6`:

- Accepts bounded Chat safety annotations, routing metadata and latency counters.
- Accepts the closed Responses safety-annotation array.
- Preserves native metadata and existing token accounting. Any blocked/filtered
  annotation prevents tool dispatch and continuation, even with a normal finish
  status. Detection alone remains advisory. Unknown/malformed metadata and
  unsupported filter-error payloads still fail validation.
- Adds 32 common regression fixtures, including filtered tool calls and rejected
  continuations; the complete five-codec fixture set now has 390 cases.

Gemini's initial `gemini-2.5-flash-lite` invocation failed; the separate diagnostic
returned HTTP 404. Its precise cause is not established. The key's model catalog
listed several Flash-Lite models; explicit selection of `gemini-3.1-flash-lite`
then passed all three turns without a Gemini code change. Google documents
restricted 2.5-model access for new projects in its
[availability notice](https://ai.google.dev/gemini-api/docs/deprecations), but this
record does not attribute the 404 to that policy conclusively. The
[3.1 model documentation](https://ai.google.dev/gemini-api/docs/models/gemini-3.1-flash-lite)
describes its text and function-calling support.

The [diagnostic summary](diagnostic-summary.json) retains only structural error
locations and status, not native payloads or credentials. See
[Azure's documented Responses extension](https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/responses#content-filtering).

## Exact candidate identities and validation

Initial probes/diagnostics used source
`21887d35c6f8c2291f54561f9f2411e0703c466e` with contract `0.0.2`.
All five corrected conversations used:

- Operator source `7f779b66926807b13c5c9019107d9f23f4f822c9`.
- Shared contract `0.0.3`, digest
  `sha256:7d85bc39910f23b2c8f8de761f447be5dedc9c74d0833f358eadded20a07bfac`.
- Operator matching release pin `54eb5ad`; Attack Harness matching pin `756d4b5`.

Validation passed: full Operator Go suite; focused Go static checks; 28 Python
contract tests; all 82 Attack Harness tests; standalone contract-package Go/Python
suites; package-integrity and harness source-lock checks; generated-schema checks
and whitespace checks. Existing images still need rebuilding with the updated pin
before running the new container pair. These are local qualification candidates,
not published releases. No Interceptor changes were necessary.

[SHA256SUMS](SHA256SUMS) binds the original JSONL evidence and diagnostic summary.
The earlier [local-cloud record](../cloud-local-2026-09-30/README.md) remains the
evidence for AWS/Azure/Google secret-store rotation/cache checks and other providers.

## Still outstanding

- LiteLLM Chat/Responses need a configured proxy and credentials; Vault needs its
  Agent/Proxy resources. Those were not supplied for this run.
- Managed identities, service principals, GitHub OIDC/federation, environment-key
  model modes, other models and regions require their own runner tests. Successful
  local identity selections do not prove every cloud authentication mechanism.
- Identity renewal, controlled in-flight cancellation, permission-denial fixtures
  and accepted-request/response-loss faults remain unqualified. The JSONL records
  explicitly mark in-flight cancellation, uncertain-outcome injection and runner
  identity attestation as `not_run`.
- Host tuples, real targets, container campaigns and release publication remain
  separate qualification work.
