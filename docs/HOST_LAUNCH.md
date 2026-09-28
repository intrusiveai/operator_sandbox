# Host launch and lifetime

## Docker launch boundary

`internal/dockercontrol.NewLaunchPlan` freezes the native image/daemon pin, run
manifest and verified `staging.Tree`/`transport.Session`. No campaign-controlled
Docker flags, environment values, executable paths or additional mounts are accepted.

The launcher MUST persist campaign start intent before calling `Create`. It MUST
save every nonzero returned Docker binding even when verification also returns an
error. `StartCreated` requires that exact saved binding and the original single-use
plan; it rechecks mounted inputs, transport identity, policy bytes and image identity.
Create and start are each attempted at most once per in-memory plan. Recovery MUST
never reconstruct a plan to retry an uncertain start. A lost create reply does not
prove that no stopped container exists; retain its failure and mount paths for
administrative reconciliation. Labels are attribution, never cleanup selectors.

The fixed runtime uses:

- Immutable local image ID, `--pull=never`, fixed Python bootstrap and isolated
  Python flags, no restart policy, init wrapper, health check or Docker log storage.
  Create uses a private empty Docker CLI configuration so ambient proxy/credential
  configuration cannot add guest environment values.
- UID 65532 and the transport's frozen nonroot GID. The default is the host's
  primary group; a privileged host MUST select a dedicated nonroot transport group.
  No supplementary group is added.
- Read-only root, network none, dropped capabilities, no new privileges, private
  IPC/cgroup namespaces and a startup syscall allowlist. `--ipc=none` omits the
  writable `/dev/shm` mount while preserving a private IPC namespace, as defined
  by [Docker](https://docs.docker.com/reference/cli/docker/container/run/#ipc-settings---ipc).
- Two CPUs, 4 GiB memory with no additional swap, 16 tasks, 256 descriptors,
  disabled core dumps; 128 MiB/8192-inode work and 32 MiB/2048-inode temporary tmpfs
  mounts with `nodev,nosuid,noexec`.
- Nonrecursive private binds for verified read-only files/directories (0444/0555).
  FIFO transport is read-only mounted. Only the two macOS outbound lanes have
  writable host binds. Image-declared volumes are rejected.

The startup syscall allowlist permits Python startup and installation of the
irreversible guest live filter. It denies sockets, process creation, mount/namespace
changes, ptrace, BPF and perf. It is **not** the live filter. The actual published
Python image and both stages still require runtime qualification.

`WatchEvents` addresses one saved endpoint/full container ID and never reconnects.
A subscription replays events from before create; EOF, malformed messages and
unexpected container events are failures. Callers MUST fence on those failures and
continue independent exact-identity termination. Current running-state/daemon
checks remain mandatory before admission.

## Host lifetime

`internal/hostlifetime` reuses Interceptor's lease pattern. On macOS it starts
`caffeinate -i -w <host-pid>`, verifies the helper's idle-sleep assertion through
`pmset`, and checks kernel sleep/wake markers and helper liveness every 500 ms.
The worker MUST check the manager on admission and retain its lease through
terminal cleanup. A fault is latched and cannot be cleared by a later wake.
Linux uses an inert backend. This does not provide logged-out macOS execution.

Unit tests exercise command policy, changed identity/input/policy failures,
uncertain creation, saved-binding startup, no restart, stream loss and lifetime
faults. They do not qualify Docker containment, Docker Desktop sharing or real
caffeinate assertions. The worker integration below consumes these primitives.

## Live worker integration

`internal/hostworker.New` requires fresh persisted preparation, private staged
inputs, a live transport, compatible host-approved release metadata, native/model/
evidence services, a fixed absolute deadline and a start request ID. A caller MUST
serialize campaign starts for the MVP and resolve accepted start-request retries
from durable records; constructing another worker is not a retry mechanism.

`Run` executes once:

1. Acquire the host lifetime lease, journal start intent/release bytes/mount paths,
   and reserve space for the terminal launch record.
2. Create the fixed Docker container and save its full binding before start.
3. Arm independent termination and native cleanup, subscribe to Docker events,
   then attempt start once. Start has a five-second bound; create has a sixty-second
   bound. Both are capped by host cancellation/deadline.
4. Run the transport pump independently. Generate the host's three startup messages
   from frozen inputs; accept the guest's two messages only from the live channel.
   Journal control intent, actual received replies and completed publication.
5. Verify confinement acknowledgement before initialization; validate the complete
   transcript/input identity, host lifetime and running Docker identity before
   service admission. Publish `admission_open` before opening ordinary transport.
6. Serve the existing 13 operations while checking host lifetime on each admission
   and polling exact Docker state. An idle admitted harness has 180 seconds to make
   progress; an active request retains its operation deadline instead.
7. On termination, keep the power lease while independent Docker stop and native
   cleanup/evidence collection finish. Remove only the exact confirmed inactive
   container, close transport handles and delete its spool/FIFOs and staged inputs.
   Journal the final cleanup/termination state before releasing the lease.

A stop racing an unconfirmed Docker start records
`startup_outcome_unconfirmed`; it cannot authorize removal or filesystem cleanup.
Unknown creation, start or termination retains mount directories. Removal failure
is separately reported; confirmed exit still permits transient-file cleanup.
A successful removal is recorded in `launch.terminal`. The standalone emergency
termination command remains conservative when Docker later reports that container
missing; it does not treat absence alone as evidence of prior termination.

The worker returns its terminal result and stop cause. `stop_accepted` distinguishes
a durably accepted harness stop from an unsolicited exit; it does not claim
successful cleanup or an assessment verdict. Campaign reports interpret the retained attempt/completion/native evidence.
The campaign writer remains caller-owned and MUST stay open until `Run` returns.
Context cancellation is the embedding host's signal/logout shutdown entry point.
Recovery remains cleanup/reporting only.

Integration tests use real campaign journals, immutable staging and macOS spool
files with scripted Docker/native peers and power leases. They cover live bootstrap,
ordinary attempt dispatch, requested completion, bad guest identities, absent replies, Docker stream loss,
lost create/start replies, unconfirmed termination, durable startup uncertainty,
single-use execution and cleanup. Linux FIFO I/O has separate transport tests.
These tests do not qualify an actual Docker/Python deployment.

[Installed campaign preparation/start](CAMPAIGN_START.md) supplies administrative
run preparation, provider/credential binding, accepted-start deduplication,
single-host campaign serialization, signals and systemd/LaunchAgent submission.
Host installation packaging and full native qualification remain outstanding.
