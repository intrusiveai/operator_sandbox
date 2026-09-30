# Cloud authentication setup

Operator separates **model authentication** from **secret-store authentication**.
A model API key SHOULD be stored in a secret store; an independently configured
cloud identity (or Vault Agent/Proxy) retrieves it. Explicit `api-key-env` also
supports a named host environment variable. The store MAY belong to a different cloud than the
model. For example, an AWS named profile can read a Vertex key from Secrets Manager.
Direct cloud-identity model authentication does not require a stored model key.

Administrators MUST provision model access, selected secrets and network access
before live qualification. Operator MUST NOT grant permissions, enable APIs, create
keys, log in interactively or rotate remote secrets during these probes. The runtime
SHOULD receive the resource-scoped roles below rather than administrator roles.

## Supported selections

| Destination | Private `authentication` selector | Credential source |
|---|---|---|
| HTTP model routes | `api-key-env` | Explicit `api_key_env` host variable |
| Bedrock | `aws-environment` | Host AWS access-key pair and optional session token |
| Vertex Gemini | `secret-store` | `credential_id` referencing an API key in any supported store |
| Vertex Gemini | `google-adc` | Google SDK Application Default Credentials, including local user, service-account, impersonated or federated ADC |
| Vertex Gemini | `workload-identity` | Attached metadata identity or external-account workload federation |
| Azure OpenAI Chat/Responses | `secret-store` | `credential_id` referencing the resource API key |
| Azure OpenAI Chat/Responses | `azure-cli` | Existing Azure CLI login |
| Azure OpenAI Chat/Responses | `azure-client-secret` | Service principal selected by host `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET` |
| Azure OpenAI Chat/Responses | `workload-identity` | Federated workload or managed identity |
| Google Secret Manager store | `google-adc` | Same Google ADC mechanism |
| Azure Key Vault store | `azure-cli` or `azure-client-secret` | Same Azure mechanism |
| Google/Azure store | omitted or `workload-identity` | Existing workload identity behavior |

OpenAI, Anthropic, Gemini Developer API and LiteLLM retain secret-store key support;
Bedrock retains AWS role and explicit named-profile authentication. These selectors
MUST reflect the provider's supported protocol; cloud IAM is not interchangeable
with an API key. Host credentials MUST NOT enter the Attack Harness container.

## Google Cloud

### Local login and ADC

For the OS account that will run Operator:

```sh
gcloud auth application-default login
gcloud auth application-default set-quota-project PROJECT_ID
```

Select `authentication: "google-adc"` in the Vertex model profile and, independently,
in any Google Secret Manager store profile. `gcloud auth login` alone does not
create local ADC. The selected quota project MUST permit `serviceusage.services.use`,
commonly through `roles/serviceusage.serviceUsageConsumer`. Operator sends quota
attribution as `X-Goog-User-Project`; `GOOGLE_CLOUD_QUOTA_PROJECT` overrides the ADC
quota project. [Local ADC](https://docs.cloud.google.com/docs/authentication/set-up-adc-local-dev-environment),
[Service Usage permissions](https://docs.cloud.google.com/service-usage/docs/access-control).

An administrator MAY instead configure impersonated ADC:

```sh
gcloud auth application-default login --impersonate-service-account SERVICE_ACCOUNT_EMAIL
```

The caller needs `roles/iam.serviceAccountTokenCreator` on that service account;
the impersonated account needs the inference/secret-read roles below. The IAM
Service Account Credentials API MUST be enabled for impersonation.
[Impersonation setup](https://docs.cloud.google.com/docs/authentication/use-service-account-impersonation).

A service-account JSON key MAY be selected through a private
`GOOGLE_APPLICATION_CREDENTIALS` file with `google-adc`. The SDK also supports
federation configurations through that variable. Administrators MUST protect those
files and their parent directories and MUST NOT include them in campaign inputs,
release archives or source control. The explicit ADC selector accepts the SDK's
credential discovery; the workload-only selector retains its narrower admission.

### Vertex model access

The target project MUST have billing and `aiplatform.googleapis.com` enabled.
For bearer identity access, grant `roles/aiplatform.user` on the target project,
or a custom inference role with `aiplatform.endpoints.predict`. This is the
permission used for prompt requests; model deployment/training administration is
not required for an existing supported Gemini model.
[Vertex setup](https://cloud.google.com/vertex-ai/generative-ai/docs/start/gcp-auth),
[inference permissions](https://cloud.google.com/vertex-ai/generative-ai/docs/access-control).

For API-key access:

- Administrators MUST provision a key that supports the selected Vertex route:
  a service-account-bound authorization key for the applicable standard API, or
  an express-mode key for its express endpoint. An arbitrary unrestricted project
  key is not a substitute for supported Vertex authentication.
- Standard authorization keys act as their bound service account; that account
  needs the relevant inference permission. Administrators SHOULD restrict the key's
  API target to `aiplatform.googleapis.com` and, where practical, its application
  restriction to the Operator host's public egress IP.
- A Gemini Developer API key targets `generativelanguage.googleapis.com` and belongs
  to Operator's `gemini-api` route. Administrators MUST match service, key type and
  endpoint; the two Google routes are separate qualification cases.

[Google key types and restrictions](https://docs.cloud.google.com/docs/authentication/api-keys),
[Vertex key setup](https://cloud.google.com/vertex-ai/generative-ai/docs/start/api-keys).

Configure the model profile with `authentication: "secret-store"` and
`credential_id: "qualification-model-key"`. Operator sends `X-Goog-Api-Key`; it
MUST NOT put the key in an endpoint query. Both project/location and express
endpoint layouts are configured as complete HTTPS URLs ending in
`/models/MODEL:generateContent`. The selected model MUST match the profile.

## Azure

### Local login or service principal

For local CLI authentication, run as the Operator OS account:

```sh
az login --tenant TENANT_ID
az account set --subscription SUBSCRIPTION_ID
```

Select `authentication: "azure-cli"` in the model and/or Key Vault store profile.
Operator uses the existing CLI account and invokes the SDK's token command; it
MUST NOT open a login prompt or fall back to a different identity.
[Azure CLI authentication](https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/local-development-dev-accounts).

For a service principal, select `azure-client-secret` and provision the host-only
variables `AZURE_TENANT_ID`, `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`. This last
value is the service principal's bootstrap credential, not the model API key.
Administrators MUST supply it privately to the Operator process; it MUST NOT be
stored in a model profile, start request or generated service definition. Workload
federation and managed identities remain available through `workload-identity`.
[Azure service-principal authentication](https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/local-development-service-principal).

### Model permissions and API keys

Bearer identity access requires **Cognitive Services OpenAI User** on the selected
Azure OpenAI resource, with an existing model deployment and reachable endpoint.
Grant it to the CLI user, service principal or managed identity actually selected.
It permits inference without deployment administration or key retrieval.
[Azure OpenAI RBAC](https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/role-based-access-control).

API-key access uses that resource's key, stored in the selected secret store, with
`authentication: "secret-store"` and `credential_id`. Operator sends `Api-Key` for
both Chat and Responses. Local/key authentication MUST be enabled on the resource
(`disableLocalAuth` must not disable it), and resource network rules MUST permit the
Operator host. API keys carry resource access rather than per-user RBAC permissions;
use a dedicated test resource when that is the intended access boundary. The runtime
does not need permission to list or regenerate resource keys when an administrator
has already stored the key.
[Key authentication](https://learn.microsoft.com/en-us/azure/api-management/api-management-authenticate-authorize-ai-apis),
[local-authentication settings](https://learn.microsoft.com/en-us/azure/ai-services/disable-local-auth).

## Secret-store read permissions

These are permissions for **Operator's bootstrap identity**, independent of any
permissions on the model key stored there. Normal resolution and qualification
MUST use only secret-read operations; write/delete/rotation grants are unnecessary.

| Store | Required access | Scope/restrictions |
|---|---|---|
| Google Secret Manager | `roles/secretmanager.secretAccessor` (`secretmanager.versions.access`) | Grant on the selected secret. Enable `secretmanager.googleapis.com`; select an enabled version. Apply quota-project permissions when used. Compute Engine identities also need an appropriate OAuth access scope such as `cloud-platform`. |
| Azure Key Vault with RBAC | **Key Vault Secrets User** | Grant on a dedicated vault or selected secret. **Key Vault Reader** alone cannot read secret values. Vault firewall/private networking must allow the host. |
| Azure Key Vault with legacy access policies | Secret **Get** | Grant to the selected identity. Operator does not enumerate secrets and does not require List, Set or Delete. |
| AWS Secrets Manager | `secretsmanager:GetSecretValue` | Limit to the selected secret ARN. Add `kms:Decrypt` on its customer-managed KMS key when applicable, with a permitting key policy. |
| HashiCorp Vault KV v2 | `read` on `MOUNT/data/PATH` | The Agent/Proxy identity needs the policy for that exact data path. Operator does not need metadata listing or secret write/delete capabilities. |

[Google secret access](https://docs.cloud.google.com/secret-manager/docs/access-secret-version),
[Google secret authentication](https://docs.cloud.google.com/secret-manager/docs/authentication),
[Key Vault RBAC](https://learn.microsoft.com/en-us/azure/key-vault/general/rbac-guide),
[Key Vault access policies](https://learn.microsoft.com/en-us/azure/key-vault/general/assign-access-policy),
[AWS GetSecretValue](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetSecretValue.html),
[Vault KV reads](https://developer.hashicorp.com/vault/docs/secrets/kv/kv-v2/cookbook/read-data).

All stores MUST also satisfy Operator's `allowed_locator_prefixes` configuration.
This allowlist narrows local selection; it does not grant cloud permissions.
Disabled versions, network denials and expired logins remain ordinary resolution
failures. Cached values expire according to the selected credential's TTL.

### Example: Vertex key in AWS Secrets Manager

An administrator can store the key as a plain UTF-8 secret, then use this private
credential configuration (replace placeholders; do not insert the key itself):

```json
{
  "profiles": [{
    "api_version": "operator.dev/secret-store-profile/v1alpha1",
    "kind": "SecretStoreProfile",
    "id": "model-secrets",
    "backend_kind": "aws-secrets-manager",
    "region": "us-east-2",
    "aws_profile": "YOUR_AWS_PROFILE",
    "allowed_locator_prefixes": ["aws://us-east-2/dev/operator/vertex-model-key"]
  }],
  "credentials": [{
    "api_version": "operator.dev/host-credential-ref/v1alpha1",
    "kind": "HostCredentialRef",
    "credential_id": "qualification-model-key",
    "store_profile_id": "model-secrets",
    "locator": {
      "backend_kind": "aws-secrets-manager",
      "region": "us-east-2",
      "secret_id": "dev/operator/vertex-model-key"
    },
    "value": {"format": "utf8"},
    "cache_ttl_seconds": 30
  }]
}
```

Pair it with the `vertex-gemini-gemini-secret-store` model/plan templates. No Google
ADC is used on that route. A Google or Azure store can replace AWS by selecting its
own store profile and identity while preserving the same model `credential_id`.
The shipped `model-key-credentials.json` is only an example store configuration.

## Process account and qualification

Administrators MUST make login caches, SDK credential files and CLI executables
available to the account actually running Operator. `operatorctl qualify --live`
uses its calling process environment. Campaign workers launched through systemd
or launchd use their service-manager environment; they do not inherit terminal
exports. Administrators MUST provision that environment and its `PATH`/home as
needed, without placing credentials in campaign data. LaunchAgent sessions and
Linux service accounts may therefore need distinct authentication setup.

Use [qualification setup](LIVE_QUALIFICATION_SETUP.md) for bounded, explicit probes.
Offline preflight MUST not log in or resolve secrets. Each route/authentication
combination needs its own live evidence. Synthetic tests of these new local modes
do not establish Google or Azure cloud qualification.

## Foreground execution, environment keys and CI federation

Both components now use the [shared host authentication contract](HOST_AUTHENTICATION.md).
Foreground execution inherits the calling environment. Optional services require
explicit account setup. For model environment keys, replace `credential_id` with
`api_key_env: "MODEL_API_KEY"` and select `authentication: "api-key-env"`.
The variable value MUST remain host-only. Model API keys do not authenticate
Secret Manager or Key Vault; those still use their separately selected identity.
