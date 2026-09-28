# Late native evidence collection

```sh
operatorctl campaign evidence collect --campaign CAMPAIGN_ID
operatorctl campaign evidence collect --campaign CAMPAIGN_ID --state-root /absolute/private/state --docker-bin /absolute/path/to/docker
```

Late collection MUST be an explicit administrator operation. Campaign startup
MUST NOT initiate it. The command MUST emit an
`operator.dev/evidence-collection/v1alpha1` JSON receipt containing the campaign,
run-manifest digest and per-session outcomes. Exit status MUST be zero only when
all selected sessions have verified, durably adopted complete evidence. Partial,
missing, invalid or unrecorded evidence MUST return nonzero without changing the
campaign's execution result. Preflight failures MUST include a fixed reason.

## Admission and provenance

Collection MUST acquire the installation lease and the selected campaign's writer
lock. An active worker or concurrent administrative collection MUST prevent it.
The entire execution journal and its referenced content MUST pass inspection.
The command MUST require retained preparation, an `evidence.policy` record and
at least one `evidence.session-selected` record. Missing historical policy or
selection MUST remain unavailable; current settings and current target contents
MUST NOT supply the missing provenance.

A saved Docker binding MUST be checked using its exact local endpoint, daemon ID,
full container ID and labels. Active or unconfirmed containers MUST block collection.
Absent and independently confirmed inactive containers permit collection. Without
a binding, an intact journal MUST prove that no launch start intent was recorded.
The command MUST NOT kill or remove containers, close native execution, attach,
restore, invoke a target, generate model requests, or resume a campaign.
Administrators MUST use the existing termination/cleanup facilities when needed.

The collector MUST use only journal-selected source and replacement sessions.
Each selection MUST match the original campaign, environment, application,
capability and native feedback-profile pins. A replacement MUST carry a known
parent session and the verified checkpoint ID/digest. An unknown replacement from
an uncertain restore MUST NOT be discovered from the current session list.
Before a new download, native status MUST match the saved Interceptor instance and
campaign and include the selected session. Worker attribution MUST NOT restrict
reading a session in the same campaign.

The frozen policy MUST control the local byte ceiling and per-session/overall
deadlines. Each selection supplies its saved native ceiling. The existing bounded
transfer, tar inspection and native provenance verifier MUST be used. A successful
transfer MUST preserve native partial/completeness markers. It MUST NOT imply
that target cleanup, campaign execution or reporting succeeded.

## Retention and repeated invocations

The original journal MUST remain unchanged. Already adopted archives MUST be
rehash-verified and reused without native contact. This includes an archive with
a journal adoption event whose subsequent collection-result event was lost.
Damaged retained bytes MUST be reported as invalid and MUST NOT be overwritten.
Verification interrupted by a deadline MUST remain unavailable, not corrupt.

Each new session collection MUST durably publish an intent before native contact.
A result MUST be published only after archive length/hash/provenance validation
and durable archive storage. Its immutable envelope MUST include the collection
time, frozen selection/policy digest, result digest, outcome, and adopted archive
path/provenance where applicable. An archive without a journal adoption event or
complete late-collection result MUST remain an orphan. An independently verified
redownload may reuse identical orphan bytes after rehashing them.

```
evidence-recovery/<native-session-id>/
  intent.json
  result.json
  retries/
    0001/intent.json
    0001/result.json
    ...
native-evidence/<native-session-id>/<archive-sha256>.tar
```

A command invocation MUST attempt each eligible selected session at most once.
A later explicit invocation MAY retry a transient failure, invalid incoming
archive, or interrupted download using a new numbered attempt. It MUST preserve
all earlier results and partial files. It MUST validate retained attempt history
before retrying; damaged audit records MUST NOT silently become a fresh attempt.
The implementation MUST bound retries to 1,000 per session and each result
record to 4 MiB. There MUST be no automatic retry loop or startup-triggered retry.

Complete and partial verified archives MUST be reused. Recorded native/local
size-limit failures and unavailable frozen native limits MUST NOT trigger another
download under unchanged limits. Administrator-exported archive import with an
explicitly increased limit remains a separate workflow. Missing results MUST
remain uncertain in the old attempt even if a later read succeeds.

Before downloading, the collector MUST check free space for two archive copies
at the smaller local/native ceiling, plus 8 MiB of result-publication capacity and
the original journal's free-space floor. Archive adoption MUST recheck space.
This is a logical allowance; other host processes can consume free space. Disk or
publication failure MUST remain explicit and MUST preserve previously adopted
archives. All collection attempts and archives belong to the same independently
purgeable campaign group. They MUST remain inaccessible to the guest.

`--config` MUST load the specified configuration. With an explicit `--state-root`
and no `--config`, collection MUST bypass current configuration, as administrative
termination does. Configuration may select the state root and Docker executable;
it MUST NOT replace saved Docker/native identities or collection limits. Raw
native/provider errors and credentials MUST NOT appear in CLI diagnostics.

## Validation

Tests MUST cover active workers and containers, damaged journals and identities,
frozen limits, source/replacement provenance, complete and partial archives,
size rejection, interrupted and failed publication, explicit retries, retained
archive corruption, lost live collection results, unchanged execution journals,
and reuse without network contact. Native host/Interceptor qualification remains
separate from scripted peers and checked native archive fixtures.
