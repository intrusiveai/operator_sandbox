# Harness finish, exit, and retained diagnostics

An accepted `engine.finish` closes execution admission. It does not establish that
the Harness exited successfully or that cleanup completed. Operator MUST observe
the exit status of the exact saved Docker container on the saved daemon before
removing it. No new wire message or contract identity is required.

## Completion rules

- Accepted finish plus an observed exit code of zero MUST count as successful
  Harness execution, unless cancellation, a deadline, forced termination, or an
  independent product/structured fatal failure also occurred.
- FIFO peer loss after accepted finish MUST fence new work but allow the remainder
  of the original five-second exit deadline. It MUST NOT renew the deadline or
  delay explicit administrator cancellation. Without accepted finish, ordinary
  failure/termination behavior applies.
- A Docker `die` notification is distinct from a broken event subscription,
  malformed event, or an unexpected lifecycle action. An event alone MUST NOT
  establish an exit code. Protocol errors MUST NOT become normal completion.
- A Docker probe canceled by the terminal fence MUST preserve that fence’s original
  cause instead of manufacturing an independent identity-check failure. Other
  inspection errors MUST remain failures.
- A rejected assessment followed by an accepted correction MUST remain rejection
  history, not the causal error for a later exit.
- Nonzero exit MUST fail execution. Zero exit without accepted finish MUST fail
  as incomplete. Unavailable status MUST remain unconfirmed (`exit_observed: false`,
  `exit_code: null`, and an explicit reason).
- Cleanup/evidence collection MUST continue. Successful Harness execution and
  incomplete cleanup MUST be reported separately.

## Retained observations

Operator writes `launch.exit` before removal, spending the existing reserved
terminal journal capacity. If that write fails, it MUST retain the stopped
container rather than remove the only remaining exit/log source. It still attempts
other owned cleanup and retains the secondary failure when possible.

The observation contains finish acceptance, assessment commitment, observed exit
code or unavailable reason, forced termination, the categorized original worker
cause, available causal diagnostic, and bounded stderr. Unknown worker error text
is omitted explicitly (`independent_worker_failure_details_omitted`), rather than
copying unrestricted backend errors. Existing structured model/workflow diagnostics
remain in the terminal result. A recovered assessment rejection is never used as
a fatal diagnostic after accepted finish.

Docker uses its local log driver with `max-size=1m`, `max-file=1`, and
`compress=false`. Operator reads at most 200 trailing log lines, keeps at most
8 KiB of stderr, discards stdout, and applies the existing credential and
control-character filtering before retention. Truncation, unavailable logs, and
unsafe-field omission are explicit. No Docker environment, authorization headers,
or configuration dump is collected. Guest text is diagnostic evidence, never an
instruction or an authoritative claim about execution. Host credentials are never
sent to the guest; redaction additionally removes common credential forms and URLs.

`launch.terminal` records the final cleanup facts alongside the exit observation.
`inspect`, campaign status, reports, exported event metadata, and failed start
completion records expose `harness_exit`. Foreground and supervised failures print
the same exit code, available cause, and retained-details reference. Older records
without this field remain readable and MUST NOT be interpreted as observed zero.

```text
Campaign execution failed during Harness exit (execution_failed):
Attack Harness exited with code 1 after Operator accepted engine.finish. Cause: RuntimeError: controlled post-finish failure
Worker termination cause: docker_exit_notification
Details: campaign journal event launch.exit
Execution will not resume.
```

```sh
operatorctl inspect --state-root /path/to/state --campaign CAMPAIGN_ID
operatorctl report --state-root /path/to/state --campaign CAMPAIGN_ID
operatorctl export --state-root /path/to/state --campaign CAMPAIGN_ID --output /new/export-directory
```

The returned exit outcome MUST be read separately from assessment claims and
cleanup state. If no causal Harness diagnostic was emitted, Operator says so;
it cannot reconstruct a swallowed exception.

## Controlled verification

`make test` covers identity/status failure cases, independent-error exclusion,
worker finalization, durable completion records, and equivalent CLI diagnostics.
The opt-in macOS/Docker Desktop test below uses an already cached image with
`/usr/bin/python3`, networking disabled, no pulls, a controlled Python spool peer,
the real Go worker, Docker events/status/logs/removal, and a controlled native
Interceptor peer. It builds a separate CLI to inspect retained data after removal.

```sh
OPERATOR_EXIT_TEST_IMAGE=operator-harness-native-test:dev \
  go test ./internal/preparation -run '^TestNativeDockerFinishExit$' -count=1 -v
```

Cases include natural exit, injected peer-loss notification before actual exit,
rejected/corrected assessment, nonzero exit with/without diagnostics, no accepted
finish, injected missing status, deadline/kill, and injected removal failure.
The peer-loss notification is injected because Docker Desktop uses spool transport;
it is not a claim of Linux Docker FIFO qualification. Both real FIFO EOF handling
and completion classification have focused regression coverage. These tests do not
qualify the production Harness image, a live model route, or a cloud campaign.
