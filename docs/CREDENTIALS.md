# Host credential resolution

Operator's host-only resolver supports AWS Secrets Manager, Azure Key Vault,
Google Cloud Secret Manager and HashiCorp Vault. It adapts Interceptor's existing
credential profile and backend pattern; Operator uses its own `operator.dev`
version identifiers and stricter private-file loading. Interceptor is unchanged.

The installed configuration selects an absolute `credentials.file`. The file MUST
be a private regular file, owned by the service user or root, at most 1 MiB, with
no links. YAML MUST contain exactly one document with known fields and no aliases
or anchors. No secret value belongs in this configuration; entries identify remote
values and approved locator prefixes.

```yaml
profiles:
  - api_version: operator.dev/secret-store-profile/v1alpha1
    kind: SecretStoreProfile
    id: provider-secrets
    backend_kind: gcp-secret-manager
    allowed_locator_prefixes: [gcp://projects/security/secrets/operator-]
    timeout_seconds: 10
    max_value_bytes: 65536
    max_cache_ttl_seconds: 300
credentials:
  - api_version: operator.dev/host-credential-ref/v1alpha1
    kind: HostCredentialRef
    credential_id: model-key
    store_profile_id: provider-secrets
    locator:
      backend_kind: gcp-secret-manager
      project: security
      secret_name: operator-model
      version: latest
    value:
      format: json-string-field
      field: api_key
    cache_ttl_seconds: 30
```

Profiles MUST choose one of:

| Backend | Profile configuration | Locator | Authentication |
| --- | --- | --- | --- |
| `aws-secrets-manager` | `region` | matching `region`, `secret_id`; optional `version_id` or `version_stage` | SDK web identity, container or instance role; static environment keys and shared credential/config files are excluded. |
| `azure-key-vault` | HTTPS `vault_url` | matching `vault_url`, `secret_name`, optional `version` | Workload identity or managed identity. |
| `gcp-secret-manager` | No extra route field | `project`, `secret_name`, `version` | Metadata identity or ADC external-account workload federation; user/service-account-key ADC is rejected. |
| `hashicorp-vault` | HTTPS `vault_url`; optional `vault_namespace`, `vault_ca_certificate`; exactly one of `vault_token_file` or `vault_proxy: true` | `mount`, safe `path`, optional positive integer `version` | Configured Agent token sink or authenticating Proxy. |

Vault token sinks MUST be bounded private regular files and MUST be reread for
uncached secret resolution so Agent rotation takes effect. Proxy mode MUST NOT
send an ambient Vault token. Vault connections MUST verify TLS, disable redirects
and exclude environment-selected proxy, agent address, TLS bypass and headers.
Vault KV v2 values MUST select an explicit JSON string field. Other stores MAY
select `utf8` or `json-string-field`; duplicate JSON keys, empty values, invalid
UTF-8, NUL and line breaks in the selected value MUST fail.

The resolver MUST freeze configuration, enforce profile locator prefixes, bound
backend initialization and reads, and honor cancellation even for cached values.
Defaults are 10 seconds, 64 KiB per value and 300 seconds maximum cache TTL.
A reference's cache TTL defaults to zero. Secret-store failures MUST redact remote
error text and MUST NOT return a cached value after a failed refresh. Secret bytes
MUST NOT be serialized or included in formatted resolution metadata. The worker
MUST keep credential values and locators out of guest data and campaign evidence.
SDK read-only retries remain bounded by the resolver deadline; model generation
retries are governed separately and remain disabled.

Tests exercise all backend request/selection rules with synthetic peers, including
Vault over local TLS, sink rotation, Proxy mode, checksum failures, redaction and
cancellation. They do not establish cloud connectivity or live workload identity
qualification. Provider wiring and lifecycle audit remain separate integration work.
