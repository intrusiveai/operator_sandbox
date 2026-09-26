# Attempt, feedback and cleanup broker

`attemptadapter.Broker` implements the host routes for `engine.attempt_execute`,
`engine.observation_read` and `engine.injection_delete`. It validates installed
wire contracts, campaign/launch identity and channel admission, serializes work,
caps deadlines and validates correlated replies. Other operations return
`ErrOperation` to the enclosing dispatcher. Transport sequence tracking and its
original receive deadline remain the transport's responsibility.

## Execution and publication

Attempt observation precedes tactical validation. A rejected attempt consumes its
index, retains the rejection and makes no target contact. Admission charges once;
the broker reserves publication capacity before dispatch, executes the compiled
plan through the native journal and adopts the immutable receipt before replying.
Known failure and unknown outcomes retain their distinct results and close further
guest execution. A storage failure returns no publishable reply.

The host `Prepare` callback supplies a compiled plan and pinned live guard from
verified campaign artifacts, current native lineage, installed policy and live
session revision. The broker checks the submitted identity and deadline against
that plan. The callback must use the supplied absolute deadline and obtain a fresh
native revision; it cannot reuse an initial revision after native mutations.

## Reads and cleanup

The ordinary ledger shares the attempt ledger's operation namespace and maximum
unique submissions. Read requests reserve their requested maximum before byte I/O,
settle actual bytes before reply, and refund unused bytes for EOF, unavailability
or a known denial. Lost settlement retains the pending allowance and closes
execution. Counters do not reset on restore. Exact operation retransmissions replay
the retained result without a second charge; a new read operation pays again for
overlapping bytes. Guest model-tool and reference-read accounting remains an
independent requirement of the harness/service integration.

Receipt reads never contact Interceptor. They retain original source identities
and recheck current permitted kinds even on replay. Denied visibility cannot be
bypassed with a previously successful read ID. A replayed attempt result containing
newly forbidden entries is denied rather than rewritten.

Cleanup resolves a confirmed same-campaign attempt/action handle, checks current
capability/policy through the installed `Cleanup` callback, then submits one exact
native deletion to the current session with no original attempt-context fields.
Only native 204 or the bound operation's 404 `not_found` proves absence. A generic
transport 404, storage failure or malformed reply does not. The command and result
are journaled independently of the original immutable attempt receipt. An operation
cannot acquire a second native cleanup dispatch under a new native command ID.

Old cleanup IDs replay historical results after restore. A new cleanup ID contacts
the replacement target even if an earlier deletion succeeded, because restored
checkpoints may contain that injection. Worker and revision remain attribution,
not handle-access restrictions. Cleanup consumes no attempt admission.

## Service integration boundary

Construct one broker for the live campaign after immutable preparation. The service
must supply verified source callbacks and admission state, and drain ordinary work
across the entire restore transition before calling `Rebind`. This library does not
issue restores or attach to transport queues itself. Keep the independent fence
observer and absolute campaign timer outside the ordinary mutex.

The [campaign service](CAMPAIGN_SERVICE.md) now supplies the concrete preparation
callbacks, startup admission, exact envelope audit and transport queue integration.
Its separate post-closure controller uses confirmed handles, bounded deadlines
and pre-reserved audit capacity without reopening guest admission or bypassing the
ordinary executor's fence. It also wires native closure and independent Docker
termination. The [state service](SNAPSHOT_SERVICE.md) coordinates healthy restore
and rebinding. Remaining ordinary routes are the next service stage.

Tests use the real journal, compiled attempt adapter and scripted native peer.
They cover rejection numbering, duplicate concurrency, publication/settlement loss,
read budgets and narrowing, restored handle deletion, confirmed absence, uncertain
cleanup and failure fencing. They do not qualify a live Docker deployment.
