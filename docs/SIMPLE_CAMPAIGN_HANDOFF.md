# Simple campaign contract: coordinated Harness handoff

## Required matching builds

The current identity and coordinated update are defined in
[Explicit attempt origin](EXPLICIT_ORIGIN_HANDOFF.md) and
[release/contract-lock.json](../release/contract-lock.json). The original implicit-origin
package is incompatible with this update. Use shared package 0.2.0; the simplified
input and finish behavior below remains unchanged.

## Harness implementation checklist

1. Consume the exact package in the lock and run its Go/Python fixtures. Replace
   model-facing bookkeeping/descriptor assembly with the new `attempt_execute`
   arguments. `origin`, target `operation_id` and `input` are required. Scenario origin requires
   a submitted `scenario_id`; exploratory origin forbids it. Runtime still
   assigns the outer operation ID, call ID, binding and transport sequence.
2. Use the shared `resolve_direct_input` / `ResolveDirectInput` semantics: JSON,
   UTF-8, canonical base64, or committed artifact receipt. Direct decoded bytes
   are limited to 32,768 and JSON depth to 16; the entire attempt request is 64 KiB.
   A supplied parent is an execution receipt, not a host attempt ID. Never repair
   invalid supplied arguments or fabricate receipts.
3. Send `finish` with `finish_reason` plus exactly one assessment, committed
   assessment-draft artifact receipt, or unavailable reason. Remove mandatory
   conclusion publication, conclusion record and separate stop calls. Assessment
   drafts omit binding/finish metadata. They require summary and objectives;
   optional arrays default empty. `normalize_assessment` validates these defaults
   and local hypotheses using the current known binding, without registering history.
   Receipt category/membership/visibility checks still require runtime state and
   authoritative host validation. Supported outcomes require execution evidence.
4. Treat accepted finish as closed execution with cleanup **pending**, then exit
   within five seconds. Retain duplicate operation identity only for delivery of
   the same request. Never retry uncertain effects or resume interrupted work.
5. Use the shared operation-error format for local malformed calls, with
   `INVALID_ARGUMENTS`, no effects, correct-and-resubmit, and the shared diagnostic
   whose source is `harness-local`. Preserve `ContractError.reason`, `field_path`
   and correction. Diagnose duplicate JSON keys before normal decoding. Tool/call
   identity stays in the provider's correlated tool message; rejected calls never
   receive invented execution receipts. Host errors retain their original code.
6. On local stopping conditions send runtime-only `engine.diagnostic_report`, with
   safe reason/path, last tool/call and observed/configured counts. Then finish with
   an honest assessment or explicit unavailable reason. Never raise loop limits
   to compensate for errors. Operator stores selected safe facts and the original
   report digest, not supplied diagnostic prose. Stopping reports remain attributed
   to Harness; host dispatch and cleanup records establish execution facts.
7. Use the shared provider projections for all five codecs. Injection branches
   are hidden only when the operation catalog lacks injection cleanup capability.
   Full runtime validation enforces restrictions omitted from provider projections.
   Add the requested trusted operating guidance; it must explain direct input,
   real returned handles, evidence inspection, rejected submissions and honest finish.
8. Keep optional injection, snapshot/restore and historical records. Restore still
   skips the rest of that model-response batch. The Harness must retain conversation
   state and adopt the replacement revision before another tool call.

## Integration and evidence

`make test` now includes a controlled Python protocol peer against the Go service
through real file-spool boundaries, followed by separate CLI inspection/report/export.
It verifies direct execution → evidence read → finish, duplicate finish, correction
of a rejected call, and zero-dispatch invalid-call exhaustion. It uses a local native
peer, no Docker launch, model calls or cloud writes. This is not the production Harness.

The opt-in production-Harness fixtures load the matching shared package into the
sibling Python entrypoint and supply controlled model responses. Run:

```sh
make test-integration
```

That gate covers the production Python entrypoint, FIFO/spool, prompt/skill/input
loading, optional artifacts/history/injections, restores and failure paths. Its
results and limitations are recorded in
[explicit-origin verification](qualification/explicit-origin-evidence-2026-10-19.md).
The separate Harness session must still update its prompt guidance, pinned package
and image build. Native Docker containment and live providers remain separate
qualification.

Measured all-tools OpenAI Chat schema size decreases from 58,000 to 39,767 bytes
(31.4%). Required attempt fields decrease from seven to three (plus scenario_id for scenario work). Finish requires a
reason plus one content/unavailable alternative, replacing nine required model
assessment fields and the multi-step runtime completion. A small ordinary run
requires three host calls: attempt, evidence read, finish. Formerly it required
at least ten (input begin/part/commit, attempt/read, conclusion begin/part/commit,
record and stop); multipart artifacts may require more.
