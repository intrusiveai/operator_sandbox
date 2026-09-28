# Offline native evidence import

```sh
operatorctl campaign evidence import --campaign ID --session ID --archive retained.tar \
  --state-root /absolute/private/state --max-archive-bytes 8589934592
```

`--run DIR` MAY replace `--campaign ID` and state/config selection. The command
MUST obtain the installation lease and campaign writer lock, verify the complete
retained journal and preparation, and resolve the session from saved evidence
selections. It MUST NOT contact Docker, Interceptor, a model, or a secret store.

The source MUST be a regular non-symlink file. Import MUST bound its size, hash its
bytes, stage a second digest-checked copy, and apply the same tar framing, member,
blob, native identity, checkpoint-lineage and provenance validation as collection.
It MUST NOT extract archive entries. Cancellation MUST interrupt streaming.

The acceptance ceiling defaults to the saved collection policy. An explicit
`--max-archive-bytes` MUST be a positive JSON-safe integer and MAY exceed both the
original local limit and the original Interceptor API limit. Zero selects the saved
default. The transfer receipt's two capacity fields MUST both record this offline
acceptance ceiling; they MUST NOT be interpreted as the running service's capacity.
Import MUST reserve space for two archive copies and 8 MiB of metadata above the
original free-space floor. This is a free-space check, not a filesystem reservation.

Verified bytes MUST be retained under the campaign's `native-evidence` directory.
An immutable digest-checked `evidence-imports/<archive-sha256>.json` record MUST
adopt them, binding the run manifest and native provenance. Import MUST preserve
the original journal, online collection results and earlier imports. The campaign
MUST accept at most 1,000 distinct imported archives. Each record MUST fit 4 MiB.
Identical imports MUST reuse rehashed retained bytes and the original record;
existing corrupt bytes MUST fail verification rather than be overwritten.
An archive published without its adoption record MUST remain an orphan until a
later explicit import verifies and adopts it. Incomplete metadata publication
MUST fail closed and remain available for inspection.

Exit status zero means verified adoption, including an explicitly partial native
archive. It MUST NOT imply successful execution, completed cleanup, or complete
evidence. Reporting MUST expose the native completeness and gaps separately.

Tests MUST cover active writers, wrong sessions/lineage, links, limits, malformed
archives, cancellation, duplicate adoption, corrupt retained bytes, and unchanged
execution records. No live infrastructure qualification is implied by fixtures.
