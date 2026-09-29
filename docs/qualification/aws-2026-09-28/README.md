# AWS live qualification — 2026-09-28

Selected host-adapter checks passed on macOS ARM64 using an explicitly selected
local AWS named profile configured for IAM Identity Center/SSO. The administrator
renewed the SSO session before testing. No remote secrets were created, updated,
rotated or deleted. Account IDs, local profile names and secret locators are excluded
from this record and its evidence files.

## Bedrock Converse

- Region: `us-east-2`.
- Model: `amazon.nova-lite-v1:0` (on-demand).
- Codec: `bedrock-converse-text-tools-v1`.
- Authentication mode: `aws-profile` / qualification label `named-profile`.
- Successful executing source: `6e38ea591696ec624bb2ad385382fe32a8e3209a`.
- Contract: `0.0.1`, digest
  `sha256:13f2df023707c43f8d8ac0509513fd5059246df680b47e9afbef15d785fadf63`.
- Private provider-profile digest:
  `sha256:968ea168aa72c5c90501f56d4d2dc18c2d880a36946cb71076578c758085a604`.
- Per-run bounds: 120 seconds, three generation calls, 1,024 output tokens per call;
  profile prompt allowance 32,768 tokens.

[Successful evidence](bedrock-corrected-conversation.jsonl) records:

| Exchange | Input tokens | Output tokens | Tool calls | Result |
|---|---:|---:|---:|---|
| Initial text | 779 | 47 | 0 | Passed |
| Tool call following text | 841 | 50 | 1 | Passed |
| Text following simulated tool result | 926 | 48 | 0 | Passed |
| Total for successful run | 2,546 | 145 | 1 | 2,691 validated tokens |

Pre-canceled generation also passed. The tool was an inert `snapshot_list` fixture;
no campaign, target or snapshot operation was executed.

### Discrepancy and correction

The [initial attempt](bedrock-initial-rejection.jsonl), source
`80f9492dc10063093c4d85e7e305b27a558f2bc6` with contract `0.0.0`, stopped after its
first response failed schema validation. Three separately authorized one-call
diagnostics used the production provider client and reported only field names/types;
response text and credential values were not retained. They identified an empty
`usage.serverToolUsage` object in the live Bedrock response.

Commit `c24fc7cd10199d679b7219570af983694736529b` adds this exact empty-object shape
and shared positive/negative fixtures. Populated objects, unknown counters and null
remain rejected. Native bytes are preserved and token accounting is unchanged.
Operator and Attack Harness now pin the same rebuilt contract; Attack Harness pin
commit is `c6c8116`.

Seven model generations were invoked in total: one initial failed qualification,
three diagnostics, and the final successful three-call conversation. The latter six
were separately authorized; no generation was automatically retried. The token total
above covers **only** the final validated run. Initial/diagnostic response usage was
not retained as validated evidence and is not included in that total.

## AWS Secrets Manager

[Read/cache evidence](secrets-manager-read-cache.jsonl) records a successful run in
`us-east-2`, with `named-profile` authentication, UTF-8 value selection, a one-second
cache TTL and a 60-second total deadline. Executing source was
`80f9492dc10063093c4d85e7e305b27a558f2bc6` with contract `0.0.0`; the later correction
only changes Bedrock response schemas and does not change the credential resolver.

Passed: initial resolution, cached value reuse, pre-canceled resolution, explicit
invalidation/refresh, and natural TTL expiry followed by uncached resolution.
Only the administrator-selected existing test secret was read.

## Validation and scope

The AWS-profile change passed the complete Go suite and focused race tests.
The corrected contract passed its packaged Go tests and 28 Python tests; all 82
Attack Harness tests passed against the corrected package. Attack Harness's exact
package-integrity and source-lock checks passed. The final complete Go suite
(`go test ./...`) and static checks (`go vet ./...`) passed after both corrections.

These are live **host-adapter** results for the named profile, region, model and
codec above. They do not qualify EC2/container/web identity, credential renewal,
other models/regions/providers, secret JSON-field selection, secret rotation,
access-denied fixtures, controlled in-flight cancellation, or ambiguous-outcome
fault injection. The automated identity-mechanism attestation check remains
`not_run`; configuration/SSO setup attribution is described here separately.

No full Attack Harness container campaign, Docker confinement, target integration,
or release publication is qualified by these runs. Existing images embed the old
contract and MUST be rebuilt before qualifying a matching host/harness pair.

Evidence JSONL files retain original bytes and explicit `not_run` entries.
[SHA256SUMS](SHA256SUMS) binds the three retained files. Private plans and local
candidate binaries remain in the administrator's temporary qualification directory;
they are not release artifacts or repository evidence.
