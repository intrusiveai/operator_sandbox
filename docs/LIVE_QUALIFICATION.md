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

No live provider or secret-store combination is qualified by local preflight or
controlled fixture tests.

## Explicit secret-store execution

Add `--live --output /private/evidence/new-case.jsonl` to run a validated plan.
The output MUST be new (never overwritten), is created at mode 0600, and MUST be
synced before each remote operation. Evidence-write failure MUST stop further
operations. An interrupted `started` record without a corresponding result MUST
remain unresolved; a later invocation MUST use a new evidence file, not replay it.
The total plan deadline is 1–600 seconds. SIGINT/SIGTERM cancel the run.

Secret probes MUST use the production resolver and audit cache signal. They cover
initial resolution, cached reuse when enabled, pre-canceled resolution (including
cached values), explicit invalidation/refresh, and natural TTL expiry if the time
budget leaves at least ten seconds for the refresh. Each omitted check MUST be
recorded as `not_run` with its reason.

Optional `failure_credential_id` selects a separate nonexistent/denied test secret
in the same store profile. It tests failure without a returned value; a generic
resolution error does not identify whether a store denied access or was unavailable.
Optional `rotation_wait_seconds` (1–300, less than the plan deadline) opens a window
**after cache checks** for an administrator to change the selected test secret.
Watch for the `rotation/started` record. The probe then invalidates its cache and
requires a different value. It MUST NOT record either value or its hash, and MUST
NOT write, delete or rotate any remote secret itself. This check does not prove
workload-token renewal or a changed Vault token sink; those need separate fixtures.

Authentication labels accepted for secret probes:

| Store | `declared_auth_mode` |
|---|---|
| AWS Secrets Manager | `web-identity`, `container-role`, `instance-role` |
| Azure Key Vault | `workload-identity`, `managed-identity` |
| Google Secret Manager | `external-account`, `metadata-identity` |
| Vault | `token-sink`, `proxy` |

Read [credential configuration](CREDENTIALS.md) for the existing private profile
format. The plan MUST select an existing credential; preflight does not resolve it.
Pre-canceled calls MUST NOT be labeled as in-flight cancellation qualification.
Actual in-flight faults and SDK identity selection require separate live evidence.

## Provider conversation probes

```json
{
  "api_version": "operator.dev/live-qualification/v1alpha1",
  "case_id": "provider-route-01",
  "kind": "provider",
  "declared_auth_mode": "secret-store",
  "model_profile_file": "/absolute/private/model.json",
  "credentials_file": "/absolute/private/credentials.yaml",
  "timeout_seconds": 120,
  "maximum_model_calls": 3,
  "maximum_output_tokens": 1024
}
```

A provider probe MUST make at most three potentially billable generations, with
no retry. This plan's output cap MUST be positive and no greater than the selected
profile's cap; it MUST also accommodate any selected thinking budget. Input is a
fixed harmless prompt plus a single package-owned `snapshot_list` declaration.
No target or campaign is created and no returned tool is dispatched. The simulated
tool result contains an empty snapshot list. Models that refuse, truncate, omit
usage, return malformed calls, or choose an unexpected disposition MUST fail the
probe; the tool MUST NOT spend extra calls trying to persuade them.

The three exchanges MUST cover text, a single tool call after that text, and text
after the simulated tool result. Every request/result MUST pass the production
shared codec validator, including model aliases, token accounting, tool correlation
and native continuation. In-memory native history MUST retain opaque provider
reasoning blocks. Only validated numeric usage MUST enter the evidence. Exceeding
the profile's declared prompt-token allowance MUST stop subsequent calls; this
allowance is a checked reservation, not a provider-side input-token limiter.

The tool MUST test pre-canceled generation separately. A generation transport error
MUST be recorded as `uncertain` and MUST end the probe without retry. This does not
establish that a provider stopped remote computation or billing. Controlled
in-flight cancellation and ambiguous-outcome qualification MUST remain `not_run`
unless separately demonstrated with fault-injection evidence.

Provider `declared_auth_mode` MUST be `secret-store` for key-based profiles.
Workload profiles omit `credentials_file` and use AWS (`web-identity`,
`container-role`, `instance-role`), Azure (`workload-identity`, `managed-identity`),
or Vertex (`external-account`, `metadata-identity`) labels as applicable.
See [model profiles](MODEL_PROVIDERS.md) for provider/codec/endpoint selection.

A successful command means the selected automated checks completed. It MUST NOT
be interpreted as blanket qualification of a provider, model family, authentication
mechanism or untested fault case. Missing results remain unqualified.
