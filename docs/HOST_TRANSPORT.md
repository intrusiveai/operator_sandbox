# Host FIFO and file-spool transport

Status: implemented in Go under `internal/transport`. This layer moves the existing
shared messages over real named FIFOs and regular files. It does not start Docker,
implement the Python peer, verify confinement, dispatch operations or grant durable
admission. No shared wire schemas or Interceptor APIs change.

The sibling Attack Harness now supplies Python FIFO and spool peers. Run
`OPERATOR_PYTHON_PEER_TEST=1 go test ./internal/transport -run 'TestPython.*Interoperability' -count=1`
with its checkout and Operator's prepared `.venv` to exchange the complete wire
startup and one ordinary operation over real FIFOs and spool files, including
guest FD 3–6 handoff. This tests peer communication;
the test-only guest does not claim confinement or validate campaign inputs. Docker
Desktop mount behavior and the complete runtime still require qualification.

## Launcher and broker integration

1. Load the installed `contracts.Protocol`, establish the campaign/launch identity,
   and supply the campaign's existing `campaign.Fence`. Supply the trusted absolute
   monotonic campaign deadline, no more than 30 minutes away. Do not reconstruct a
   remaining deadline from a guest timestamp or restart an interrupted campaign.
2. Create an empty host-private launch directory, then call `NewFIFO` for Linux or
   `NewSpool` for macOS. A nonempty directory cannot be reopened. Construction errors
   close handles but can leave partial preparation files; the launcher owns cleanup
   of those files before starting a container. Constructors do not start Docker.
3. Match `Config.GuestGID` to the guest's transport-access group. Nil selects the
   host's primary group. Host-produced files are group-readable; guest-produced
   FIFO endpoints are group-writable, and spool directories are group-writable.
   The launcher must select a guest UID that cannot exercise host-owner privileges,
   bind only the designated paths, and enforce read-only host-produced spool mounts
   or the read-only FIFO directory mount. Validate Docker Desktop's actual permission
   mapping during qualification; local same-user tests do not prove guest isolation.
4. Run `Session.Run(ctx)` in its own goroutine. It performs bounded pumping with
   control/ACK priority and enforces deadlines even while ordinary work is idle.
   Tests or a dedicated event loop can use `Pump`; intermittent calls are not a
   production scheduler. Never put the pump behind a model call or journal write.
5. `Enqueue` validates and copies host messages. FIFO startup returns `ErrNotReady`
   until both host write endpoints rendezvous; continue pumping, then retry the
   same unsent bootstrap. `ErrQueueFull` also permits retry of the same unsent
   message. Neither error advances its sequence. Other protocol/I/O/deadline errors
   are terminal and signal the shared fence.
6. `Receive` returns captured guest bytes from separate control and ordinary queues.
   A consumption ACK means bounded capture, not journal commitment or permission
   for an effect. The broker must run identity, policy and durable admission checks.
7. After verifying `confinement_ready`, call `BeginInitialization` once. After all
   startup/manifest/journal checks, enqueue `admission_open`. `Published(lane, seq)`
   identifies complete FIFO write or atomic spool publication, not guest consumption.
   Call `OpenAdmission` after publication. A fast guest's first request stays in the
   transport during this short handoff; ordinary capture cannot bypass the broker
   gate. Startup verification still belongs to the broker and the existing shared
   validators. Transport alone does not prove the full startup transcript or inputs.
8. The ordinary lane allows one active request. `OperationDeadline` supplies its
   absolute deadline; `TightenOperation` can apply a shorter native-adapter deadline.
   Correlated responses must pass the shared response validator before enqueueing.
   Healthy restores preserve the same session and all lane counters.

The launcher must observe `Fence.Done()` independently and terminate the exact
recorded Docker container on failure. The transport does not perform Docker calls
or write failure evidence itself. Context cancellation and `Close` signal the fence
before waiting for the I/O mutex. A blocked filesystem call can still delay the
pump or handle closure; independent Docker termination must not wait for them.
The [Docker termination layer](DOCKER_TERMINATION.md) now supplies that observer;
arming it with the saved binding remains part of launcher integration.

## Bounds and deadlines

| Resource | Host implementation |
|---|---|
| Ordinary messages | 4 MiB each; 2 queued per direction, at most 8 MiB |
| Control messages | 64 KiB each; 16 queued per direction, at most 1 MiB |
| Spool ACK | 1 KiB, cumulative positions, no sequence slot or ACK-of-ACK |
| Transfer / backpressure | Non-renewing 5-second deadline; partial progress and stale ACKs do not renew it |
| Startup | Up to 60 seconds for confinement, then 60 seconds for initialization; bounded by campaign deadline |
| Ordinary execution | Minimum of requested timeout, registry ceiling, campaign deadline and any supplied native deadline |
| Spool polling / size check | Poll every 10 ms; logical-size scan initially and every second |
| Spool quota | `Config.SpoolMaxBytes`, default 512 MiB; excess signals `SPOOL_SIZE_LIMIT` |

Spool physical outstanding files have the same per-direction message/byte bounds
as queues, including publication temporaries. Host producers delete only files
covered by valid ACKs, before reusing capacity. Missing files alone are normal
idleness; outstanding ACKs and queues have deadlines. An accepted model request
can take longer than five seconds because execution has its own deadline.

FIFO endpoints are nonblocking, directional and tied to their prepared inodes.
Initial EOF/ENXIO is allowed only during rendezvous. Verified confinement establishes
the peer; subsequent EOF, EPIPE, partial-frame timeout or inode replacement is
terminal. No reconnect, dummy endpoint or replay path is provided. FIFO pumping
uses a 1 ms tick and bounded nonblocking reads/writes, with control first.

Spool readers accept only fixed names and bounded regular files, reject hardlinks,
symlinks and special files, and retain validated bytes independently of subsequent
path changes. ACK replacement and acknowledged producer deletion can unlink an
already opened inode without invalidating its captured bytes. Changed retained
messages, unacknowledged deletion, reappearing sequences, gaps and future ACKs fail.
Publication uses an exclusive temporary file followed by atomic rename. Host lanes
must have exactly one producer and be read-only to the guest.

The size scan includes temporaries, ACKs and unexpected nested content in all four
lanes. It stops at the configured quota or an inspection failure. Traversal is
bounded to 256 entries and 32 nested levels, checks cancellation, and has a
five-second processing deadline. Directory floods and invalid entries fail rather
than causing an unbounded walk. `SpoolUsage` supplies the last measured bytes and
limit for later host failure recording. This is periodic detection, not filesystem
capacity reservation; transient overshoot remains possible.

## Shutdown and cleanup

`Close` stops host I/O and retains paths. After the launcher independently confirms
exit of the recorded Docker container, it calls `CleanupAfterExit(true)`. Cleanup
requires closed host handles, checks the original directory identity, and removes
the whole launch transport directory, including leftover temporaries and unexpected
nested entries. It does not follow links to outside files. Repeated successful
cleanup is harmless; a replacement directory is rejected.

The caller must supply real Docker exit confirmation; the boolean is a trusted
launcher assertion. Cleanup is separate from termination because deleting many
hostile files can take time. Lifecycle integration must also distinguish expected
final exit from failure and reconcile a lost final spool ACK using authoritative
Docker/journal state. A closed transport is never reopened for execution.

## Validation and remaining work

Tests use actual FIFOs and spool directories, complete shared startup fixtures,
fragmented framing, fast first requests, response correlation, restore-continuing
sequences, queue pressure, stale/late/future ACKs, peer loss, operation/startup/
campaign deadlines, malformed paths, size excess, and confirmed-exit cleanup.
Cancellation is tested while the I/O mutex is held. Fake monotonic clocks make
deadline boundary tests deterministic.

The Python physical peer is implemented. Complete Go/Python process exchanges and
Docker launch/permission/confinement qualification remain outstanding. The host worker now connects broker
dispatch and durable traffic audit. Transport schedules transfer, startup, idle,
operation and campaign deadlines; the service separately bounds finalization.
Meaningful model-loop progress remains an Attack Harness responsibility. No Docker
runtime is qualified by these local tests or cross-compilation alone.

The [host worker](HOST_LAUNCH.md) now supplies live bootstrap/admission and terminal
cleanup orchestration. Idle admission has a 180-second progress deadline; active
requests use their own bounded deadlines. Only publication of a completed ordinary
response resets the idle deadline. Guest ACKs and file timestamps cannot renew it.
Transport access groups are frozen at construction and checked by the launch plan.
