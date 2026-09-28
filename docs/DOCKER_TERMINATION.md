# Independent Docker termination

Status: implemented in `internal/dockercontrol`, `internal/termination`,
`internal/campaign/termination.go` and `cmd/operatorctl`. This stage provides the
administrative command and a terminal-fence observer. Container creation/start,
the campaign service and native execution now use this layer. Tests use a fake
Docker executable; real Docker and host-profile qualification remain pending.

The same restricted Docker subprocess wrapper now supports
[read-only local image preparation](IMAGE_PREPARATION.md). Saved-container
termination continues to use its recorded endpoint and exact container identity.

## Administrative command

Build and invoke the initial CLI:

```sh
go build -o build/operatorctl ./cmd/operatorctl
build/operatorctl campaign terminate \
  --campaign campaign-1 \
  --mode immediate \
  --reason user-request
```

The command loads the [installed host configuration](HOST_CONFIGURATION.md), or an
explicit `--config` file, for `state.root` and `docker.executable`. `--state-root`
and `--docker-bin` override these settings. An explicit state root without
`--config` bypasses default configuration for emergency recovery. Otherwise a
missing default file permits OS defaults; an existing invalid file fails.
The state-root defaults are `/var/lib/operator` on Linux and
`~/Library/Application Support/Operator/data` on macOS. An omitted executable is
resolved once from the administrator's `PATH`. Termination always uses the saved
campaign endpoint, regardless of the endpoint configured for new campaigns.

Each request receives a random 32-character lowercase hex request ID. An
administrator may supply `--request-id` to correlate retries. `--reason` is a
bounded host identifier, not arbitrary text. JSON output uses
`operator.dev/termination-receipt/v1alpha1` and separates:

- `outcome.confirmed`, `kill_attempted`, `state` and `code`: Docker observations.
- `intent_recording` and `result_recording`: `recorded`, `failed`, `unconfirmed`
  or `not_attempted`.
- Campaign, launch, request and exact Docker container identities.

Exit status is **0** only for confirmed stopped state with both records committed;
**1** means unconfirmed termination or incomplete recording; **2** means invalid
CLI usage or failed configuration loading. Recording failure can therefore produce a nonzero status even when
Docker exit is confirmed. Failed binding lookup does not fall back to names,
labels, current contexts or another daemon.

## Docker identity and exit confirmation

`ReadDockerBinding` reads the saved identity independently of the journal lock and
journal contents. The Docker client validates it again before use. All commands
carry the saved local `--host unix:///...` endpoint. Docker environment overrides
and proxy variables are removed from the child environment. No shell, user-supplied
Docker arguments, remote endpoint or mutable container name is used.

The control sequence is:

1. Read the daemon ID and compare it with the saved value.
2. Inspect the **full** container ID. Check its ID, image ID, campaign, launch and
   shared container labels. Inspect only the required identity, process state and
   lifecycle-policy fields; never emit container environment or mount contents.
3. If execution has not stopped, issue one `docker container kill --signal SIGKILL`
   for that exact ID. A failed/lost command response does not prove the kill failed.
4. Reinspect within the original deadline. Require `running`, `paused` and
   `restarting` to be false, with status `created`, `exited` or `dead`. Recheck the
   daemon identity after observing stopped state.
5. Confirm only if restart policy is `no` and automatic removal is disabled.

An already stopped, correctly bound container succeeds without another kill.
A changed lifecycle policy still permits a kill against verified running identity,
but produces an unconfirmed result because future restart/removal semantics are
outside the saved launch requirements. Missing containers remain unconfirmed;
this stage has no verified removal receipts. Neither a successful kill exit code
nor a failed inspect is accepted as proof of exit.

The total identity-read/Docker confirmation budget is five seconds, shortened by
any caller deadline. Each Docker subprocess is canceled with that context; pipe
draining has a separate 100 ms maximum. Command stdout is bounded to 64 KiB and
stderr is discarded rather than copied into receipts. Docker/API errors use fixed
codes. One unavailable or ambiguous daemon/container observation fails closed.

This uses Docker's documented [explicit daemon selection](https://docs.docker.com/reference/cli/docker/),
[container inspection](https://docs.docker.com/reference/cli/docker/container/inspect/)
and [forced kill](https://docs.docker.com/reference/cli/docker/container/kill/).

## Emergency evidence independent of the main journal

The campaign owns these additional private files:

```text
termination.lock                 # separate, nonblocking administrative writer lock
termination-intent.json          # first terminal intent; immutable
termination-results.json         # bounded, logically append-only observation segment
```

Records use `operator.dev/termination/v1alpha1` with request/reason, campaign,
launch, run-manifest digest, full Docker container ID and UTC observation time.
An intent has a null outcome; result records contain the typed Docker outcome.
Each record has a canonical SHA-256 digest, and the result inventory also has a
digest to detect removal of whole entries. Files and parent directories are synced
using the existing atomic publication helper. These are corruption checks, not
signatures against the trusted service user.

The first intent permanently marks the launch terminal. Repeated requests preserve
it; a reused recorded request ID with a changed reason conflicts. Result observations
are appended logically and published by atomic replacement of the complete bounded
segment. Earlier confirmed or unconfirmed results remain available. The segment
has a **1 MiB / 128-observation** ceiling and each record has an **8 KiB** ceiling.
This fixed emergency allowance is separate from ordinary journal reservations;
temporary publication files can transiently duplicate it. No physical disk capacity
is reserved. Exhaustion is a recording failure and cannot block another kill.

`ReadStopIntent` and `ReadTerminationResult` validate records without the worker
lock. An interrupted `.pending` publication is uncertainty, not a successful write.
A result without an intent also closes the stop-intent gate, since intent persistence
may have failed. Reading a result provides historical evidence, not current Docker
state, removal provenance or permission to resume. Recovery must retain uncertainty
and reconcile these records separately from the ordinary journal inspection.

Intent persistence starts concurrently with Docker termination. Result recording
follows the intent attempt and Docker observation. The caller waits at most another
250 ms for recording after Docker returns; an unfinished write is reported as
`unconfirmed`. Filesystem I/O cannot be forcibly canceled portably. A late write
may finish in a live service; CLI process exit may interrupt it. Readers handle
leftover temporaries conservatively. No writer/journal lock, sync, report generation
or guest acknowledgement is a prerequisite for issuing the kill.

## Runtime integration

Before admission, the launcher MUST save the binding, establish its validity,
and arm `Service.Watch` with that binding and the shared `campaign.Fence`. The
observer freezes the binding before waiting. It uses the saved in-memory copy on
failure, so a later state-filesystem stall cannot prevent the live kill attempt.
Fence closure and service cancellation each trigger one bounded termination attempt
with a fresh context. It does not wait for the worker, pump or journal to finish.

Administrative stop reaches a separate worker through persisted terminal state and
the actual container stop. The launcher/broker MUST check the stop-intent
gate, in-memory fence and exact Docker state at launch and external-dispatch
boundaries, and fence raced work. A running container alone can never override a
recorded terminal decision. Already dispatched native work can remain unknown.

This command stops the harness container and retains it for evidence. It does not
remove Docker resources, delete transport storage, stop the native Interceptor
target or purge campaign artifacts. The [host worker](HOST_LAUNCH.md) supplies those lifecycle steps after confirmed
exit and records removal/cleanup separately. Its macOS power lease lasts through
cleanup. Administrative reporting and supervised service submission are implemented;
host installation packaging and full native qualification remain outstanding.
A stop overlapping an unconfirmed start uses `startup_outcome_unconfirmed`; that
outcome does not authorize removal or transient-directory cleanup.

## Validation

Tests exercise exact binding/label/image checks, context/environment changes,
renamed-container independence, already stopped states, lost kill responses,
missing containers, changed daemons, lifecycle policy drift, deadlines, bounded
subprocess output and cancellation. The CLI is exercised with a real subprocess
standing in for Docker while the campaign writer remains active and its journal
is corrupt. Separate tests block/fail recording, cancel the worker context, corrupt
emergency records, leave interrupted publications and exhaust the emergency segment.
No test result here qualifies actual Docker confinement or host interruption handling.
