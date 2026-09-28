# Campaign preparation and service startup

The CLI MUST persist a typed start request and its submitted-run link before
contacting the OS service manager. A worker MUST permanently claim that exact
request once, revalidate its frozen input fingerprint, finish online preparation,
and persist acceptance before creating the harness container.

```sh
operatorctl campaign prepare --run ./runs/example
operatorctl campaign start --run ./runs/example
operatorctl campaign status --run ./runs/example
operatorctl campaign logs --run ./runs/example --after 0 --limit 1000
operatorctl campaign wait --run ./runs/example
```

`prepare` MUST perform offline validation and save the selected identities and
input fingerprint. It MUST NOT contact Docker, Interceptor, model providers,
secret stores, the release service or the OS service manager. Its `submitted`
phase describes a retained request, not a running or accepted campaign. The
installed private state root MUST already exist. `start` MUST perform the same
preparation when no saved link exists, then submit its fixed worker entrypoint.

Both commands accept repeatable `--skill DIGEST` or exclusive `--skill-set FILE`,
plus `--system-prompt FILE` or
repeatable `--system-prompt-append FILE`. Replacement and extension are exclusive.
Omitted selections MUST reuse the saved selection; a fresh campaign MUST use the
canonical defaults. An explicit changed selection or changed source content MUST
conflict with the saved request. `--new-campaign` MUST select new identities and
replace the run link while retaining the previous campaign and start records.
An unreadable or dangling link MUST NOT silently initiate another campaign.
Frozen-set verification and loader matching MUST follow [the skill contract](SKILLS.md#frozen-sets-and-reversible-removal).

`start` MUST return a `operator.dev/start-observation/v1alpha1` object, including
the start-request ID, campaign ID, request digest, phase, service-submission
outcome and optional accepted/completion records. Successful service submission
MUST NOT imply campaign acceptance. The default acceptance wait is 135 seconds;
`--timeout` accepts a positive duration up to ten minutes. Timeout, cancellation
or an uncertain service reply MUST return nonzero with the saved lookup key.
They MUST NOT cancel the worker or allocate a replacement key.

The `--run` observers MUST resolve the saved state root and campaign ID without
loading current installation settings. They MUST NOT accept simultaneous
`--campaign`, `--state-root` or `--config` overrides. Their observation receipt
includes a `start` object; a missing journal before acceptance MUST remain
unverified with unknown execution state. `wait` MUST return nonzero for a recorded
worker failure. Observation MUST tolerate a bounded 250 ms atomic-publication
window without accepting unfinished records. No read operation grants permission
to resume a claimed campaign.

Online preparation MUST persist the attach intent and verified native routing
records before using the target to build its campaign journal. A preparation
failure MUST retain these campaign-scoped records for the next startup's
[reconciliation](STARTUP_RECOVERY.md#native-attach-before-campaign-preparation).
Failure or cleanup MUST NOT clear the original worker claim or allow retrying the
same campaign. A lost attach reply remains explicitly unconfirmed.

## OS service binding

The administrator MUST install `operatorctl` at a stable absolute executable path
that is not group/world writable. They MUST provision the configured private
state root and configuration, contract package, profiles, local Docker image and
Docker access for the same account that submits campaigns. CLI startup MUST NOT
change accounts, run sudo, or prompt for service-manager authorization.

Linux MUST submit a transient systemd service through `/usr/bin/systemd-run`.
Non-root installations MUST have an available user service manager; root uses
the system manager. The fixed service name is
`operator-campaign-<start-request-id>`. Submission MUST use `Type=exec`,
`Restart=no`, `UMask=0077`, a 90-second stop grace, no terminal attachment and
disabled argument environment expansion. There are no boot/login timers or
enabled units. The service manager owns the worker after submission, as defined
by [systemd's transient-service contract](https://github.com/systemd/systemd/blob/main/man/systemd-run.xml).

macOS MUST submit to the logged-in account's `gui/<uid>` launchd domain. Root
submission MUST fail. The generated LaunchAgent definition MUST reside privately
in `starts/<start-request-id>/worker.plist`, with label
`ai.intrusive.operator.campaign.<start-request-id>`, `RunAtLoad=false`,
`KeepAlive=false`, umask 0077 and a 90-second exit grace. The definition MUST carry
the fixed executable and argument array using XML escaping. Startup MUST use
`launchctl bootstrap` followed by `launchctl kickstart` without `-k`. It MUST NOT
install an automatically loaded login job. The installed `launchctl(1)` and
`launchd.plist(5)` manuals define these host interfaces.

Both managers MUST receive only `_worker --state-root ... --request-id ...
--request-digest ...`. Arbitrary commands, shell strings, submitted environment
variables and credentials MUST NOT be forwarded. Provider workload identity
MUST be configured for the service account/manager independently of a terminal's
environment. Worker diagnostics MUST remain bounded and omit raw provider errors.
SIGINT, SIGTERM and SIGHUP MUST cancel the worker into terminal cleanup.

Each manager command MUST have a bounded 15-second submission context. A failed
command MUST be reported as unconfirmed: a worker may already exist. An already
claimed request MUST NOT be resubmitted. A lost reply before the permanent claim
may leave a registered, unstarted job; that outcome requires inspection of the
same saved request and service, never a replacement execution. Administrative retirement now durably rejects late claims and reconciles the
exact service registration before purge; see [start-request retirement](START_REQUESTS.md#administrative-retirement).

Scripted manager tests validate arguments, escaping, input binding, permanent
claims and uncertain replies. The opt-in `OPERATOR_LIVE_SERVICE_TEST=1 go test
./internal/supervisor -run TestLiveLaunchAgentRunsFixedWorker -count=1` test
successfully exercised the local macOS ARM64 GUI domain: bootstrap, explicit
kickstart, real fixed-worker execution, durable pre-Docker preparation failure,
and removal of its temporary service. This does not qualify live systemd, Docker,
Python confinement or full campaign execution. The
[host profile gate](../HOST_RUNTIME_PROFILES.md) still applies.
