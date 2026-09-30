# Administrator run and diagnostic workflows

## Combined run

```sh
operatorctl run --bundle ./scenario-bundle.json --environment ./environment \
  --output ./runs/example --wait
operatorctl inspect --run ./runs/example
operatorctl doctor --config /absolute/config.yaml
operatorctl doctor --config /absolute/config.yaml --offline
```

`run` MUST reuse the submission validator, durable start request, campaign worker
and observer implementations. It MUST accept the same bundle/artifact/target
selectors as `submit` and prompt/skill selectors as campaign start. It MUST default to foreground execution using the same worker as optional
`--service` execution. Foreground cancellation MUST stop that execution and perform
cleanup. `--wait` is redundant for new foreground execution; with `--service`
it attaches a read-only observer. `--timeout` bounds service acceptance
(default 135 seconds, maximum ten minutes); `--wait-timeout` bounds the optional
completion observer (default 35 minutes, maximum 24 hours).

Both `interceptor/v1` and `https/v1` target profiles MUST use this workflow.
For HTTPS profile creation and capability export, see [HTTPS targets](HTTPS_TARGETS.md).

A new output MUST follow normal submission publication. An existing output MUST
be fully revalidated and match the newly supplied inputs and explicit profile
selection exactly. It MUST NOT be overwritten or repaired. Matching repeated
invocations MUST reuse the saved start, its one-time claim and identities.
`--new-campaign` MUST be explicit to replace a saved or dangling start selection;
it MUST NOT bypass submission validation or erase earlier starts.

`run` MUST emit one `operator.dev/run-receipt/v1alpha1` JSON object with the absolute
run lookup directory, status and available submission/start/observation receipts.
Stage receipts MUST NOT be interleaved with that object. Human diagnostics MUST go
to stderr. Failure after start publication MUST preserve its lookup key whenever
the underlying start operation returns one. An uncertain service response MUST
remain a failure, never trigger a replacement execution. Closing or canceling
`--service --wait` MUST only end observation; it MUST NOT terminate the worker. A status of
`accepted` does not establish experiment success or ongoing worker health;
`execution_closed` describes retained closure, not scenario success. The worker
owns finalization and report publication.

## Read-only inspection

`inspect --run DIR` or `inspect --campaign ID [--state-root DIR]` MUST use exactly
the existing campaign-status observer and return its bounded observation receipt.
It MUST verify a captured committed journal prefix and distinguish terminal records
from unknown live execution state. It MUST NOT repair journals, resolve credentials,
contact a provider/target, claim starts or resume execution. A submitted run with
no start yet MUST be checked with `validate --run DIR`; missing/dangling start links
MUST remain explicit inspection failures. Existing observer configuration and
cancellation rules apply unchanged.

## Doctor

`doctor` MUST emit `operator.dev/doctor/v1alpha1` JSON with ordered checks, fixed
status codes and actionable fixed remediation identifiers. It MUST validate private
configuration, installed contract, target/model profiles, credential reference
configuration, state directory ownership/permissions and supported host architecture.
An omitted default target profile MUST be marked `not_checked` with guidance to
select an explicit submission target. It MUST NOT print configuration contents,
locators, raw errors or private endpoints.

By default it MUST also perform bounded, read-only local Docker daemon/image
inspection using the configured endpoint and native image platform. `--offline`
MUST mark that check `not_checked`. It MUST NOT pull images, create containers,
attach targets, resolve secrets, generate model requests, create state directories,
install services or perform privileged repair. Live provider/target/runtime
qualification MUST remain explicitly `not_checked`; passing these installation
checks does not confer qualification or campaign admission.

Commands MUST return 0 on successful requested checks/observation, 1 on operational
failure or canceled/uncertain execution observation, and 2 on invalid arguments.
