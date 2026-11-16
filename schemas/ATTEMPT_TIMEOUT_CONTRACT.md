# Attempt execution and reply deadlines

`engine.attempt_execute` MUST use the host-configured request wait supplied in
EngineContext, as specified in [operating limits](OPERATING_LIMITS_CONTRACT.md).
Harness MUST NOT substitute a registry constant. A caller MAY request less time.
Exact shared package identity checks MUST reject incompatible components before execution.

Operator's administrator target profile `operation_timeout_ms` MUST be within the global installation
`target.max_operation_timeout_ms` ceiling (default 120000; configurable in
1..1800000, the supported maximum campaign lifetime) and MUST bound the **complete attempt**: input retention, preflight,
setup, target invocation, observations and injection cleanup. Every native step
MUST inherit the same absolute execution deadline. Operator MUST also honor the
earlier caller deadline and remaining campaign lifetime; no step resets a budget.

For multi-step targets, configure the native target turn to 60 seconds and the
Operator attempt to 90 seconds, setting the host-configured Harness wait to
120 seconds. These are operating recommendations, not automatic configuration or
an increase to a native target's own turn limit. Operator MUST NOT modify the
target turn setting. Shorter request/campaign limits can remove reporting headroom.

An attempt result MAY contain `timeout`, with these required bounded facts:

- `native_operation`: the installed native operation in progress.
- `attempt_allowance_ms`: the configured complete-attempt allowance.
- `elapsed_ms`: elapsed monotonic attempt processing time.
- `deadline_source`: `attempt`, `request`, or `campaign`, whichever limits execution.
- `observation_collection`: `not-started`, `incomplete`, or `complete`.

The surrounding result MUST retain its `request_id`, `attempt_id`, stage and
`invocation_state`. Successful invocation MUST remain `succeeded` even when
observation collection fails. A lost native response MUST retain its uncertainty;
a timeout diagnostic does not justify inventing a known native result. Neither
failed nor unknown attempts may be replayed. The existing receipt, integrity,
visibility and execution-admission rules remain applicable.

On timeout, Operator MUST close execution admission immediately. It MAY drain the
existing terminal response for at most two seconds, bounded by the original
request and campaign deadline. This window MUST NOT authorize another invocation,
renew a deadline, or delay an independent cancellation, protocol, storage or
product failure. Owned cleanup MUST remain bounded independently of the expired
execution context. A returned result is not a promise that cleanup has completed.

Operator MUST retain the causal timeout in its journal and completion diagnostic,
and expose it through inspection, CLI, reports and exported evidence. Host-only
`result_delivery` MUST distinguish `published` (complete FIFO write or atomic spool
publication, not proof of guest consumption), `unavailable`, and `unconfirmed`.
A later Harness exit diagnostic MUST NOT replace the known Operator timeout.
Older retained records without these optional facts MUST remain readable.
