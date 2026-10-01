# Local cloud qualification — 2026-09-30

Selected production host-adapter probes passed on macOS ARM64 using explicitly
configured local cloud identities and administrator-selected test resources.
Timestamps in the original evidence are UTC (2026-10-01); this record uses the
runner's local date. These are host-adapter checks, not full container campaigns.

## Successful secret-store probes

| Store | Explicit authentication | Checks passed | Evidence |
|---|---|---|---|
| AWS Secrets Manager, us-east-2 | Named profile | Read, cache hit, invalidation/refresh, TTL expiry, pre-canceled resolution, missing-secret failure | [AWS](aws-read-cache-02.jsonl) |
| Azure Key Vault | Azure CLI | All checks above plus changed-value rotation | [Azure](azure-rotation.jsonl) |
| Google Secret Manager | Local ADC | All checks above plus changed-value rotation | [Google](google-rotation.jsonl) |

The Azure and Google rotation fixtures were explicitly designated nonproduction
secrets. An external administrator CLI coordinator changed each test value during
the probe's rotation window, then restored and read back the original value.
[Restoration record](rotation-restoration.json) records those checks. Each store
retains two additional versions (test rotation and restoration); no versions were
deleted. AWS's secret and all model API-key secrets were read only. No real values,
value hashes, account identifiers, locators or raw remote errors enter this record.
The qualification command itself remains read only.

The first AWS resolution [failed](aws-read-cache.jsonl). A separate AWS CLI check
then confirmed valid identity and access; the explicit second probe passed. The
original failure's cause is not established. That first local candidate omitted
contract version/digest build flags; its failure is retained and does not qualify
anything. All successful probes include source and contract identity.

Google's administrator CLI initially required renewed login, so the rotation
coordinator stopped before mutation or an Operator secret probe. The administrator
renewed CLI authentication and the probe then passed. Operator used its separately
selected local ADC for Google reads and Vertex inference. CLI login and ADC are
separate credential sources; the successful run does not prove identity renewal.

## Successful provider conversations

Every row passed text, one function call following that text, and text following
an inert simulated tool result. Native response schemas, model binding, usage,
continuation correlation and pre-canceled generation were checked. No target,
campaign or real snapshot operation was executed.

| Route | Model | Authentication | Input/output tokens | Evidence |
|---|---|---|---|---|
| OpenAI Chat | gpt-4.1-mini-2025-04-14 | API key resolved from Azure Key Vault using Azure CLI | 723 / 25 | [Chat](openai-chat.jsonl) |
| OpenAI Responses | gpt-4.1-mini-2025-04-14 | API key resolved from Azure Key Vault using Azure CLI | 715 / 30 | [Responses](openai-responses-corrected.jsonl) |
| Anthropic Messages | claude-haiku-4-5-20251001 | API key resolved from Azure Key Vault using Azure CLI | 2,935 / 92 | [Claude](anthropic.jsonl) |
| Bedrock Converse | amazon.nova-lite-v1:0, us-east-2 | AWS named profile | 2,565 / 185 | [Bedrock](bedrock.jsonl) |
| Vertex GenerateContent | gemini-2.5-flash-lite, global endpoint | Local Google ADC | 314 / 15 | [Vertex](vertex-corrected.jsonl) |

Each probe allowed three generation calls, 1,024 output tokens per call and a
120-second deadline, with no automatic retries. Vertex selected `thinkingBudget:0`;
Claude selected disabled thinking. Exactly **19 generation invocations** were made:
15 in the five successful conversations, two initially rejected responses, and two
separately authorized diagnostic calls. The successful conversations account for
7,599 validated tokens (7,252 input, 347 output). Initial/diagnostic response usage
was not retained as validated evidence and is excluded from those totals.

## Discrepancies and corrections

Initial [Responses](openai-responses.jsonl) and [Vertex](vertex.jsonl) probes each
stopped after one response failed the existing strict schema. One additional call
per route printed only structural field/type and error-location information;
raw responses remained in memory. The [diagnostic summary](diagnostic-summary.json)
records the observed missing fields, not a raw response capture.

Operator commit `0ba13e0` corrects the shared Go/Python contract:

- Responses accepts bounded billing metadata, zero frequency/presence penalties,
  and strictly zero counters in the closed hosted-tool usage metadata shape.
- Echoed function declarations may add only `output_schema:null`. Correlation
  ignores that null default in copies; native bytes remain intact. Requests,
  changed declarations and nonnull output schemas remain rejected.
- Vertex accepts the documented traffic-type enum in usage metadata without
  changing token accounting.

Unknown fields, nonzero hosted-tool usage and invalid traffic types still fail.
Synthetic common fixtures cover the positive forms and negative boundaries.
The separately authorized final three-call probes passed on both corrected routes.

## Executing identities and package publication

Original successful AWS/Azure/Google store probes, OpenAI Chat, Claude and Bedrock:

- Operator source `4f9806514e2df29a623f4e0a42469d2f25ca24e0`.
- Contract `0.0.1`, digest
  `sha256:13f2df023707c43f8d8ac0509513fd5059246df680b47e9afbef15d785fadf63`.

Corrected Responses and Vertex probes:

- Operator source `0ba13e0` (full source ID retained in each JSONL record).
- Contract `0.0.2`, digest
  `sha256:7c12058d9e4f5bf69de5b2665fa5ff2d074b30ab0743e3bf6132ad84b165da3c`.
- Operator release pin `c7d5423`; Attack Harness matching pin `dc4ac05`.

These are local qualification candidates, not signed published releases. Both
components now select the same contract package. Existing harness images still
need rebuilding before testing an updated container pair.

Validation passed: complete Operator Go suite; 28 Python contract tests; all 82
Attack Harness tests; standalone published-package Go and Python suites;
Attack Harness package-integrity/source-lock checks; generated-schema consistency
and whitespace checks. This qualification did not require Interceptor changes.

## Still outstanding

- Vault Agent token-sink and authenticating Proxy resources were not supplied.
- Azure OpenAI deployments, Gemini Developer API keys and LiteLLM endpoints/keys
  were not supplied; those routes remain unqualified.
- AWS role/web-identity/environment sessions, Azure managed/workload/service-principal
  identities and Google metadata/federated identities need their corresponding
  runner configurations. No GitHub OIDC runner was exercised.
- Environment-key model modes, additional API-key/identity combinations and other
  model versions/regions require separate live runs.
- AWS rotation, denied-permission fixtures, credential renewal, controlled in-flight
  cancellation and accepted-request/response-loss faults remain unqualified.
  Pre-canceled checks and generic missing-secret failures do not prove those cases.
- Docker/host tuples, real targets, container campaigns and release publication
  remain separate qualification work.

All original JSONL records, including failures and explicit `not_run` checks, are
retained unchanged. [SHA256SUMS](SHA256SUMS) binds the evidence and summaries.
Private plans, model profiles and binaries remain outside Git in the local
qualification directory; none of the credentials were copied into the repository.
