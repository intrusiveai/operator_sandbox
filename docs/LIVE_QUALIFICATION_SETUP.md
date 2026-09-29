# Setting up live qualification

The first AWS named-profile run is documented in the
[live result record](qualification/aws-2026-09-28/README.md), including the Bedrock
compatibility correction and remaining qualification limits.

## What to prepare

The tooling is ready to run; live resources are administrator-provisioned. Start
with one store and one model route, then repeat for the remaining combinations.
A **runner** is simply the machine/container executing `operatorctl`. Docker,
Interceptor, a campaign, and the Attack Harness image are not needed for these
host-adapter checks.

Prepare these three items for the first run:

1. A secret store containing a harmless JSON test secret, for example
   `{"value":"qualification-first"}`, and an identity allowed to read it.
2. An enabled model/deployment with billing/quota available. For a key-based model,
   store the real API key in a **different** secret as `{"value":"<API key>"}`.
   Never use that key-bearing secret as the rotation/failure test fixture.
3. A supported Operator binary and a private configuration/evidence directory on
   the runner. Use a release-built binary for attributable qualification evidence:
   `operatorctl version` MUST report source and contract identities. Development
   binaries can exercise probes but may leave those identities unqualified.

No account, key, or service is created by the test command. No test rotates or
writes a remote secret. Model probes make **at most three generation calls per
invocation**, plus a pre-canceled call that must not dispatch. Each output is capped
by the plan; the example cap is 1,024 tokens per generation. This is not a dollar
budget: input, reasoning and provider pricing also affect charges. Choose the model
and its cap before running; increase the cap explicitly for models needing a larger
thinking budget. Never run a loop that automatically retries failed probes.

## 1. Copy the templates

Installed releases contain `templates/qualification/`; source checkouts contain
[release/templates/qualification](../release/templates/qualification).
The templates include all four stores and thirteen provider/codec/authentication
combinations. They are deliberately populated with `REPLACE` placeholders.

```sh
umask 077
mkdir -p "$HOME/operator-qualification/config" "$HOME/operator-qualification/evidence"
chmod 700 "$HOME/operator-qualification/config" "$HOME/operator-qualification/evidence"
```

Copy the chosen files into `config`, edit them there, and set their modes to 0600.
Replace `/REPLACE/private` in each plan with the **absolute** config directory path;
there is no `~` or environment-variable expansion inside JSON. Keep config outside
Git. JSON credential examples are accepted by the existing strict YAML loader.

The `*-plan.json` files drive tests; `*-credentials.json` select store resources;
`*-model.json` select models and endpoints. `model-key-credentials.json` is a Vault
example for provider authentication. It can instead use **any** supported store:
copy that store's example, retain one credential, rename it
`qualification-model-key`, and select the separate key-bearing secret.
Provider profiles and key credential IDs MUST agree.

## 2. Configure a secret store and its runner identity

All store examples use JSON field `value`, cache TTL 5 seconds and timeout 10 seconds.
The plan allows 60 seconds, enough for ordinary cache-expiry checks. Exact resource
names in `allowed_locator_prefixes` MUST match the selected locator. Prefixes also
cover the deliberately missing test secret with the `-missing` suffix.

| Store | Resources to supply | Practical first runner |
|---|---|---|
| AWS Secrets Manager | Region, test secret name/ARN, role with `GetSecretValue` access; KMS decrypt access if the chosen key requires it | EC2 with an instance role, or an existing container/web-identity runner |
| Azure Key Vault | HTTPS vault URL, test secret, identity with secret-read permission | Azure VM with a system-assigned managed identity, or an existing federated workload |
| Google Secret Manager | Project, secret name, enabled version and accessor identity | GCP VM with an attached service account and appropriate access scope, or a federated runner |
| Vault | HTTPS KV-v2 endpoint, mount/path, read policy and Agent token sink or authenticating Proxy | Existing host with access to the Vault Agent/Proxy; can be the Mac |

These adapters intentionally use the workload authentication described in
[CREDENTIALS.md](CREDENTIALS.md). A successful cloud CLI login alone is **not** a
supported credential source for these probes:

- **AWS:** static access-key environment variables are rejected; shared credential
  and config files are excluded in workload mode. An explicitly selected `aws_profile`
  enables named-profile authentication from local SDK files, including SSO; set
  the plan mode to `named-profile`. Use `aws sso login --profile NAME` before testing
  an SSO profile. The `aws-named-profile-*` and `bedrock-named-profile-*` templates
  cover this path. For workload mode select `instance-role`, `container-role` or
  `web-identity` to match the runner. The runtime platform supplies metadata/container
  credentials; federated runners supply their configured role and web-identity token
  file. Consult [AWS credential providers](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html).
- **Azure:** workload identity is attempted when configured, otherwise managed
  identity. A managed-identity VM is a simple first setup; workload identity needs
  the configured tenant/client and federated token file. Declare the actual runner
  mode. See [Azure Identity](https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/authentication-overview).
- **Google:** use metadata identity or an ADC `external_account` federation
  configuration, typically selected through `GOOGLE_APPLICATION_CREDENTIALS`.
  User ADC and service-account-key JSON are rejected. See
  [Workload Identity Federation](https://docs.cloud.google.com/iam/docs/workload-identity-federation).
- **Vault:** the example uses `vault_proxy:true`. For an Agent token sink, remove
  that field and set `vault_token_file` to a private absolute path; change the plan
  to `declared_auth_mode:"token-sink"`. An administrator-installed CA can be selected
  with `vault_ca_certificate`; TLS verification remains enabled. Ambient Vault
  address/token/proxy settings are not used. See [Vault auto-auth](https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth).

To qualify every authentication mode, repeat each store plan on the corresponding
runner and change `case_id` and `declared_auth_mode`. A label is not identity proof:
retain a private runner setup record identifying which mechanisms were configured
and which competing credential sources were absent. Do not put tokens, account
locators or raw SDK logs in shared evidence.

### Secret failure and rotation checks

The example's `qualification-missing` reference MUST point to an absent/denied
**test** secret in the same profile. Initial successful resolution proves that the
store was reachable; the later error is still reported generically, without
asserting a particular native HTTP status. Remove `failure_credential_id` to skip
that check explicitly.

For rotation, select an unpinned/latest version (the defaults in the examples), add
`"rotation_wait_seconds":30`, and increase `timeout_seconds` to 120. Watch the JSONL
file for `check:"rotation", status:"started"`, then use the store's administrator
interface to change the **test** value. The probe rereads after the window and
requires a changed value. A pinned immutable version cannot demonstrate latest-value
rotation. This test is separate from renewing a workload token or a Vault Agent
sink token; neither is proven by changing the secret value.

## 3. Configure the model route

Replace the endpoint, request model/deployment and exact response-model aliases in
the chosen `*-model.json`. Provider identifiers alone do not select an endpoint.
Use the complete HTTPS operation URL supplied for your actual service/deployment.

| Provider template | Required route settings |
|---|---|
| `openai-chat-chat-secret-store` | OpenAI-compatible `/chat/completions`; selected model and bearer-key secret |
| `openai-responses-responses-secret-store` | `/responses`; selected model and bearer-key secret |
| `anthropic-messages-anthropic-secret-store` | `/messages`; selected model, explicit API version and API-key secret |
| `gemini-api-gemini-secret-store` | Exact `/models/<model>:generateContent` endpoint and API-key secret |
| `vertex-gemini-gemini-workload-identity` | Full project/location/model HTTPS endpoint and Google workload identity |
| `bedrock-named-profile` | Enabled model ID, region, `authentication:"aws-profile"`, explicit `aws_profile`; renew SSO before running |
| `bedrock-converse-bedrock-workload-identity` | Enabled model ID, AWS region and workload identity; no endpoint override |
| `azure-openai-chat-*` / `azure-openai-responses-*` | Actual deployment operation URL; optional `api-version` query; API-key secret or Azure identity with model inference access |
| `litellm-chat-secret-store` / `litellm-responses-secret-store` | HTTPS proxy operation URL, proxy model alias and bearer-key secret |

For each Azure codec, both secret-store and workload-identity plans are included.
LiteLLM Chat and Responses are distinct qualification routes. Vertex, Bedrock and
Azure workload plans omit `credentials_file`; the runner's identity authenticates
directly. Model access is separate from secret-store read access.

Set `codec_options.response_models` to exact permitted response aliases; do not
weaken validation to accept arbitrary names. Bedrock has no response alias list:
its selected model is bound by the signed regional request. Optional reasoning/
thinking settings are codec-specific; see [MODEL_PROVIDERS.md](MODEL_PROVIDERS.md).
Select public API versions/model access from the provider's own account console and
API documentation. A model that cannot satisfy text/function-tool continuation is
not qualified by merely returning HTTP 200.

## 4. Preflight, then opt in

Replace the example filename with the plan you copied:

```sh
operatorctl qualify --plan "$HOME/operator-qualification/config/hashicorp-vault-plan.json"
operatorctl qualify --plan "$HOME/operator-qualification/config/hashicorp-vault-plan.json" \
  --live --output "$HOME/operator-qualification/evidence/vault-run-001.jsonl"
```

Preflight makes no service calls. Live output MUST have a new filename in an
existing private directory; the command refuses overwrite and symlink targets.
Use a new filename for each deliberate rerun. Do not reuse output as input or as a
resumption request. Ctrl-C cancels the current run. An incomplete final JSONL line
or an operation with only `started` evidence remains unresolved.

Exit 0 means selected automated checks completed, including any explicitly recorded
`not_run` checks. Exit 1 means failure/interruption/evidence-write failure. Exit 2
means invalid input/configuration. Share the sanitized JSONL plus the nonsensitive
case-to-model/version mapping when ready; keep full profiles and locators private.

## 5. Finish the checks that require external coordination

The automated happy-path probe does not silently certify fault cases. For each
selected route/store, retain additional evidence for:

- **Identity mechanism and renewal:** run on the intended identity platform, capture
  sanitized runner attribution, and exercise credential renewal using dedicated
  fixtures. Different mechanisms require separate runs.
- **In-flight cancellation:** coordinate a controlled test service/runner fault;
  confirm a request reached that service, interrupt the probe, and check prompt
  cancellation. Pre-canceled calls do not prove this. Do not disable TLS or enable
  an ambient proxy to create the fault.
- **Uncertain outcome/no replay:** make the test service accept a request and drop
  its response. Verify the `uncertain` result and one server-side request with no
  retry. A timeout alone does not establish whether the service received a request.
- **Negative remote responses:** use a dedicated denied/missing credential, or a
  controlled provider returning an error/oversized/malformed response. Confirm
  termination and sanitized evidence. Do not revoke production credentials.

The provider client has deterministic TLS tests for those failure behaviors. Real
service fault and identity-renewal evidence must be collected on suitable test
infrastructure before those rows can be marked qualified. There is no all-provider
live pass claim in this tooling stage.
