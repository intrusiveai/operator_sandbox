# Explicit attempt origin and retained evidence — controlled verification

> Historical engineering record. This captures the instructions and evidence at
> the time it was written; it is not the release integration contract.

## Contract and scope

Operator branch `fix/explicit-origin-evidence` targets `candidate-v0.1.0`.
The agreed breaking contract is package 0.2.0, pinned by
`release/contract-lock.json`; see `docs/EXPLICIT_ORIGIN_HANDOFF.md`.
Explicit scenario origin requires a submitted scenario ID; exploratory origin
forbids it. No implicit-origin fallback, hypothesis registration, mandatory small
input upload or additional completion handshake is introduced.

## Verification performed

- Shared Go/Python validators and fixtures cover valid scenario/exploratory origin,
  missing/invalid origin, missing/unknown scenario and forbidden/null scenario.
  Generated declarations for all five provider codecs retain required origin;
  OpenAI parameters remain object schemas without forbidden root combinators.
- Service regressions verify rejection before target dispatch, corrected requests,
  duplicate deliveries, and changed content under an executed operation identity.
  Only one target invocation occurs in each correction/duplicate case.
- Actual export generation preserves retrieved, cached and failed audit events for
  all four supported secret backends, with only selected fields. Synthetic values,
  locators, headers, raw responses and unrelated metadata are absent. These are
  controlled audit fixtures, not live secret-store qualification.
- Reporting checks cover uncovered scenarios, permitted output with digest-bound
  complete artifacts, accepted assessment interpretation, missing evidence, unsafe
  Markdown, bounded excerpts, and explicit omission of invalid credential fields.
- The production Python Harness entrypoint with the new shared package completed
  scenario, exploratory and corrected-invalid-call workflows over both FIFO and
  file spool. Controlled model replies led to one direct-input target invocation,
  observation inspection, accepted finish, OS process exit 0 and confirmed target
  cleanup. Reports/exports were generated from retained state. The test adapter
  relocates paths and replaces confinement; it is not production image qualification.
- On macOS ARM64/Docker Desktop Linux ARM64, a cached Python container and the real
  Operator worker/spool/Docker lifecycle exercised scenario invocation, observation,
  finish, exact exit 0, cleanup/removal and export. Separate cases verified success,
  corrected assessment rejection, nonzero with/without causal stderr, zero without
  finish, and cleanup failure. This Docker fixture is a controlled peer, not the
  production Harness image. The scenario fixture needed a 4096-byte observation
  allowance; its initial default allowance correctly rejected the evidence read.
- The completion/inspection process fixture now exercises a rejected assessment
  followed by a corrected, accepted finish with a fresh call identity. Both FIFO
  and spool retain the rejection in separate CLI inspection/status/report/export,
  without manufacturing historical assessment records or treating recovery as a
  terminal failure. Callback assertion failures return errors instead of exiting a
  service goroutine and leaving the test blocked. This replaces stale fixture
  expectations from the former multi-step completion workflow.
- Existing exact local compatibility checks reject the old implicit-origin package
  and the new explicit-origin package when paired with an incompatible host, even
  if a package is relabeled without matching its digest.

Local validation includes `make test`, `go vet ./...`, focused race checks and the
matching-package process integration gate. No paid calls, cloud writes, registry
pulls, release publication, campaign replay or historical source repair is involved.

## Historical run 011 re-export

A private temporary copy of retained campaign
`campaign-1d7c5a64494d245ea569af463b662b87` was exported with the updated code.
The original retained state was not modified. Inspection verified all 48 exported
file hashes/sizes and the unchanged journal head. The result preserves:

- One dispatched exploratory invocation of `invoke`, without a scenario ID.
- Required scenario `configuration` and its objective lack associated scenario
  execution coverage; no association was fabricated.
- Permitted target output containing `QA_CONFIG_OK`, linked to its complete retained
  artifact. This establishes a returned marker, not an external QA recipe pass.
- Three successful uncached AWS Secrets Manager resolutions for credential
  `model-key`, profile `qa-store`, at source events 31, 74 and 88, including timestamps.
- Accepted assessment, verified Harness exit 0 and completed cleanup.

The review artifact is `/private/tmp/operator-origin-run011-final/report.md`;
its sibling export inventory and JSONL events contain the supporting evidence.
This is an offline re-export of a historical cloud run, not a new cloud campaign.

## Remaining coordination

The separate Harness session must adopt the exact package, update trusted prompt
guidance and its build/runtime/embedded identities, and rebuild compatible images.
See the handoff for exact instructions. The controlled production-process test
loads the package explicitly and does not qualify a newly published image or
prove live-model selection behavior. Linux Docker FIFO containment and live
providers remain separate qualification work.
