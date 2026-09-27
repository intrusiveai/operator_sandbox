# Durable campaign start requests

`internal/startrequest` stores host-owned requests and worker claims under the
private installation root. Its records are outside the shared host/harness wire
contract and MUST NOT be mounted into the guest.

```text
<state-root>/starts/<32-hex-start-request-id>/
  request.json
  owner.json          # once claimed, including after worker loss
  accepted.json       # only after the launch session is durably prepared
  completion.json     # finished or failed; fixed host reason code
```

Every file contains a closed typed value and its canonical SHA-256 digest.
Directories are private and owned by the executing user or root; files are private
regular singly linked files. Publication uses a synced temporary file, atomic
no-replace link, temporary-name removal and directory sync. Records are bounded to
256 KiB. Partial publication remains explicit uncertainty and cannot grant execution.

`New` derives a request from offline-verified `hostrun.InstalledInputs`. It binds
the effective installation paths/endpoint, exact input fingerprint and host-assigned
selection. The fingerprint includes effective defaults as well as configuration
file bytes. The worker MUST reload with the recorded defaults and verify the same
fingerprint before online preparation. These private records contain source paths
and selectors, not secret values or guest-selected execution settings.

`Save` binds the start key immutably. An identical request returns its existing
status, including after completion. Changed content conflicts. A duplicate request
MUST NOT regenerate identities, launch a second worker or reopen completed execution.

`ClaimOnce` publishes exactly one owner identity for that request digest. Concurrent
claimants cannot both win. A claim survives process death; a replacement process
MUST NOT interpret the dead worker's claim as permission to execute again. Such a
request remains claimed/unknown until cleanup and reporting establish further
facts. Closing the owner handle does not delete the claim.

The owning worker records acceptance only after complete launch preparation. The
receipt MUST match campaign, launch and start-request IDs and include a valid
RunManifest digest. Repeated identical acceptance/completion writes are idempotent;
changed results conflict. A worker can record failure before acceptance, but cannot
record `finished` before acceptance. Completion uses a fixed host reason code,
never a raw provider/secret-store error. Detailed execution and native-effect
outcomes remain in campaign evidence; `finished` is not an experiment-success claim.

Read-only phases are `submitted`, `claimed`, `accepted`, `finished` or `failed`.
These describe durable records, not process liveness. Missing or malformed records,
identity mismatch and pending publications MUST be reported as uncertainty.

Administrative purge MUST associate each start group with its request's campaign
ID, removing its metadata along with that campaign's retained resources. No start
group contains a shared artifact/blob store. Active or unresolved worker claims
must be reconciled before purge; deletion must not make a pending job executable.

Tests cover immutable replay/conflict, concurrent claims, acceptance/completion
ordering, partial/corrupt/unsafe files, competing publication and a claimed child
process killed without cleanup. Run-directory links and the fixed worker entrypoint are now implemented;
OS supervisor submission remains the next integration.

## Run-directory links and worker dispatch

`LockRun` serializes starts for one private submitted-run directory. The frontend
MUST hold it while selecting/publishing a request and submitting its supervisor
job, then release it before waiting. It uses a separate stable `start.lock` inode;
closing a CLI observer does not release a worker's execution lease.

`start.json` binds the run to a private start-request group by state root, key,
campaign and request digest. Readers MUST verify the referenced request and exact
run-directory binding. Copying a link to another submission cannot select its
campaign. Identical publication is idempotent. Selecting a different campaign
requires explicit `--new-campaign`; replacement preserves historical start groups.
After a purge removes the referenced metadata, the old pointer cannot silently
start again; explicit new-campaign selection may replace it.

The installed supervisor invokes the fixed entrypoint:

```sh
operatorctl _worker --state-root /private/installed/state \
  --request-id HEX32 --request-digest sha256:HEX64
```

The command accepts only a durable request binding. It claims once, reloads frozen
inputs, checks the fingerprint, performs online preparation, records acceptance,
then executes the prepared session. Input/online preparation has a two-minute
ceiling. The campaign retains its separately frozen execution deadline. Failed
acceptance cancels the never-started session; it cannot proceed to Docker start.
Worker completion is recorded with a fresh five-second recording context even if
execution was cancelled. Errors do not produce a second claim or automatic retry.
The worker's stdout is one bounded host result; diagnostics use fixed reason codes,
not raw configuration/provider/secret-store errors. A terminal worker result is
separate from experiment success and complete evidence.

`operatorctl version` reports the installed compatibility version. Release builds
set it with `go build -ldflags '-X main.operatorVersion=VERSION' ./cmd/operatorctl`;
the initial source version is `0.1.0`. A version label does not qualify a platform,
image, provider or secret store.
