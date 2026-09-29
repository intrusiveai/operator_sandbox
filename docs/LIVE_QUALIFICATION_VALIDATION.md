# Live-qualification tooling validation

Date: 2026-09-28. Host: macOS ARM64. This record covers the tooling's controlled
tests, **not** live cloud qualification. No billable model calls or remote secret
mutations were performed. The administrator will configure live resources next.

## Completed checks

- `go test ./...`: passed.
- `go test -race ./internal/livequalification ./cmd/operatorctl ./internal/credentials ./internal/modelprovider`: passed, including the final local TLS Vault CLI test.
- `go vet ./...`: passed.
- `python3 -m unittest discover -s scripts/tests`: three tests passed.
- CLI builds for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`: passed.
- `git diff --check`: passed.

The qualification-specific tests prove:

- All sixteen distributed plans (twelve provider-route plans and four secret-store
  plans) load locally without contacting services. Unknown/duplicate fields,
  unsupported selections and invalid bounds are rejected.
- All twelve provider/codec/authentication combinations complete the fixed native
  three-turn conversation against controlled responses. The five codec families
  use shared validators and continuation constructors. Usage and exact response
  aliases remain checked; fixture histories do not bypass request validation.
- All four store backends exercise resolution, cache hits, natural TTL expiry,
  invalidation, changed-value rotation and expected failures through the real
  resolver with injected read-only backends. These do not qualify SDK identities.
- The actual `operatorctl qualify --live` command uses the production Vault SDK
  against a local HTTPS fixture with an explicit CA. It observes cache expiry,
  performs four expected backend reads, handles a missing-secret response, and
  excludes private secret/error/endpoint text from its JSONL evidence.
- Evidence failures prevent subsequent service calls. Existing files, links and
  nonprivate output directories are rejected. Canceled runs retain terminal
  records. Provider errors stop after one dispatched call with no replay.
- Native transport failure/uncertainty, credential audit and TLS behavior remain
  covered by the existing model-provider and credential tests.

## Live qualification status

| Provider route | Authentication | Live status |
|---|---|---|
| OpenAI Chat | Secret store | Not run |
| OpenAI Responses | Secret store | Not run |
| Anthropic Messages | Secret store | Not run |
| Bedrock Converse | AWS workload identity | Not run |
| Gemini API | Secret store | Not run |
| Vertex Gemini | Google workload identity | Not run |
| Azure Chat | Secret store | Not run |
| Azure Chat | Azure workload identity | Not run |
| Azure Responses | Secret store | Not run |
| Azure Responses | Azure workload identity | Not run |
| LiteLLM Chat | Secret store | Not run |
| LiteLLM Responses | Secret store | Not run |

| Secret store | Authentication modes still requiring live runs |
|---|---|
| AWS Secrets Manager | Web identity, container role, instance role |
| Azure Key Vault | Workload identity, managed identity |
| Google Secret Manager | External account federation, metadata identity |
| Vault | Agent token sink, authenticating Proxy |

No actual model/version, cloud identity mechanism, token renewal, remote rotation,
or live in-flight fault combination is marked passed here. The tooling reports
pre-canceled checks separately and retains `not_run` for coordinated fault and
identity checks. Each live record MUST identify its executing source/contract pins
and private-profile digest; separate private runner records MUST identify the actual
model/deployment and identity setup. See [setup instructions](LIVE_QUALIFICATION_SETUP.md).

Native host/image qualification, Interceptor/HTTPS target qualification and release
publication remain separate gates. No Attack Harness contract or runtime changes
were needed for this host-adapter tooling stage.
