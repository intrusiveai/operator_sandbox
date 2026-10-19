# Controlled finish-to-exit verification — 2026-10-19

## Original evidence and confirmed defects

The retained `aws-openai-gpt5-mini-010` journal has accepted finish at event 113,
the accepted response at 115, and terminal cleanup at 125. The terminal record
confirms an exited container, no attempted kill, accepted finish, and removal.
It does not contain the exit code or original worker error. The precise cause of
that historical failure remains unknown; the campaign was not replayed.

Source inspection and regressions establish that the former normal-stop predicate
excluded FIFO `ErrPeerLost`. Its fence could also trigger immediate kill before
the already accepted five-second exit deadline. Repeated native Docker testing
then reproduced a second race: a Docker probe canceled by an exit notification
returned a generic identity-check error, which replaced the original stop cause.
Both paths now preserve expected completion while retaining independent failures.

## Local verification

Host: macOS ARM64 with local Docker Desktop Linux/ARM64 containers. Cached test
image: `operator-harness-native-test:dev`. The test peer exchanges startup and
finish messages through the real file spool. Operator uses its real worker,
Docker event/inspection/logging/removal client, journals and report generation;
Interceptor is a controlled in-process peer. No image pull, model call, registry
lookup or cloud write is performed.

| Case | Required observation |
| --- | --- |
| Accepted finish, natural zero exit | Successful Harness execution, committed assessment, cleanup complete |
| Accepted finish, injected peer-loss notification before delayed actual exit | Original deadline respected, no kill, observed zero exit, success |
| Rejection followed by corrected accepted finish | Rejection retained as history; corrected finish and zero exit succeed |
| Accepted finish, exit 1 with stderr | Failed execution, original safe stderr cause retained; synthetic credential redacted |
| Accepted finish, silent exit 2 | Failed execution; explicitly no causal diagnostic available |
| Zero exit without finish | Failed/incomplete; acceptance remains false |
| Injected exit-status inspection failure | Unconfirmed, null exit code, explicit reason |
| Accepted finish with guest exceeding exit deadline | Forced kill, observed nonzero exit, failed execution |
| Successful Harness, injected removal failure | Harness outcome remains successful; cleanup incomplete and worker failure explicit |

Each native case invokes a separately compiled CLI for inspection after the worker
finishes, and generates a report from disk. Normal removal happens only after
`launch.exit` is durably written. Retained exit facts match the worker result.
A deterministic worker regression also cancels an in-flight Docker poll with an
exit notification and verifies that the original cause survives. Existing real
FIFO EOF coverage and new normal-stop/error-exclusion tests cover FIFO semantics.
CLI tests compare foreground and supervised diagnostics; durable start-record tests
verify that a recovered rejection does not replace the nonzero-exit cause. Legacy
records without exit observations remain readable.

The default suite (`make test`), static analysis (`go vet ./...`) and focused race
checks cover the related persistence, reporting, identity and completion paths.
These results do not establish production Harness, Linux Docker FIFO, or live cloud
campaign qualification. The explicit peer-loss case injects the notification on
macOS while using real Docker exit status; it does not claim Docker Desktop shares
FIFOs. The separate Harness session must address the swallowed-error behavior
identified in [the handoff](../ATTACK_HARNESS_EXIT_HANDOFF.md).
