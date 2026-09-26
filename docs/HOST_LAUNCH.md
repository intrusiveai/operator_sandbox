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
  Python flags, no restart policy, health check or Docker log storage.
- UID 65532 and the host's nonroot primary GID, matched against transport ownership.
  A root primary group is rejected. No supplementary group is added.
- Read-only root, network none, dropped capabilities, no new privileges, private
  IPC/cgroup namespaces and a startup syscall allowlist.
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
caffeinate assertions. Executable campaign startup and lifecycle integration are
separate from these low-level primitives.
