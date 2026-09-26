# Campaign service foundation

Status: implemented in `internal/campaignservice`, with integration tests in
`internal/preparation`. This host library connects frozen preparation, ordinary
attempt/feedback/cleanup dispatch, transport and terminal handling. Campaign start
CLI, Docker creation/bootstrap and the remaining ordinary routes are subsequent
implementation stages. [Snapshot/restore coordination](SNAPSHOT_SERVICE.md) is now
implemented on this foundation.

## Construction and admission

The host MUST supply adopted `preparation.Stored` inputs, the native client, Docker
runtime, private state root and the saved exact Docker binding. Construction MUST
verify the campaign's persisted manifest/container binding and freeze caller-owned
identity fields. The service MUST use one attempt ledger and one ordinary gate.

The service supports `engine.attempt_execute`, `engine.observation_read`,
`engine.injection_delete` and all four snapshot/restore routes. Construction MUST
reject a context advertising other
routes. Dispatch MUST reject unadvertised operations before target effects. This
restricted service is not a complete Attack Harness session yet.

Admission MUST validate the complete five-message startup transcript, actual input
bytes and all RunManifest pins, recheck native identity/readiness and the exact
running Docker container, and durably record admission. The enclosing launcher MUST
also verify the staged filesystem and independently approve the image/release; this
library's transcript validation cannot establish those runtime facts.

## Live execution and transport

Before compiling an attempt or deleting a retained injection, the service MUST
obtain current native `session.status`, check capability/environment/application
pins and the administrator's target-stop permission, and use its native mutation
revision. Campaign run_revision MUST remain a separate attribution value. Artifact
bytes MUST be read from verified retained content; parent attempts and known turns
MUST come from successful journaled native operations in the current session.

The service MUST retain the exact received envelope and reserve response storage
before broker effects. It MUST retain the correlated response before handing it
to transport. Journal failure or uncertain native effects MUST close execution.
The broker MUST preserve cumulative admission/read accounting and duplicate rules.

`ServeOrdinary` MUST have one owner per service. It receives the guest's
`ordinary-out` lane and sends replies through `ordinary-in`, starting host sequence
numbers at zero. It MUST carry the transport's original absolute operation deadline
into dispatch and tighten it by the private profile's operation timeout. Physical
FIFO/spool pumping and control consumption MUST run
independently; an ordinary request MUST NOT block urgent termination. The launcher
MUST open transport admission only after service admission succeeds. Spool cleanup
MUST follow confirmed container exit through the existing transport cleanup API.

## Independent termination and bounded cleanup

Construction MUST arm the existing Docker termination observer before admission.
The observer MUST use the saved endpoint, daemon and full container identity and
remain independent of the ordinary gate and journal writer. An absolute campaign
timer MUST enforce the prepared remaining time, capped at 30 minutes for the MVP;
an explicitly supplied earlier deadline MUST tighten it. A one-second status
watcher MUST close execution on lost readiness, changed binding or failed status
queries, even while no ordinary requests arrive.

Terminal handling MUST fence guest admission immediately and cancel in-flight
ordinary work. Docker termination MUST proceed independently of native closure
and reporting. The host controller MUST use these bounds:

| Resource | Bound |
| --- | --- |
| Terminal audit reservation | 70 MiB reserved within the campaign journal budget before admission. |
| Native closure/cleanup/optional target stop | One shared 30-second deadline, including ordinary-gate drain. |
| Confirmed injection cleanup | At most 64 unique injection IDs per terminal pass. |
| Retained native status/terminal response | At most 256 KiB each. |

The controller MUST journal native closure intent before dispatch and confirm
closure before deleting injections. Cleanup MUST use only IDs from successful
journaled `injection.arm` commands in the same campaign. Historically deleted IDs
remain candidates because a restored checkpoint can reintroduce them. Unknown arm
outcomes MUST NOT be treated as confirmed handles. Only a typed deletion success
or a bound `not_found` response establishes absence; an uncertain deletion MUST
end that cleanup pass without a new dispatch ID or retry.

The controller MUST record confirmed cleanup count, remaining known candidates
and whether completion was confirmed, bounded or uncertain. `unconfirmed` before
inventory means the remaining count is unknown, not proof of an empty target.
It MUST issue target lifecycle stop only when the frozen administrator profile
permits it, and record its outcome separately from harness Docker termination.
After failed early checks, skipped cleanup steps MUST remain unconfirmed and MUST
NOT reopen execution.
Callers MUST treat successful `Wait` as completion of the controllers, not proof
that every outcome was confirmed; they MUST inspect both returned result records
and writer failure state. A caller's wait timeout MUST NOT cancel the Docker observer.

## Validation and next boundary

Tests join real preparation, manifests, journals, the ordinary broker and physical
file-spool transport to a scripted native peer and Docker termination interface.
They cover the startup transcript, sequence zero, live revision progression,
verified parent lineage, unadvertised routes, frozen identity, idle deadlines,
native status loss, blocked ordinary work, closure uncertainty, confirmed-injection
cleanup and lost deletion replies. These tests do not qualify Docker or a live
Interceptor/Attack Harness deployment.

The next service stage MUST implement artifact uploads,
model relay, records/conclusion/stop and their accounting before a complete campaign
can be launched. Reporting/provenance validation, Docker launch/control orchestration,
OS service lifetime and runtime qualification remain required follow-on work.
