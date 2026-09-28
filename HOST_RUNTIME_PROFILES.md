# Operator host runtime profiles

Status: platform/transport/termination and MVP containment decisions accepted,
file-spool and service-lifetime decisions accepted. Transport, supervised launch and
lifecycle handling are implemented as of 2026-09-28; installation packaging and
full native qualification remain outstanding. See [implementation status](docs/IMPLEMENTATION_STATUS.md).

This document supplies the host-specific requirements for `operator-container/v1`.
The [product spec](OPERATOR_SANDBOX_SPEC.md), [guest contract](GUEST_CONTAINER_SPEC.md)
and [shared message contract](schemas/SHARED_CONTRACT.md) retain their respective
lifecycle, containment and wire responsibilities.

The [host transport layer](docs/HOST_TRANSPORT.md) implements FIFO/spool pumping,
bounded queues, deadlines, periodic size checks and confirmed-exit transport cleanup.
Its local tests do not qualify Docker permissions, confinement, the Python peer or
independent container termination; those gates below remain open.

## 1. Supported host scope

| Operator host | Go host platform | Local runtime | Attack Harness image platform | Transport |
|---|---|---|---|---|
| Linux x86_64 | `linux/amd64` | Docker Engine | `linux/amd64` | Named FIFOs |
| Linux AArch64 | `linux/arm64` | Docker Engine | `linux/arm64` | Named FIFOs |
| macOS x86_64 (Intel) | `darwin/amd64` | Docker Desktop | `linux/amd64` | Regular-file spool |
| macOS ARM64 (Apple Silicon) | `darwin/arm64` | Docker Desktop | `linux/arm64` | Regular-file spool |

These are the supported host OS/architecture targets. Operator and Attack Harness
runtimes are implemented; complete production-image tests on all four tuples
remain outstanding.
Cross-architecture emulation, remote daemons and other desktop runtimes are outside
the initial matrix. Linux Docker Desktop is not the selected Linux profile.
Docker Desktop runs the Linux guest kernel on macOS; guest paths and Python/syscall
ABIs remain Linux. The matching Linux image may serve both host OSes when its
bootstrap supports both transports. The installed host policy selects transport;
campaign data cannot change it. The launcher supplies the fixed allowlisted
`OPERATOR_TRANSPORT=fifo` or `OPERATOR_TRANSPORT=spool` setting. Bootstrap verifies
its mounted layout and requires the host bootstrap binding to match.

The release response's `platform` identifies the Linux **image**, not the Operator
host OS. Keep host platform, image platform, local daemon binding, profile revision
and transport in host launch records. `operator-container/v1` names the common ABI;
the installed host profile checks the required runtime capabilities before launch.
The confirmed-supported version list is informational, not an admission allowlist.
No additional downloaded contract or guest-selected Docker flags are introduced.
Docker publishes [Intel and Apple Silicon builds and its macOS support policy](https://docs.docker.com/desktop/setup/install/mac-install/);
the configured [VM and file-sharing settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)
must permit the required mounts and transport.

Operator and Interceptor run on the same physical machine with local Docker access.
Docker Desktop is a supported execution target, not merely a development client.
Existing Interceptor spool validation is useful evidence for the transport pattern;
it does not qualify Operator startup, confinement, journaling or termination.

### 1.1 Confirmed supported versions

Use **confirmed supported versions** for the documented baseline, not minimum
required versions. The agreed initial entries, selected on 2026-09-17, are:

| Component | Confirmed supported version |
|---|---|
| Linux OS, x86_64/AArch64 | Ubuntu 26.04.1 LTS |
| macOS, Intel/Apple Silicon | macOS Sequoia 15.8 |
| Docker Engine on Linux | 29.8.1 |
| Docker Desktop on macOS | 4.91.0, including its bundled Engine 29.8.0 |

This records the agreed support baseline. Selected macOS ARM64 service/confinement
probes have passed; complete production-image feasibility tests remain outstanding. Sources: [Ubuntu point release](https://lists.ubuntu.com/archives/ubuntu-announce/2026-August/000326.html),
[Apple releases](https://support.apple.com/en-us/100100),
[Docker Engine releases](https://docs.docker.com/engine/release-notes/29/), and
[Docker Desktop releases](https://docs.docker.com/desktop/release-notes/#4910).

Operator does not reject older, newer or unlisted OS/Docker versions solely because
they differ from these entries. Startup checks actual required capabilities and
configuration: local Docker access, matching image architecture, mounts/permissions,
transport, resource controls and syscall filter installation. A missing capability
produces an actionable error identifying that capability. No OS/Docker patch-age
policy, enforced update schedule, per-release version matrix or prior qualification
of every new OS/Docker combination is required for the MVP. Ordinary launch identity
and image/release compatibility records remain required. The image release's
`minimum_operator_version` is a separate application compatibility field.

### 1.2 MVP filesystem and syscall controls

Use Docker's read-only root and explicit read-only input/skill/manifest mounts,
ordinary ownership/permissions, and bounded non-executable temporary filesystems.
Input/skill/manifest binds MUST be nonrecursive with private propagation; staged
regular files MUST use mode `0444` and directories `0555`. Writable tmpfs mounts
MUST use `nodev,nosuid,noexec` with byte/inode ceilings.
Only the designated macOS outbound spool lanes are writable host-backed transport
mounts. Do not expose runtime sockets, credentials or unrelated host paths.
Read-only mounts prevent writes; readable container files are not subject to an
additional path allowlist.

Run a fixed nonroot UID/GID with empty capabilities and `no_new_privs`. Separate
user-namespace remapping is optional. A custom AppArmor, SELinux, Landlock or other
additional filesystem policy is not an MVP prerequisite. Compatible runtime-provided
protections may remain enabled; Operator does not require administrators to install
or manage an extra filesystem policy.

Keep the two-stage seccomp policy: Docker applies a restricted startup profile,
including socket denial; trusted Python bootstrap loads its fixed dependencies and
installs the tighter live allowlist before processing untrusted inputs. The live
filter denies additional execution, child processes/threads and unnecessary kernel
operations while allowing required FIFO/spool and scratch I/O. Filter failure is a
startup failure; there is no unfiltered fallback. Network none remains required.

Linux FIFO direction and immutable directory entries are enforced with ownership,
permissions and a read-only mount. Kernel enforcement of a post-bootstrap FIFO
reopen ban is not required. Reopening an assigned FIFO with its permitted direction
does not grant a new launch, reset sequences or recover a failed channel. Peer loss
remains terminal and the normal client retains its established descriptors.

## 2. Transport requirements

Both transports implement the same four logical directions: ordinary host-to-guest,
ordinary guest-to-host, control host-to-guest and control guest-to-host. Preserve
the shared startup, identities, per-direction sequences, budgets, errors, operation
idempotency and healthy-restore continuity. The ordinary JSON limit is 4 MiB and
control limit 64 KiB, with two ordinary frames/8 MiB and 16 control frames/1 MiB
per direction. Transport storage is provisional; only host journal/artifact commits
establish durable outcomes.

### Linux FIFOs

Keep the [four-path FIFO contract](GUEST_CONTAINER_SPEC.md#41-descriptor-inventory):
fixed directions, bootstrap mapping to FD 3–6, four-byte length-prefixed JSON,
bounded rendezvous, no dummy endpoints and terminal established-peer EOF. The private FIFO directory remains read-only to the guest.

### macOS regular-file spool

Use Interceptor's atomic-file publication and polling pattern, adapted to Operator's
shared envelopes. The authoritative names, acknowledgement payload and delivery
rules are in [shared contract Section 4.1](schemas/SHARED_CONTRACT.md#41-macos-file-spool-wire-rules).
Interceptor's [HTTP spool](../interceptor_sandbox/internal/bridge/http_spool.go)
and [atomic writer](../interceptor_sandbox/internal/fsutil/atomic.go) are reference
implementations of the pattern, not alternative Operator message schemas.

Each fresh launch exposes four directories under `/run/operator/spool/`:

| Lane | Producer | Guest mount |
|---|---|---|
| `ordinary-in` | Host | Read-only |
| `ordinary-out` | Harness | Writable |
| `control-in` | Host | Read-only |
| `control-out` | Harness | Writable |

Mount the lanes individually; the guest cannot access the parent or another launch.
Directory identity and ownership are host-owned;
only the designated outbound directories allow guest file creation/removal. Validate
effective file permissions through Docker Desktop sharing. Inputs, skills and full
manifests stay on separate immutable mounts; artifacts still use typed commit calls.

Use 20-digit zero-padded sequence filenames, temporary-file/atomic-rename publication,
and one cumulative `consumed.json` per side in its outbound control lane. Consumption
releases transport storage only. Preserve startup gates, operation identities,
monotonic deadlines and journal-before-effects rules. The host and harness service
acknowledgements and control first every 10 ms, then bounded ordinary work. No
filesystem notification dependency, extra thread or network socket is required.

#### Protocol limits

| Item | Limit |
|---|---|
| Ordinary JSON message | 4 MiB |
| Control JSON message | 64 KiB |
| Unacknowledged ordinary messages | 2 / 8 MiB per direction |
| Unacknowledged control messages | 16 / 1 MiB per direction |
| `consumed.json` | 1 KiB per side |
| Temporary files | At most one active publication per writer per lane, including acknowledgement replacement |

Count a message under construction against the outstanding-message limit. Count
temporary bytes as well as ready-file bytes; a sender cannot create a new copy of
every outstanding message outside the limit. Each recipient's in-memory queue also
retains the shared bounds.
Unexpected files, oversized content or a producer exceeding protocol limits are
transport failures. Poll exact expected filenames and use bounded checks for other
entries, never unbounded directory enumeration or recursive cleanup during dispatch.

#### Host spool-size check

The installed Operator configuration sets `spool.max_bytes`, defaulting to
536870912 bytes (512 MiB). Require a positive integer; omission selects the
default. Record the effective value with the launch configuration and retain it
through healthy target restores. Guest input cannot change it. It applies to the
combined four macOS spool lanes; the Linux FIFO profile has no file spool to count.

The Operator host process checks the directories before guest admission and once
per second while the container can write, including bootstrap and paused operation
states. Sum logical file lengths across all lanes, including temporary publications,
ACKs, consumed files awaiting deletion and unexpected files in nested directories.
Do not follow links or traverse outside the assigned spool roots. A file that
vanishes during the scan is normal concurrent cleanup. Other inspection failures
or unexpected links/special files fail the transport. Keep traversal memory bounded,
cancelable and independent of message dispatch; a scan that cannot complete within
five seconds also fails the transport. Stop scanning as soon as the sum exceeds the
configured limit. Equality is allowed. Report bytes, limit and reason.

On excess, close admission and invoke the immediate Docker termination path with
reason `SPOOL_SIZE_LIMIT`; do not wait for another model/attempt quota or a spool
reply. Preserve uncertain operations and attempt a bounded journal record without
allowing journal failure to delay termination. The limit is a process-enforced
threshold: writes can exceed it between checks and before Docker confirms exit.
It does not change message sizes, queue limits, ACKs or polling rules. File content
is neither read nor copied into the journal by the size check.

#### Cleanup and failure handling

Producers promptly remove messages covered by consumption acknowledgements. Remove
failed temporary publications; never age out unacknowledged messages and continue
execution. Five-second publication/acknowledgement/full-queue deadlines detect
transport stalls without shortening the separate model/target operation deadlines.
Neither polling nor changing file timestamps extends a deadline.

Failed publication/ACK, invalid protocol or required journal failure closes
admission and attempts Docker termination. Record one bounded failure
summary, with explicit uncertainty for any dispatched work. Do not copy an entire
hostile spool into journal storage or repeatedly log every bad file. Administrative
Docker termination needs neither writable spool space nor a healthy worker.

After Docker confirms exit and host transport writers have stopped, remove the
complete spool directories, including temporary and unexpected entries, and finish
bounded journal records when possible. Apply this to normal completion, explicit
stop, quota/fault termination and partial startup failure.
Failed or uncertain exit/cleanup retains the recorded resource binding for later
cleanup. At host startup, reclaim abandoned spool directories only after confirming
that their bound container and host writers are inactive; never follow links outside
the recorded roots or reuse leftover messages. Recovery cleans up only. Healthy
target restores retain the files, sequence positions and acknowledgement state.
Journals and committed artifacts provide evidence; the spool is transient.

## 3. Journaling and termination

All platforms retain host-side broker/model and lifecycle journals. None requires
a guest activity sensor, syscall/denial event stream, monitoring watchdog or
kernel-event readiness gate. Journals cover mediated interactions and recorded
Docker outcomes; they do not claim complete observation of internal guest behavior.
Network denial, nonroot identity, read-only inputs/root, syscall restrictions,
resource ceilings and journal failure handling remain required.

The administrative command runs independently of the campaign worker:

```sh
operatorctl campaign terminate --campaign <id> --mode immediate --reason user-request
```

It uses the durable exact daemon/container binding to call Docker kill and confirm
stopped state. It requires local permission to that state and Docker endpoint, not
a functioning worker, harness or transport. Disable restart policies and persist
container identity before start. Journal failure must not block the kill attempt.
The [stop contract](OPERATOR_SANDBOX_SPEC.md#102-stop-behavior) defines fencing,
identity checks, bounded confirmation, unknown outcomes and resource cleanup.

Docker must respond. The five-second confirmation target applies to a responsive
installation satisfying the runtime requirements. API/daemon/VM failure yields an explicit unconfirmed
outcome and nonzero CLI status; it does not require an OS-specific bypass or
automatically restart Docker. No automatic worker-death watchdog is required for
the MVP administrative termination guarantee. Worker/service recovery performs
cleanup and finalization only, never campaign execution recovery.

The [implemented termination stage](docs/DOCKER_TERMINATION.md) provides this CLI,
saved-binding verification, bounded Docker inspection/kill, emergency evidence and
the live terminal-fence observer. Launcher/service integration and actual Docker
qualification are still required; fake subprocess tests do not qualify a host profile.

## 3.1 macOS service lifetime and idle-sleep prevention

Operator and Interceptor run as the logged-in Docker Desktop user. An installed
background service uses a per-user launchd LaunchAgent; foreground CLI execution
has the same lifetime rules. Screen lock and display sleep are supported. Campaigns
are not required to survive logout, host sleep or Docker interruption. Campaign
launch jobs use `KeepAlive=false` and `RunAtLoad=false`: starting execution is an
explicit administrator action, not a login/restart recovery action.

Each component starts its own `/usr/bin/caffeinate -i -w <host-process-pid>` helper
before starting execution, verifies the helper's `PreventUserIdleSystemSleep`
assertion, and retains it through active execution and shutdown cleanup. Hold the
same assertion continuously through healthy target restore, including the interval
between containers. Stop and reap the helper when cleanup completes; an idle API
serving status/evidence does not keep the Mac awake. The PID watch releases the
assertion if its host process dies. This needs no root access or persistent power
preference change and does not prevent display sleep or screen lock.

`caffeinate -i` prevents automatic idle system sleep. It does not prevent explicit
sleep, lid-close sleep, logout, shutdown or emergency sleep. On macOS, compare the
kernel sleep/wake markers at least every 500 ms while active and before admitting
new execution requests. Changed markers, unreadable power state or unexpected
helper exit close execution and trigger Docker termination. Initial assertion or
power-state verification failure rejects startup before launching the container.
Detection after wake does not guarantee that no container instructions execute
before host termination is scheduled; retain uncertainty for interrupted work.

Treat SIGTERM/logout, SIGHUP, detected host sleep, Docker connection loss/restart
and unexpected container exit as terminal interruptions. Close admission, attempt
a bounded durable reason record and independently request Docker termination.
A Docker event-stream disconnect is terminal even if Docker reconnects with a
running container; also check container state. Journal failure must not delay the
kill. Docker unavailability produces unconfirmed termination/cleanup, never a
success claim. Recovery only performs cleanup and reporting; it does not resume
execution or replay pending operations. Disable automatic container restart.
Operator additionally treats loss of its Interceptor service as terminal.

Mac Operator defaults use the private configuration file
`~/Library/Application Support/Operator/config/config.yaml` and retained state under
`~/Library/Application Support/Operator/data`. Resolve the account home and Docker
endpoint once from trusted installation configuration. The default Docker Desktop
socket is `unix://<home>/.docker/run/docker.sock`; configure other socket locations
explicitly under the [host configuration contract](docs/HOST_CONFIGURATION.md).
Worker and administrator CLI use the same account and state root. Cleanup uses
the saved campaign endpoint even if configuration changes. Interceptor
retains its documented data-directory configuration under that user's account.
LaunchAgents require that Docker Desktop is available before explicit run startup.

## 4. Decisions required before profile qualification

D1 is resolved by Sections 1.1–1.2, D2 by Section 2 and shared contract Section
4.1, and D3 by Section 3.1. Implementation and feasibility evidence remain required.

| ID | Decision | Recommended starting point / constraint |
|---|---|---|
| D1 | Confirmed supported versions and MVP containment — resolved | Section 1.1 records the informational version baseline. Section 1.2 selects Docker mounts/permissions and two-stage seccomp; additional filesystem policy, separate user-namespace remapping and a kernel-enforced FIFO reopen ban are not required. |
| D2 | File-spool protocol — resolved | Atomic sequence-named files, cumulative consumption acknowledgements, producer cleanup, 10 ms polling and five-second transport deadlines. Host-process size checks run every second with configurable `spool.max_bytes` (default 512 MiB); excess triggers Docker termination. Shutdown/startup cleanup removes inactive spools. Host transport and launch-worker termination/cleanup integration are available; supervised service submission and the Python peer are implemented. Installation packaging, complete Go/Python process exchanges and full native qualification remain outstanding. |
| D3 | Host installation, service identity and lifecycle — resolved | Linux starts with systemd. macOS uses the logged-in Docker Desktop user and a per-user LaunchAgent, private Library configuration/state, and `caffeinate -i -w <pid>` during active work and cleanup. Screen lock is allowed; logout, actual sleep and Docker interruption end execution. Recovery is cleanup/reporting only. See Section 3.1. |

Initial feasibility testing verifies immutable input/skill/manifest publication
through Docker Desktop sharing: permissions, case/Unicode collisions and no writable
aliases. Reject layouts that cannot satisfy those properties. Verify the selected
spool operations under the live syscall filter and Docker mount permissions, and
test durable journal writes separately from transient transport writes. Verify that
worker and administrator CLI resolve the same daemon and campaign state.

## 5. Qualification gate for gap 7

Run the initial real-image feasibility test on each of the
four host OS/architecture targets using the agreed baseline. Record enough context
to reproduce failures and mark each test pass/fail/not-run. This initial validation
does not impose recurring certification of every OS/Docker version combination:

1. Start the production Python image without pull and validate the release response,
   matching image/host architectures and full read-only input/skill manifests.
2. Complete all five startup messages and a model/tool/artifact round trip through
   the selected transport, then a healthy target restore without harness restart.
3. Verify socket/network and post-bootstrap exec/process denial with separate probe
   artifacts, plus read-only mounts, spool/FIFO isolation and resource controls.
4. Exercise malformed/oversize/partial traffic, queue pressure, control priority,
   deadline/peer loss, failed file/journal writes and no replay after failure.
   Verify transport directory sharing, permissions, the default/custom size limit,
   excess from temporary/unexpected files, scan failure, shutdown cleanup and
   abandoned-spool cleanup after host restart. Verify journals survive spool removal.
5. Kill a hung harness from a separate administrator CLI after stopping the worker.
   Confirm Docker-stopped state and no restart. Repeat with journal write failure.
6. Make Docker unavailable and confirm bounded unconfirmed status with no false
   success, replacement or unsafe cleanup. Qualify selected sleep/logout/restart
   behavior and recovery for cleanup only. On macOS, verify helper assertion readiness,
   continuity through restore, release after cleanup while the API remains available,
   and closure on helper failure or a changed sleep/wake marker. Distinguish simulated
   interruptions from tests of physical sleep/logout/Docker restart.

The complete native test matrix remains outstanding. Neither the support matrix nor passing
Interceptor's own tests closes gap 7 without Operator/Attack Harness evidence on these tuples.
