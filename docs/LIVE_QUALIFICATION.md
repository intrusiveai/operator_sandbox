# Live provider and secret-store qualification

Qualification MUST use explicit administrator-selected nonproduction resources.
`operatorctl qualify --plan /absolute/private/plan.json` validates configuration
locally without initializing cloud identities or sending service requests.
Plans MUST be private regular files, use the version below, and contain absolute
paths to existing private provider/credential configurations. Unknown fields and
unsupported authentication combinations MUST be rejected.

```json
{
  "api_version": "operator.dev/live-qualification/v1alpha1",
  "case_id": "vault-proxy-01",
  "kind": "secret-store",
  "declared_auth_mode": "proxy",
  "credentials_file": "/absolute/private/credentials.yaml",
  "credential_id": "qualification-secret",
  "timeout_seconds": 60
}
```

Evidence MUST allowlist metadata and fixed outcome codes. It MUST exclude values,
locators, configuration paths, raw remote exceptions, prompts, native responses,
and authentication headers. Case IDs MUST be nonsensitive administrator labels.
`declared_auth_mode` is attribution supplied by the administrator; it MUST NOT be
presented as proof that an SDK selected that identity mechanism. Preflight success
MUST NOT imply live qualification. Release source/contract identity fields MUST
identify the executing build; empty development-build fields leave that identity
unqualified.

Live execution tooling and detailed service setup are being completed. No live
provider or secret-store combination is qualified by the local preflight tests.
