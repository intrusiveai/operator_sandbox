# Integration failure propagation

Operator MUST preserve producer-selected failure facts from external integrations
through execution, owned cleanup, durable completion, inspection, and export.
Execution states and retry restrictions MUST remain independent of diagnostic text.
This specification changes host diagnostics, not either shared integration contract.

## Common implementation

- `internal/diagnostictext.Failure` MUST carry the component, operation, request
  identity, category, and available native status, code, message, request ID and
  response digest. Native error codes MUST remain data; unfamiliar codes MUST NOT
  require a new reporting message or catalogue entry.
- Producers MUST select fields explicitly. Arbitrary wrapped errors, response
  bodies, headers, credential values and endpoints MUST NOT become diagnostic
  messages. Selected remote text MUST use common redaction and bounds and be
  presented as quoted reported data. Omitted or bounded fields MUST be identified.
- `DiagnosticFailure()` and `DiagnosticMessage()` MUST preserve useful facts at
  their owner. Wrapping MUST preserve `errors.Is` and `errors.As`. Generic handling
  MUST follow the primary member of joined errors, never select a cleanup error
  merely because it has more descriptive text.
- Existing provider and credential diagnostics SHOULD retain their specialized
  facts. Common context and rendering MUST accept producer-owned safe descriptions
  without introducing central recognition lists for product error types.

## Interceptor exchange boundary

- Native state, lifecycle and recovery exchanges MUST use `interceptor.Call` for
  interpretation, retention and error propagation. The native-step executor is
  the separate existing dispatch transaction boundary and MUST capture the same
  failure representation centrally for all of its adapters.
- The shared boundary MUST retain the original failure ahead of any subsequent
  evidence-recording failure. A retention failure MUST be reported separately.
- Decoders MUST preserve valid remote rejections separately from invalid success
  responses. Native nested and top-level error envelopes MUST use one extractor.
- Operation-specific interpretation MUST remain explicit: a tolerated not-found
  response may complete deletion; 202, 5xx and unknown-effect responses MUST NOT
  become proof of non-execution. Capturing a native error MUST NOT authorize a
  retry, replay, restore, or continued execution.
- Snapshot creation, snapshot queries, restore, attachment, status and preflight,
  attempt steps, explicit injection deletion, closure, stop, recovery and evidence
  collection MUST retain available native causes. Status readiness failures MUST
  preserve an explicit Interceptor session failure when one was supplied.
- New native call sites MUST pass the architectural boundary regression check.

## Persistence and reporting

- A failure that closes execution MUST produce a host workflow diagnostic and an
  existing failure-trigger reference before shutdown. The in-memory copy MUST
  remain available to independent start-completion persistence if journal writing
  fails. Recoverable rejections MUST remain history without setting that trigger.
- `workflow.diagnostic` MUST retain the bounded `integration_failure` and whether
  it was terminal. Raw native response bodies remain subject to existing visibility
  rules; exporting diagnostic facts MUST NOT broaden Harness feedback permissions.
- CLI, completion receipts, inspection, exported events and reports MUST use the
  same retained diagnostic. Forced Harness termination MUST remain supporting
  evidence when Operator retained the initiating operation failure.
- Cleanup results MUST preserve separately attributed action failures, including
  Interceptor lifecycle calls and host container, transport and input cleanup.
  Recovery results and later evidence collection MUST retain their own failures
  without rewriting original execution history.
- HTTPS target failures MUST use the common diagnostic path with their logical
  operation, observed HTTP status, failure stage and safe credential-preparation
  cause when available. Unrestricted target bodies and transport errors MUST
  remain private. Model-provider diagnostics MUST retain their existing richer
  fields and primary-cause handling.
- Older retained records without these optional fields MUST remain readable.
  Missing details MUST NOT be inferred from exit codes or elapsed time.

## Verification

Controlled tests MUST cover unfamiliar native codes, both error envelopes,
redaction and bounds, primary/secondary persistence failures, status rejection,
known and unknown attempt outcomes, snapshot failure, cleanup and later collection.
A representative workflow MUST use actual Operator transport and journals, followed
by independent CLI inspection and report/export. Tests MUST demonstrate no replay
and no disclosure of private native details to a black-box Harness. Worker tests
MUST keep forced exit secondary to the retained native failure. No paid model calls
or live cloud campaign is required for these checks.

## Native evidence verification

Operator MUST verify filesystem ownership using the approved Interceptor OpenAPI
contract and native JSON serialization. It MUST preserve field order and omission
rules in hashed structures, verify ownership-aware manifests and state journals
(including retained restore sources), and continue verifying snapshot, blob and
session bindings. Historical manifests without ownership MUST remain readable
under their historical schema; verification MUST NOT invent an owner.

Local archive/provenance rejection MUST identify Operator and the local check;
it MUST NOT be attributed to Interceptor's export operation. Remote export
rejections MUST retain their original remote attribution. Validation producers
MUST supply their actual failed constraint and available member/record/field
location through the existing safe diagnostic interface. Collection and reporting
MUST forward those facts without a reason-to-message lookup table. Diagnostic
bounds/redaction MUST remain effective, and wrapped causes and the existing
provenance classification MUST be preserved.

Verification MUST cover native exporter fixtures for original, checkpoint and
restored evidence, invalid owners/versions/hash changes, and collection followed
by independent CLI journal inspection and report/export. Contract comparison MUST
check native field ordering and omission rules as well as types and representative
serialized records. Private supplied exports MUST be verified unchanged and MUST
NOT be committed as public fixtures.
