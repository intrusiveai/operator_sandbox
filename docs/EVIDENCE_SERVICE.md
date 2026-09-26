# Native evidence finalization

`internal/campaignservice` collects native evidence after bounded native closure,
cleanup and optional target stop. The saved Docker terminator runs independently;
no export, journal lock or evidence deadline gates its kill attempt. Collection
failure does not change the campaign's recorded execution outcome.

## Selection and limits

Preparation retains the native target identity and attach export ceiling. Service
construction records `evidence.session-selected` for the initial session. Verified
restores record another selection, including the exact source checkpoint digest,
before exposing the replacement. Duplicate restore replies do not add selections.
Each selection reserves 2 MiB of journal capacity for its proof and outcome.
Source sessions remain in the inventory after later restores.

The host MUST supply its configured `evidence.max_archive_bytes` through
`EvidenceConfig`. An omitted config uses 4 GiB. The native peer defaults to the
service's Interceptor client. Collection uses the smaller local/native ceiling,
with default deadlines of two minutes per session and five minutes for the batch.
Host integrations may select positive deadlines up to five minutes per session
and thirty minutes total. These deadlines do not extend campaign execution.

Each session receives one automatic export request. There is no automatic retry
of capacity, transport or integrity failures. Missing exporters, native limits,
storage, or remaining time produce explicit `missing` outcomes. Invalid archive
structure/provenance produces `invalid`; verified exports produce `complete` or
`partial` according to native closure, evaluation and interruption evidence.
An unavailable ordinary dispatch gate produces explicit unrecorded missing
outcomes, without exporting concurrently with an unresolved native operation.

## Verification

The native client streams exact bounded bytes into a private temporary file,
checks the transport digest and tar structure, then verifies:

- Campaign/session/environment/application/capability/profile identities against
  the independently selected target.
- Native Go-JSON bundle, event, state and checkpoint hashes. Shared-contract JCS
  is not substituted for those serialization recipes.
- Contiguous session-bound event/state chains and referenced blob digests/sizes.
- Execution-state bytes against the state journal, native operation fingerprints,
  response states and campaign association. Worker/revision values are attribution.
- Checkpoint positions and restore-source chains against the selected source
  checkpoint digest, including source blob availability.
- Attempt/artifact registries, attempt ancestry and event membership, canonical
  JSON artifacts, evaluation records and native completeness markers.

Verification bounds are 10,000 archive entries by default, 64 MiB per JSON metadata
member, 32 MiB per journal line, and 100,000 records per journal. An exceeded bound
is an explicit rejected export, not silent truncation. These host verification
limits are independent of the guest's artifact and observation quotas.

Hashes prove internal integrity and binding to saved host facts. They are not
signatures and do not establish the truth of detector assertions. A complete
native session export does not itself establish complete campaign coverage or
successful target cleanup. Protected archives never become harness observations.

## Durable retention

Archives live in the owning campaign group:

```text
campaigns/<campaign>/native-evidence/<session>/<archive-sha256-hex>.tar
```

`campaign.Writer.RetainEvidence` accepts only a verified archive handle. It streams
the archive again, rechecks length/hash, checks configured free-space headroom,
fsyncs and publishes a private regular file without replacing existing data.
Only then does `evidence.adopted` commit its path and full provenance receipt.
Raw archive bytes are outside the journal quota; the adoption proof consumes it.
Temporary files are removed on success and failure. Interrupted publication may
leave a final archive without an adoption event; readers MUST treat it as an
orphan. It remains inside the independently purgeable campaign group.

`evidence.collection-result` records one explicit outcome per selected session,
including both limits. `TerminalResult.Evidence` returns detached outcome copies.
`recorded: false` signals that an outcome could not be committed. A successful
archive-adoption event remains valid even if its later result event cannot commit.
Journal inspection alone does not verify external archive bytes; reporting MUST
also recheck the retained descriptor and archive hash.

`interceptor.StageEvidence` feeds host-selected retained bytes into the same
bounded inspection/provenance pipeline. It is a library primitive, not a CLI
import command. Retained imports MUST use identities recovered from verified host
records, not identities taken from the supplied archive. Imports never resume
execution or invoke a model.

## Remaining integration

Production host-service construction must wire the selected config, native client
and campaign lifetime into these libraries. Administrative collection/import,
purge, retained reporting and the Docker launcher are separate integration work.
Native exporter fixtures and protocol tests do not qualify a Docker runtime.
