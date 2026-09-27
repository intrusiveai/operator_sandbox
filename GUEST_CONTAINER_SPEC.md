# Operator Sandbox Container Guest Contract

Status: container design draft, 2026-09-16; no image, launcher or harness qualified  
Owner: this container design; paired with [the product specification](OPERATOR_SANDBOX_SPEC.md)

The accepted [shared host/harness contract](schemas/SHARED_CONTRACT.md) governs
wire semantics and data exchange. This document defines runtime responsibilities;
the package, validators/fixtures and actual launcher remain to be implemented and
qualified. Both ends must pin the exact package version and content digest.

## 1. Decisions

- One prebuilt OCI image contains CPython, the product-owned `operator-native`
  package, its exact runtime dependencies, codecs, tool catalog, skill loader,
  default prompt and a small native confinement component if required.
- A host-controlled launcher starts one nonroot Python process. No privileged
  `operator-guestd`, init system, coding CLI, shell or network relay runs inside.
- Four logical channels carry typed requests/results: named FIFOs on Linux hosts,
  regular-file spools on macOS hosts through Docker Desktop.
  Host-created mounts supply immutable input/skill data. All outputs are uploaded
  through committed artifact operations, never a writable host output directory.
- The host owns lifecycle, journal, credentials and target/model access. Docker
  termination requires neither harness nor campaign-worker cooperation.
- All writable data is non-executable; no generated-code workspace exists.
  Python never treats a payload, skill, tool result or reference file as code.

## 2. Python base image

**Recommended production candidate:**
`gcr.io/distroless/python3-debian13:nonroot`, resolved to a verified digest per
platform. Distroless provides Python/runtime dependencies without a shell or
package manager; the non-debug Python image is available for amd64 and arm64.
Use its explicit Debian version and nonroot variant. Never use `debug` or
`debug-nonroot` for campaigns because those variants include a shell.
See the [upstream image catalog and verification guidance](https://github.com/GoogleContainerTools/distroless).

**Development/build alternative:** a digest-pinned official
`python:3.13-slim-trixie`, or another exact minor selected to match the runtime.
It is convenient for dependency installation and debugging; `slim` is not a
shell-free security profile. The [official Python image documentation](https://hub.docker.com/_/python)
lists the variants, while the [image source](https://github.com/docker-library/python)
is the authority for their package inventory. Development images are not accepted
by production campaign profiles.

Use the actual Python minor/patch version and library ABI found in the selected
Distroless digest, not the floating tag name, to select/build dependencies.
A Python Official Image and a Debian-packaged interpreter can use different
prefixes and installation paths. Do not blindly copy `/usr/local`, an entire
builder virtualenv, or a native extension compiled for a different minor/ABI.
Use a matching Debian/Python build environment, install only reviewed runtime
packages into a fixed application directory, and test imports in the final image
on each architecture. The [upstream Python image guidance](https://raw.githubusercontent.com/GoogleContainerTools/distroless/main/python3/README.md)
describes its runtime composition and application use.

This recommendation optimizes the deployed contents, not merely download bytes.
No exact size comparison has been measured. An Alpine or hand-assembled scratch
Python image is not the initial choice: it adds runtime/dependency qualification
work without being necessary for the non-network integration. Image minimization
is complementary to kernel/process policy; an interpreter remains capable of
executing application code even when external utilities are absent.

### 2.1 Build requirements

1. Resolve and verify base identity, selected architecture manifest and upstream
   signature/provenance through the trusted build path. Pin all dependencies and
   hashes, Python/native toolchains and compiler flags where applicable.
2. Perform dependency download/build only in an isolated release builder.
   Administrators install the prebuilt image in local Docker; Operator selects and
   validates it using [local image/release rules](OPERATOR_SANDBOX_SPEC.md#511-local-docker-image-configuration-and-release-validation).
   Guest initialization never installs dependencies or rebuilds the image. Provider
   network SDKs, credential clients and tokenizer downloads are unnecessary inside.
3. Copy only installed runtime files and the application into the final image.
   Exclude pip/ensurepip installation facilities, compilers/linkers, build caches,
   test/debug tools, package managers, shells, Git, curl/wget, SSH, sudo, Docker
   clients and coding agent CLIs. Audit the actual selected image, not just its tag.
4. Review native modules, dynamic loader dependencies and any import-time behavior.
   Remove installer hooks and ambient site customization. Do not rely on deleting
   `subprocess` or a few builtins as a confinement mechanism.
5. Generate a final-image SBOM and manifest of required runtime files. Scan and
   patch through rebuilt, release-approved image identities; there is no in-place package update.
6. Exercise the real final image under nonroot, read-only root, no network and
   selected Docker mount/permission and syscall controls. Initial feasibility testing
   covers the four host OS/architecture targets; it is not a recurring OS/Docker
   version certification requirement.

## 3. Release metadata and filesystem

Container ABI: `operator-container/v1`, using local Docker with Linux FIFO or macOS file-spool transport. The
HTTPS release record, immutable image metadata and installed host policy bind:

| Group | Required identities |
|---|---|
| Image | Full local Docker image/config ID and native platform; separately recorded registry index/manifest digests when available; source/build provenance and SBOM. |
| Python | Interpreter version/ABI, standard library/native dependencies and exact application installation layout. |
| Harness | Source revision, package digest, fixed bootstrap entrypoint, model codec set, tool catalog, skill loader and conclusion schema. |
| Guidance | Embedded default prompt exact bytes, digest, size and media type. |
| Confinement | Startup/live syscall profiles, Docker mounts/permissions, native seccomp component and resource contract; no mandatory additional filesystem policy. |
| Integration | Exact `operator-contracts` package version/content digest, offline catalog/operation registry, pipe/input/skill schema identities, compatible host launcher/transport and shared Go/Python qualification fixture identities. |

Do not put an OCI image's own digest inside a file used to calculate that image.
The external HTTPS record approves the Docker image ID; the immutable image
binds its embedded engine manifest.
Likewise, an InputTreeManifest lives outside the file set it hashes; the guest
receives its completed descriptor through the host startup binding.

The administrator selects an already installed local Docker image with
`engine.image`. Operator resolves its full image ID and validates the matching
[HTTPS release response](schemas/ENGINE_RELEASE_CONTRACT.md), including minimum
Operator version and exact locally installed contract version/digest and runtime
profile. It creates the container with pulling forbidden and retains the verified
image and cached response. The guest has no registry or release-service client.

```text
/usr/bin/python3                         immutable qualified interpreter
/usr/lib/...                            required immutable runtime libraries
/opt/operator/engine/bootstrap.py        fixed release-owned startup code
/opt/operator/engine/lib/                 application and pinned dependencies
/opt/operator/engine/share/
  default-system-prompt.txt
  engine-manifest.json
  tool-catalog.json
  schemas/
/run/operator/input/                     frozen launch inputs
/run/operator/customer-skills/           exact frozen selected skill set
/run/operator/manifests/                 immutable full input/skill manifests
/run/operator/ipc/                       Linux FIFO directory (macOS uses /run/operator/spool/)
/run/operator/work/                      fresh bounded temporary work
/tmp/                                   fresh bounded temporary files
```

Container policy exposes only the minimum runtime pseudo-files/devices. `/proc`
is private to its PID namespace with sensitive entries masked/read-only as
appropriate; no host `/proc`, host namespace handles, writable `/sys` or cgroup
control files. `/dev` has only the exact runtime necessities, such as null and
random devices, with device policy. No TTY, raw block device or device passthrough.

Root/application paths are immutable and not writable by the guest identity.
All input/skill/manifest directories are `0555`, regular files `0444`; no writable overlays.
Host staging parents and backing files are service-owned and cannot change during
the harness lifetime. Read-only bind mounts alone do not prevent a host-side owner from
changing backing bytes, so do not mount caller-owned source trees. Reject nested
mounts; freeze and verify a host-created copy before exposing it. Input, skill and
manifest binds MUST be read-only, nonrecursive and use private propagation.
The [host staging layer](docs/INPUT_STAGING.md) creates these copies and checks their
fixed names, bytes and modes. The [host launcher](docs/HOST_LAUNCH.md) wires only
its three read-only child trees. Actual guest access still requires qualification.

Work and tmp mounts are separate bounded `tmpfs` instances with
`rw,nodev,nosuid,noexec`, private propagation and byte/inode ceilings from policy.
Retain these mounts across healthy target restores; destroy them at terminal
harness teardown. No persistence into another harness process or campaign. Size/inode accounting includes partial
artifacts and temporary parse buffers; cgroup memory includes tmpfs usage.
There is no `/run/operator/work/exec` mount.

`noexec` cannot prevent an interpreter from reading a file as source. Therefore
data directories are excluded from import paths, dispatch never interprets data,
and no execution/evaluation tool is registered. Native code loading is limited
to immutable reviewed release files; executable mappings from writable data and
unneeded anonymous executable memory are denied where the qualified profile can
enforce them. Do not claim these controls make in-process Python exploitation
impossible.

## 4. Non-network transport and launch identity

### 4.1 Descriptor inventory

On Linux hosts, Docker exposes a private per-launch FIFO directory at `/run/operator/ipc/`.
Guest bootstrap opens the following paths and assigns their descriptors locally:

| FD | Fixed path / guest direction | Purpose |
|---|---|---|
| 0 | `/dev/null`, read-only | No interactive command stream. |
| 1, 2 | Docker bounded stdout/stderr capture | Diagnostics only, never dispatch. |
| 3 | `/run/operator/ipc/ordinary-in`, read-only | Host ordinary responses. |
| 4 | `/run/operator/ipc/ordinary-out`, write-only | Guest ordinary requests. |
| 5 | `/run/operator/ipc/control-in`, read-only | Host startup/control. |
| 6 | `/run/operator/ipc/control-out`, write-only | Guest startup/progress/control. |

Operator creates fresh FIFO inodes under a service-owned launch directory before
Docker create/start. Only these four special files are allowed; the general
no-special-file rule still applies to inputs, skills and manifests. Directory
entries and ownership are immutable to the guest. Bind-mount this directory
read-only with private propagation; FIFO byte I/O is permitted according to
FIFO permissions without granting filesystem mutation. No host parent directory,
Docker socket, other launch, device or arbitrary IPC path is mounted.

The guest identity must not own the FIFOs. Use host ownership and a guest group/ACL
granting only read on `*-in` and write on `*-out`, with no other-user access. For
example, owner modes 0640/0620 implement these directions when the actual guest
group matches. Separate user-namespace remapping is not required. Verify effective
UID/GID and permissions, including denied opposite-direction opens and
chmod/unlink/replacement. Read-only mounting alone does not restrict FIFO open
direction. No supplementary group may grant access to another launch's paths.

Open fixed paths descriptor-relatively without symlink following, require FIFO
file type, validate permitted metadata and record the host-side inode binding.
Map FDs collision-safely with close-on-exec; close temporary/unrelated descriptors.
Never open a FIFO O_RDWR or retain dummy/duplicate endpoints to suppress EOF.

Opening sequence, within the existing 60-second confinement/readiness deadline:

1. Host opens the read ends of `ordinary-out` and `control-out` nonblocking before
   starting Docker. The exact container binding and journal are ready first.
2. Trusted guest bootstrap opens `ordinary-in` and `control-in` read-only/nonblocking,
   then opens its write ends nonblocking. Host opens its write ends nonblocking;
   an absent reader (`ENXIO`) permits bounded retry only during this rendezvous.
3. Host sends `bootstrap` only after both host write ends are open. Guest waits
   for that frame before parsing input/skill data. Initial no-writer EOF during
   rendezvous is not established peer loss; bound waits by startup time and
   process liveness. After receiving bootstrap, install final confinement and send
   `confinement_ready`. Host receipt confirms all guest endpoints are established.
4. Continue the unchanged `initialize` → `initialized` → `admission_open` exchange.
   No ordinary dispatch precedes admission. After peer establishment, unexpected
   EOF/EPIPE or required channel loss is terminal; do not reconnect to resume execution.

Release-owned bootstrap opens the assigned IPC paths and the normal client retains
those descriptors. Docker mounts and ordinary permissions enforce endpoint identity
and direction; the MVP does not require a kernel-enforced ban on later opens of the
same permitted paths. Such an open cannot reset sequences, establish a replacement
peer or recover failed execution. Path persistence is not a reconnection facility.
Remove the launch directory only after guest exit and closing all host endpoints;
never reuse its inodes for a replacement launch. Host-enforced deadlines bound
stalled traffic. See [Linux FIFO semantics](https://man7.org/linux/man-pages/man7/fifo.7.html).

Docker creates the container from fixed host policy: image metadata, CMD,
healthcheck, labels, ports, volumes and hooks cannot add authority or alter mounts.
Disable restart, healthchecks and image-declared writable volumes; qualify the
installed image against the fixed mount inventory. Use typed local Docker API
requests, not shell commands supplied by a model. No campaign operation attaches
an interactive shell or Docker exec session.

### 4.1.1 macOS file-spool transport

On macOS hosts, Docker Desktop shares private regular-file directories with the
Linux guest. Use Interceptor's atomic-file publication and bounded polling pattern,
adapted to the shared Operator messages. The canonical
[shared spool contract](schemas/SHARED_CONTRACT.md#41-macos-file-spool-wire-rules)
fixes names/ACKs/deadlines; [host profiles](HOST_RUNTIME_PROFILES.md#macos-regular-file-spool)
fix mounts and cleanup.

Expose only host-to-guest read-only lanes and guest-to-host writable lanes under
`/run/operator/spool/`, each scoped to one launch with ordinary/control isolation.
FD 0 is null and FD 1/2 diagnostics; FD 3–6 are not assigned as protocol endpoints.
Bootstrap selects its transport from the allowlisted host-owned `OPERATOR_TRANSPORT`
value (`fifo` or `spool`) before reading `bootstrap`; it must match the mounted
layout and the later bootstrap binding. Campaign/model input cannot switch it. Every ready file contains one
complete shared JSON envelope, without a FIFO length prefix. Publish through a
temporary regular file and same-directory atomic rename; readers ignore temporary
files and reject unsafe paths/types/lengths before bounded parsing.

The host checks the combined macOS spool size every second against administrator
configuration `spool.max_bytes` (default 512 MiB). Exceeding it closes admission
and invokes Docker termination; the harness cannot change it. Follow the
[host size-check rules](HOST_RUNTIME_PROFILES.md#host-spool-size-check).

The spool is the only guest-writable host transport mount. It is not an artifact
output directory, journal or durable operation store. Inputs/skills/manifests stay
read-only.
Docker mount permissions restrict writes to assigned outbound lanes; the live
syscall filter permits the file operations needed for spool and scratch access. No network/socket is
required. Poll every 10 ms, servicing control and cumulative `consumed.json` ACKs
before ordinary traffic. Use sequence-named JSON files, one temporary publication
per writer/lane and a 1 KiB ACK limit. ACK only validated messages accepted into a
bounded queue; the producer deletes consumed messages. ACK is not operation completion.

Both transports preserve startup ordering, operation identities, queue ceilings,
deadlines and live-restore continuity. Spools have no pipe EOF: host/container status,
deadlines and explicit terminal state determine liveness. File disappearance is
not proof of consumption or non-execution. Spool recovery never resumes a failed
campaign or replays an unknown operation. Delete acknowledged messages promptly,
remove failed temporary writes and discard the full spool only after confirmed
container exit. Never age-delete outstanding messages and continue or reuse a
launch directory. Publication/ACK/full-queue waits have five-second bounds;
model/target response deadlines remain separate.

### 4.2 Framing and state

All four logical channels use the accepted [framing and flow-control rules](schemas/SHARED_CONTRACT.md#4-pipe-envelope-and-channels).
Ordinary frames are at most 4 MiB and control frames 64 KiB; bulk artifacts never
use control. Parser nesting is at most 32. Each ordinary queue holds at most two
frames/8 MiB and each control queue 16 frames/1 MiB. FIFO partial-frame transfer/blocked writes and spool publication/acknowledgement/full-queue waits
have a five-second timeout; response availability uses the operation deadline. Queue exhaustion is an explicit failure.

Use the five-message startup exchange in shared contract Section 6: host
`bootstrap`, guest `confinement_ready`, host `initialize`, guest `initialized`,
host `admission_open`. Bootstrap binds campaign/launch/container/initial revision,
package/release/profile and RunManifest; it contains no bulk inventory.
Only `initialize`, after runtime, journal and Docker lifecycle checks, authorizes
input reads and supplies fixed manifest descriptors. The guest verifies all inputs
before `initialized`; only `admission_open` permits ordinary dispatch. No message
selects executable paths, plugins, raw routes or runtime policy. The host derives
authority from the recorded endpoint assignment, never guest identity assertions.

The host treats ordinary/control messages as guest assertions. Admission opens
only after runtime/journal readiness and application input initialization complete.
Data on stdout/stderr has no authority under any parser.

Per-direction sequence counters start at zero for each channel pair and never
reset during the harness lifetime, including target restores. A sequence number is transport ordering, not durable
idempotency. Correlate response IDs to the sole outstanding ordinary request and
retain operation IDs across the complete dispatch/record lifecycle. Host queues
remain responsive to control and termination during provider/target waits.

The harness reads bounded files incrementally and uses nonblocking FIFOs or
bounded spool polling with an event loop compatible with the no-socket/no-thread profile. Do not assume a
general async framework is compatible: test its event-loop initialization,
wakeup mechanism, signal handling, cleanup and timeout behavior. No dependency
may silently create a helper thread, socket pair, worker process or HTTP server.

There is one channel assignment per harness launch and no reconnect/resume protocol.
A healthy target restore retains the process, scratch and all endpoints. Its
correlated result confirms the new active campaign revision before the next
ordinary request; launch manifests and startup identity remain unchanged.
Unexpected FIFO loss or failed spool lanes, invalid framing, a required control consumer that stops reading,
or host ownership loss cause terminal teardown. Expected EOF after accepted stop
is normal teardown. Launch-scoped `terminate` remains valid if it overtakes a restore
response on the ordinary channel. Channel access follows the private host-controlled
mount, OS-specific directional permissions and launch assignment.

## 5. Startup and live confinement

### 5.1 Startup policy

The runtime must execute the fixed interpreter to start the process. An OCI
seccomp profile that denies every exec from the outset would prevent that launch.
Use a two-stage release-owned confinement sequence rather than claim a blanket
exec denial is a runnable container configuration:

1. The host reserves journal space and persists the exact Docker container/daemon
   binding and transport assignment before initial guest execution.
   The Docker launcher installs namespaces, nonroot identity, read-only/mount/device
   restrictions, resource ceilings, `no_new_privs`, ordinary filesystem permissions and startup seccomp policy.
2. Execute only the fixed Python bootstrap with constant argv. Candidate argv:

   ```text
   /usr/bin/python3 -I -S -B /opt/operator/engine/bootstrap.py
   ```

   `-I` isolates Python from ambient Python environment and unsafe import paths;
   `-S` disables automatic site initialization; `-B` avoids bytecode writes.
   These are runtime hygiene, not an OS isolation boundary. See [Python command
   line documentation](https://docs.python.org/3/using/cmdline.html).
3. Bootstrap initializes the assigned FIFO/spool transport as Section 4.1 specifies, then
   establishes only literal release-owned import paths and loads required
   immutable modules/confinement code and prepares the reviewed transport event loop.
   Receive and validate host `bootstrap`.
   No scenario, skill, model, reference or target content is processed yet.
4. Install the irreversible live seccomp filter,
   deny additional exec/process creation and verify configuration success. The
   guest sends `confinement_ready`. The host verifies Docker settings against the installed MVP profile;
   the guest assertion is not independent filter attestation. Post-bootstrap
   enforcement is proven using the exact release and separate qualification probes.
5. At the host-controlled startup gate, verify journal and Docker lifecycle
   readiness. Send `initialize` over the assigned control channel. The harness verifies
   frozen manifests/inputs, exact skill inventory and prompt, then returns
   `initialized` identities/digests. Send `admission_open` only after these match.
   Apply separate 60-second confinement/readiness and input-initialization limits.

The startup syscall profile already denies network sockets, privilege changes
and unnecessary kernel primitives; its narrowly required exec is further limited
by fixed launch authority and immutable release files. Trusted bootstrap
code installs the final filter before untrusted data is consumed. A missing or
failed gate destroys the revision; never retry in place with a weaker profile.

### 5.2 Live policy

The final architecture-specific allowlist permits only demonstrated CPython,
bounded FIFO/spool file I/O, event-loop, time, randomness, signal, memory and exit needs.
Explicitly deny `execve`/`execveat`, fork/vfork/clone/clone3, socket/socketpair,
mount and newer mount APIs, unshare/setns, ptrace/process-memory access, BPF/perf,
kernel modules/kexec/reboot, raw devices, keyring and unneeded privilege/security
configuration operations. Account for alternate syscall ABIs; an amd64 filter
must not accidentally admit x32/i386 paths. Prefer allowlisting to an incomplete
list of familiar dangerous calls. Deny unneeded io_uring interfaces as well.

The single-threaded baseline simplifies final filter installation. Dependencies
requiring extra threads must be removed or qualified under a distinct reviewed
profile with exact clone flags, task ceilings and synchronization guarantees.
Never change the live filter based on a model error or retry suggestion.

Linux permits stacking filters under `no_new_privs`; adding a filter cannot
relax previously applied constraints. The final implementation must check all
return values and filter/thread scope. Denial semantics require qualification probes; production denial logging is not required. [The seccomp manual](https://man7.org/linux/man-pages/man2/seccomp.2.html)
documents these constraints; Docker's default profile alone does not specify
this proposed Python runtime.

Host policy controls protected process access and signals; private PID namespaces,
nonroot identity, descriptor hygiene and mounts prevent access to broker/runtime FDs.
Separate user-namespace remapping and additional filesystem policy are optional.
The container cannot read cgroup control handles or move itself outside its
accounted subtree. Do not expose broad writable bind mounts merely to simplify
artifact transfer. The host remains authoritative if the harness obtains arbitrary
in-process code execution and sends hostile but correctly framed requests.

## 6. Input, skill and output contracts

InputTreeManifest binds each canonical path, size, media type and raw SHA-256,
the EngineContext schema and ordered skill descriptors. Use the shared canonical
JSON recipe over its payload excluding its own digest/signature if present.
The manifest excludes itself from its file inventory. OCI digests use OCI rules;
native Interceptor resources keep their separate native recipes.

Use the versioned `InputTreeManifest` and data-bundle SkillBundle/SkillSet
contracts. Host publication produces normalized regular-file inventories from
SKILL.md/reference sources. Archive/media packaging belongs to the host publisher.
The required `/run/operator/manifests/` mount contains `input-tree.json`,
`skill-set.json` and fixed per-skill manifests. Startup descriptors carry raw
size/digest and canonical `object_digest`; path IDs resolve to fixed release paths.
Apply shared contract Section 7 limits: 8 MiB input manifest, 64 KiB skill-set
manifest, 2 MiB per-skill manifest and 40 MiB aggregate manifest bytes, with
relative paths at most 1,024 UTF-8 bytes/depth 16. Account these bytes separately
from data and exclude manifests from their own inventories.

Proposed data ceilings, subject to release testing and narrower host policy:

| Data | Ceiling |
|---|---|
| Effective prompt | 131,072 bytes; at most 16 append files |
| Skill set | 16 skills; 64 MiB aggregate normalized content |
| Individual skill | 8 MiB; 1,024 files; relative path depth 16 |
| Individual skill/reference file | 1 MiB unless an explicitly versioned input artifact contract permits more |
| Campaign input tree excluding separately counted skills | 64 MiB; 4,096 files; path depth 16 |
| Control frame / artifact part | 64 KiB encoded / 256 KiB raw |

All limits count actual normalized/encoded bytes as applicable and reject the
whole invalid selection. Model-visible file access uses a fixed manifest entry ID
and byte range, never arbitrary `/proc`, absolute host paths or a recursive scan.
The loader knows exact entrypoints and reads each selected SKILL.md in canonical
order. Record unread references, failed initialization and omitted optional data.

Outputs are untrusted byte artifacts. The harness declares purpose, media type,
expected size and digest; sends ordered bounded parts; and obtains a committed
receipt only after independent host verification and durable storage. Reject
out-of-order/conflicting parts and unknown artifact IDs. The guest cannot publish
a release or change the accepted input inventory through an output operation.
Uncommitted work is not recovered by mounting scratch after failure.

Native tool calls and results preserve original provider tool-call correlation.
Only a fully parsed, host-validated model result may lead to a registered local
tool call. Structured final conclusions refer to committed evidence/observation
IDs; they cannot create successful host audit records by assertion.

## 7. Shutdown and qualification

A healthy completion commits a v1alpha2 conclusion artifact and conclusion
record, then sends `engine.request_stop` under shared contract Sections 10–11.
An explicit unavailable conclusion remains possible. Operator's ordinary accepted
acknowledgement closes execution admission and requires exit within 5,000 ms;
finalization remains pending until host work completes. Expected EOF afterward
is normal teardown. Reserve up to 1 MiB/one conclusion slot within the artifact
budget and up to 30 seconds inside the campaign deadline, admitting only
conclusion artifact/record/stop operations during graceful finalization.
Immediate/fatal stop uses the exact-container Docker kill path, independently of
the campaign worker, without waiting for output, signal handlers, control delivery
or model responses. Docker unavailability is an explicit unconfirmed outcome;
see product Section 10.2. Journaling failure cannot delay the kill attempt.

No automatic restart, CRIU restore, container commit, live image replacement or
guest-memory checkpoint. Destroy work/tmp and transport assignments at terminal
harness teardown; retain host-committed artifacts, journal and audit. A healthy
target restore advances the target revision while the same harness keeps its
context, mounts and channels under the product's cumulative-budget rules.

Qualification must use a separate adversarial probe artifact under the same
runtime controls to exercise forbidden syscalls; do not ship probe programs in
the production image or infer denial from an absent tool. Verify both allowed
Python operation and denied bypasses, Linux FIFO opening/EOF, macOS spool isolation/
atomicity/bounds, descriptor leakage, mount/network state, journal failure handling,
worker-independent Docker kill and image/release-cache drift on all four supported
Linux/macOS host tuples. Docker API/VM failure must report unconfirmed termination. Keep startup-only exceptions distinguishable
from live permissions. All such tests remain unimplemented and unrun.
D1 is resolved: Docker mounts/permissions and two-stage seccomp are the MVP controls.
Spool write access is limited to the fixed outbound transport view. These checks
verify implementation behavior; no recurring OS/Docker version matrix is required.
