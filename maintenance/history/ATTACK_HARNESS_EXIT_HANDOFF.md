# Attack Harness handoff: finish-to-exit and fatal diagnostics

> Historical engineering record. This captures the instructions and evidence at
> the time it was written; it is not the release integration contract.

Implement a focused correction using the existing Operator–Harness contract.
Do not add a completion handshake, retry a finished campaign, or send another
ordinary operation after accepted `engine.finish`.

## Confirmed gaps to inspect

At the time of this handoff, `src/attack_harness/entrypoint.py:main` catches
`RuntimeStopped` and returns 1 without emitting its cause. It also catches
`OSError`, `RuntimeError`, `ImportError`, and `ContractError`, closes transport,
and returns 1 without emitting the original exception. Operator cannot recover
those discarded details.

`src/attack_harness/runtime.py` marks the runtime stopped and closes transport
immediately after validating an accepted finish response. This close is expected.
Audit adaptive-loop, finalizer, dispatcher, and entrypoint code to ensure a later
tick, budget read, context-manager exit, or redundant close does not convert this
successful finish into `RuntimeStopped("closed")` or replace a genuine exception.
Do not assume this explains QA run `aws-openai-gpt5-mini-010`: that run retained no
exit code or original worker cause, so its exact failure cannot be reconstructed.

## Required behavior

1. After an accepted finish response, unwind once and exit zero for a healthy
   Harness execution. Keep unsuccessful/untested assessment outcomes distinct
   from a runtime crash. A rejected finish is not acceptance; allow documented
   correction and resubmission using new operation identities.
2. Preserve nonzero exit for genuine fatal errors, even after finish acceptance.
   A transport close caused by deliberate successful shutdown is not a fatal
   error. Do not broadly swallow unrelated I/O/protocol failures.
3. Emit one concise, bounded causal diagnostic to stderr before nonzero exit,
   including stage, stable reason, exception class, and a safe specific message
   when available. Flush stderr. Preserve the primary error if closing transport
   or emitting diagnostics also fails. Do not print raw tracebacks with local
   variables, prompts, request/response bodies, credentials, URLs, or configuration.
   If a field cannot safely be emitted, state its omission and reason.
4. Preserve the original exception/reason when runtime wrappers currently replace
   it with `protocol-error` or `closed`; keep sensitive values out of the message.
5. Before finish acceptance, use existing `engine.diagnostic_report` where its
   current schema expresses the stopping condition and transport still works.
   Do not invent new fields or send this operation after acceptance. Stderr is
   the fallback for pre-bootstrap failures and post-finish crashes. Operator now
   retains bounded, sanitized stderr before container removal.
6. Do not issue model requests, target calls, additional finish calls, or automatic
   retries during exit. Keep the existing five-second exit requirement.

## Verification

Use the production entrypoint/runtime with controlled providers and a local peer:

- Rejected assessment, corrected accepted finish, deliberate transport close,
  clean unwinding, process exit 0.
- Accepted finish followed by a deliberate fatal exception: process exit nonzero,
  original safe cause on stderr, no additional ordinary request.
- Failure before bootstrap and during runtime, including RuntimeStopped; stderr
  retains stage/reason instead of silently discarding it.
- Close/diagnostic-write failure does not replace the primary error.
- Synthetic secrets and unrestricted prompt/response text never appear in output.
- Independent failures remain failures; no broad exception-to-success conversion.

Run matching Operator process integration after these tests. Report the exact
Harness source pin and observed process exit codes. Do not run paid model or cloud
campaigns without separate authorization.

Operator's correction adds host-side exit observations only; it does not change
the shared wire schemas, package version, or pinned contract digest. See
[Operator exit semantics](../../docs/HARNESS_EXIT.md) for retention and inspection behavior.
