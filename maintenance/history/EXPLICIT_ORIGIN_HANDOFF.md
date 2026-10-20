# Explicit attempt origin — coordinated Harness update

> Historical engineering record. This captures the instructions and evidence at
> the time it was written; it is not the release integration contract.

## Exact approved contract

Consume Operator shared package **0.2.0**, source commit
`850fa97624171a24189084964859893a731bdc4e` (tree
`bf4232c445c748bc9afca83928c14d9d6ceb0259`). Its exact package digest is
`sha256:057c4a53997b94bcd8ae18012daca4ec4610cd26abab9f32f748867362b422ee`.
[release/contract-lock.json](../../release/contract-lock.json) is the build source of
truth. A local verified package is available at
`/private/tmp/operator-explicit-origin-contract-0.2.0`.

Operator branch `fix/explicit-origin-evidence` targets `candidate-v0.1.0`.
Make the matching Harness change on a branch targeting its candidate. This is a
breaking contract update; do not publish releases or evaluate paid cloud models.
Product release version 0.1.0 remains valid with the new contract identity.

The authoritative request schema is engine-attempt-request:v1alpha4:

```json
{"origin":"scenario","scenario_id":"configuration","operation_id":"invoke","input":{"encoding":"json","value":{"prompt":"Reply exactly QA_CONFIG_OK. Do not call tools."}}}
```

- Every attempt MUST include origin, exactly scenario or exploratory.
- Scenario origin MUST identify a scenario in the submitted bundle.
- Exploratory origin MUST omit scenario_id, including null or empty values.
- Origin MUST NOT be inferred, defaulted or silently repaired.
- Scenario attempts may mutate inputs or follow up earlier attempts. Exploratory
  experiments do not establish execution coverage for an unnamed scenario.
- The ordinary workflow remains direct input → observation read → one finish.
  No hypothesis preregistration, small-input upload, model bookkeeping or separate
  conclusion-record/stop sequence is introduced.

## Harness implementation

1. Consume the exact Operator package, schemas, provider tool projections and
   shared `fixtures/attempt-origin.json` rather than maintaining parallel rules.
   Go `ValidateAttemptOrigin` and Python `simple.validate_attempt_origin` implement
   the same relationship checks; passing the submitted scenario set also checks
   membership. The standard wire/tool validators perform the context-free checks.
   Operator additionally checks membership before input retention or dispatch.
2. Keep origin/scenario unchanged through the ordinary attempt handler. Update
   trusted operating guidance to explain that scenario_id is required for scenario
   work, even if the input is exploratory in the everyday sense of trying a mutation.
   Use exploratory origin only for work outside a submitted scenario.
3. Let the shared diagnostics identify origin_required at /origin, or
   scenario_required/scenario_forbidden/unknown_scenario at /scenario_id. Use the
   existing INVALID_ARGUMENTS error, effect_state none, correct-and-resubmit and
   diagnostic fields. Never return an execution receipt for a rejected submission.
   A corrected call gets a new operation ID; duplicate delivery of an executed
   operation retains its original identity/result and cannot execute twice.
4. Update Harness's contract lock, build fixtures, runtime config and embedded
   compatibility/manifest identities through the existing build mechanism. Older
   0.1.0-package images MUST fail Operator's existing exact-package check before
   execution. Do not add negotiation, fallback or another compatibility mechanism.
5. Test all five provider projections and the shared fixtures, explicit scenario
   and exploratory attempts, each invalid combination, correction without replay,
   and direct invocation → permitted observation → accepted finish → exit 0.

Rebuild the package from the pinned source in an isolated checkout with:

```sh
go run ./cmd/operatorctl contract build --source "$PWD" \
  --output /tmp/operator-origin-contract --package-version 0.2.0
go run ./cmd/operatorctl contract check --package-dir /tmp/operator-origin-contract \
  --package-version 0.2.0 \
  --package-digest sha256:057c4a53997b94bcd8ae18012daca4ec4610cd26abab9f32f748867362b422ee
```

Use a new output directory. Run packaged Python tests with
PYTHONDONTWRITEBYTECODE=1 to preserve the exact inventory.

## Integration boundaries

The separate Harness session owns its implementation, prompt guidance and image
build. Operator's controlled process tests can load the matching package into the
production Harness entrypoint without modifying its source. This verifies supplied
model calls and actual transport behavior; it does not prove a live model will
choose the correct origin or qualify a published image. Native Docker lifecycle
and the full production process tests must be reported separately when using
separate fixtures. No external QA recipe pass may be inferred from this work.
